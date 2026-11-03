// Package backup implements the disaster-recovery side of PLAN §4.5: the
// periodic logical dump of PostgreSQL into the "backup" storage class, its
// retention, the restore of a dump and the recovery kit (the secrets that
// are not in any backup: configuration, master key, SSH host keys).
package backup

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

// Policy is the storage class holding the backups.
const Policy = "backup"

// DumpPrefix is the class-relative prefix of the PostgreSQL dumps; each is
// DumpPrefix + "<UTC timestamp>.dump" in pg_dump custom format.
const DumpPrefix = "postgres/"

const dumpTimeFormat = "20060102T150405Z"

// DumpTimeout bounds pg_dump and pg_restore.
const DumpTimeout = 6 * time.Hour

// Dump is a stored PostgreSQL dump.
type Dump struct {
	Key  string // class-relative
	Time time.Time
	Size int64
}

// connArgs splits a database URL into the pg_dump/pg_restore connection
// argument and the environment: the password goes to PGPASSWORD so that it
// appears neither in the process list nor in error messages.
func connArgs(dsn string) (string, []string) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil {
		return dsn, nil
	}
	pw, ok := u.User.Password()
	if !ok {
		return dsn, nil
	}
	u.User = url.User(u.User.Username())
	return u.String(), []string{"PGPASSWORD=" + pw}
}

// DumpDatabase runs pg_dump on dsn and stores the result in class under
// DumpPrefix. The dump is spooled to a temporary file first: the class
// needs its size and digest before the upload.
func DumpDatabase(ctx context.Context, class *tier.Class, dsn string, now time.Time) (Dump, error) {
	f, err := os.CreateTemp("", "ervisio-pgdump-*")
	if err != nil {
		return Dump{}, fmt.Errorf("pg_dump spool: %w", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	h := sha256.New()
	conn, env := connArgs(dsn)
	if err := sys.Exec(ctx, sys.Cmd{
		Argv:    []string{"pg_dump", "--format=custom", "--no-password", "--dbname=" + conn},
		Env:     env,
		Stdout:  io.MultiWriter(f, h),
		Timeout: DumpTimeout,
	}); err != nil {
		return Dump{}, fmt.Errorf("pg_dump: %w", err)
	}
	size, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return Dump{}, fmt.Errorf("pg_dump spool: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Dump{}, fmt.Errorf("pg_dump spool: %w", err)
	}
	t := now.UTC().Truncate(time.Second)
	key := DumpPrefix + t.Format(dumpTimeFormat) + ".dump"
	if _, err := class.Put(ctx, key, f, size, h.Sum(nil)); err != nil {
		return Dump{}, fmt.Errorf("store %s: %w", key, err)
	}
	return Dump{Key: key, Time: t, Size: size}, nil
}

// ListDumps returns the dumps on the primary target of class, oldest first.
// It reads the target directly, so it works on an empty database too.
func ListDumps(ctx context.Context, class *tier.Class) ([]Dump, error) {
	prefix := class.FullKey(DumpPrefix)
	var out []Dump
	err := class.Primary().Store.List(ctx, prefix, func(i blob.Info) error {
		name, ok := strings.CutPrefix(i.Key, prefix)
		if !ok {
			return nil
		}
		ts, ok := strings.CutSuffix(name, ".dump")
		if !ok {
			return nil
		}
		t, err := time.Parse(dumpTimeFormat, ts)
		if err != nil {
			return nil //nolint:nilerr // not a dump: ignored
		}
		out = append(out, Dump{Key: DumpPrefix + name, Time: t, Size: i.Size})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list dumps: %w", err)
	}
	slices.SortFunc(out, func(a, b Dump) int { return a.Time.Compare(b.Time) })
	return out, nil
}

// Prune deletes the dumps older than retention, always keeping the newest
// keep ones. It returns how many were deleted.
func Prune(ctx context.Context, class *tier.Class, retention time.Duration, keep int, now time.Time) (int, error) {
	if retention <= 0 {
		return 0, nil
	}
	dumps, err := ListDumps(ctx, class)
	if err != nil {
		return 0, err
	}
	n := 0
	for i, d := range dumps {
		if len(dumps)-i <= keep || now.Sub(d.Time) <= retention {
			break
		}
		if err := class.Delete(ctx, d.Key); err != nil && !errors.Is(err, blob.ErrNotFound) {
			return n, fmt.Errorf("delete %s: %w", d.Key, err)
		}
		n++
	}
	return n, nil
}

// OpenDump reads a dump straight from the primary target of class (the
// database that indexes the class may be the one being restored).
func OpenDump(ctx context.Context, class *tier.Class, key string) (io.ReadCloser, error) {
	rc, _, err := class.Primary().Store.Get(ctx, class.FullKey(key), nil)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", key, err)
	}
	return rc, nil
}

// RestoreDatabase feeds a custom-format dump to pg_restore on dsn,
// replacing the objects it contains.
func RestoreDatabase(ctx context.Context, dump io.Reader, dsn string, log *slog.Logger) error {
	conn, env := connArgs(dsn)
	var stderr strings.Builder
	err := sys.Exec(ctx, sys.Cmd{
		Argv:    []string{"pg_restore", "--clean", "--if-exists", "--no-owner", "--single-transaction", "--no-password", "--dbname=" + conn},
		Env:     env,
		Stdin:   dump,
		Stderr:  &stderr,
		Timeout: DumpTimeout,
	})
	if s := strings.TrimSpace(stderr.String()); s != "" && log != nil {
		log.Warn("pg_restore output", "stderr", s)
	}
	if err != nil {
		return fmt.Errorf("pg_restore: %w", err)
	}
	return nil
}

// Latest returns the newest dump, or false when there is none.
func Latest(dumps []Dump) (Dump, bool) {
	if len(dumps) == 0 {
		return Dump{}, false
	}
	return slices.MaxFunc(dumps, func(a, b Dump) int { return cmp.Compare(a.Time.Unix(), b.Time.Unix()) }), true
}
