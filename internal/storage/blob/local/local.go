// Package local implements blob.Store on a local directory.
//
// Every access goes through an os.Root opened on the base directory, so even a
// key that slipped past validation cannot escape it. Writes go to a temporary
// file in the destination directory, are fsynced, then renamed into place and
// the directory is fsynced: an object is either fully present or absent.
package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

const (
	dirPerm  = 0o750
	filePerm = 0o640
)

// casLocks serialises conditional writes per absolute path, shared by every
// Store of the process so two Stores on the same directory stay consistent.
var casLocks blob.KeyedMutex

// Store is a blob.Store on a local directory.
type Store struct {
	name string
	dir  string
	root *os.Root
}

var (
	_ blob.Store             = (*Store)(nil)
	_ blob.ConditionalPutter = (*Store)(nil)
)

// New opens (creating it if needed) the directory dir as the target name.
func New(name, dir string) (*Store, error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("local %s: create base dir: %w", name, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("local %s: open base dir: %w", name, err)
	}
	return &Store{name: name, dir: dir, root: root}, nil
}

// Close releases the directory handle.
func (s *Store) Close() error { return s.root.Close() }

// Name implements blob.Store.
func (s *Store) Name() string { return s.name }

// Caps implements blob.Store.
func (s *Store) Caps() blob.Caps {
	return blob.Caps{Range: true, AtomicRename: true, ConditionalPut: true}
}

// Put implements blob.Store.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, o blob.PutOpts) (blob.Info, error) {
	return s.put(ctx, key, r, size, nil, o)
}

// PutConditional implements blob.ConditionalPutter. Conditional writes are
// serialised per key inside the process.
func (s *Store) PutConditional(ctx context.Context, key string, r io.Reader, size int64, c blob.Condition, o blob.PutOpts) (blob.Info, error) {
	return s.put(ctx, key, r, size, &c, o)
}

func (s *Store) put(ctx context.Context, key string, r io.Reader, size int64, c *blob.Condition, o blob.PutOpts) (info blob.Info, err error) {
	if err := blob.ValidateKey(key); err != nil {
		return blob.Info{}, err
	}
	vr, err := blob.NewVerifyReader(ctx, r, size, o.SHA256)
	if err != nil {
		return blob.Info{}, err
	}
	dir := path.Dir(key)
	if dir != "." {
		if err := s.root.MkdirAll(dir, dirPerm); err != nil {
			return blob.Info{}, s.wrap("put", key, err)
		}
	}
	tmp, err := s.createTemp(dir)
	if err != nil {
		return blob.Info{}, s.wrap("put", key, err)
	}
	tmpName := tmp.Name()
	tmpRel := path.Join(dir, path.Base(tmpName))
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = s.root.Remove(tmpRel)
		}
	}()
	if _, err = io.Copy(tmp, vr); err != nil {
		return blob.Info{}, s.wrap("put", key, err)
	}
	if err = tmp.Sync(); err != nil {
		return blob.Info{}, s.wrap("put: fsync", key, err)
	}
	if err = tmp.Close(); err != nil {
		return blob.Info{}, s.wrap("put: close", key, err)
	}
	if c != nil {
		unlock := casLocks.Lock(path.Join(s.dir, key))
		defer unlock()
		var cur *blob.Info
		if st, serr := s.Stat(ctx, key); serr == nil {
			cur = &st
		} else if !errors.Is(serr, blob.ErrNotFound) {
			return blob.Info{}, serr
		}
		if err = blob.CheckCondition(*c, cur); err != nil {
			return blob.Info{}, err
		}
	}
	if err = s.bumpMtime(tmpRel, key); err != nil {
		return blob.Info{}, s.wrap("put", key, err)
	}
	if err = ctx.Err(); err != nil {
		return blob.Info{}, err
	}
	if err = s.root.Rename(tmpRel, key); err != nil {
		return blob.Info{}, s.wrap("put: rename", key, err)
	}
	if err := s.syncDir(dir); err != nil {
		return blob.Info{}, s.wrap("put: fsync dir", key, err)
	}
	return s.Stat(ctx, key)
}

// bumpMtime makes sure the new file's mtime is later than the one it replaces,
// so that the ETag (size + mtime) always changes on overwrite.
func (s *Store) bumpMtime(tmpRel, key string) error {
	old, err := s.root.Stat(key)
	if err != nil {
		return nil //nolint:nilerr // nothing to replace
	}
	nw, err := s.root.Stat(tmpRel)
	if err != nil {
		return err
	}
	if nw.ModTime().After(old.ModTime()) {
		return nil
	}
	t := old.ModTime().Add(time.Microsecond)
	return s.root.Chtimes(tmpRel, t, t)
}

func (s *Store) createTemp(dir string) (*os.File, error) {
	for range 10 {
		var b [12]byte
		_, _ = rand.Read(b[:])
		name := path.Join(dir, blob.ReservedPrefix+"tmp-"+hex.EncodeToString(b[:]))
		f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, err
	}
	return nil, errors.New("cannot create a unique temporary file")
}

func (s *Store) syncDir(dir string) error {
	d, err := s.root.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !ignorableDirSyncErr(err) {
		return err
	}
	return nil
}

// Get implements blob.Store.
func (s *Store) Get(ctx context.Context, key string, rng *blob.Range) (io.ReadCloser, blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return nil, blob.Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, blob.Info{}, err
	}
	f, err := s.root.Open(key)
	if err != nil {
		return nil, blob.Info{}, s.wrap("get", key, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, blob.Info{}, s.wrap("get", key, err)
	}
	if !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, blob.Info{}, fmt.Errorf("local %s: get %s: %w", s.name, key, blob.ErrNotFound)
	}
	info := infoOf(key, st)
	start, end, err := blob.ResolveRange(rng, info.Size)
	if err != nil {
		_ = f.Close()
		return nil, blob.Info{}, err
	}
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, blob.Info{}, s.wrap("get: seek", key, err)
		}
	}
	return &rangeReader{Reader: io.LimitReader(f, end-start), f: f}, info, nil
}

type rangeReader struct {
	io.Reader
	f *os.File
}

func (r *rangeReader) Close() error { return r.f.Close() }

// Stat implements blob.Store.
func (s *Store) Stat(ctx context.Context, key string) (blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return blob.Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return blob.Info{}, err
	}
	st, err := s.root.Stat(key)
	if err != nil {
		return blob.Info{}, s.wrap("stat", key, err)
	}
	if !st.Mode().IsRegular() {
		return blob.Info{}, fmt.Errorf("local %s: stat %s: %w", s.name, key, blob.ErrNotFound)
	}
	return infoOf(key, st), nil
}

// Delete implements blob.Store.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := blob.ValidateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	st, err := s.root.Lstat(key)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil
	}
	if err != nil {
		return s.wrap("delete", key, err)
	}
	if !st.Mode().IsRegular() {
		return nil
	}
	if err := s.root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return s.wrap("delete", key, err)
	}
	return nil
}

// List implements blob.Store.
func (s *Store) List(ctx context.Context, prefix string, fn func(blob.Info) error) error {
	if err := blob.ValidatePrefix(prefix); err != nil {
		return err
	}
	start := "."
	if i := strings.LastIndexByte(prefix, '/'); i >= 0 {
		start = prefix[:i]
	}
	err := fs.WalkDir(s.root.FS(), start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == start && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if p != start && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			// Prune directories that cannot contain matching keys.
			if p != "." && !strings.HasPrefix(p+"/", prefix) && !strings.HasPrefix(prefix, p+"/") {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.HasPrefix(p, prefix) {
			return nil
		}
		st, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil // deleted while listing
		}
		if err != nil {
			return err
		}
		if ferr := fn(infoOf(p, st)); ferr != nil {
			return &fnError{ferr}
		}
		return nil
	})
	var fe *fnError
	if errors.As(err, &fe) {
		return fe.err
	}
	if err != nil {
		return fmt.Errorf("local %s: list %q: %w", s.name, prefix, err)
	}
	return nil
}

// fnError carries an error returned by the List callback through WalkDir.
type fnError struct{ err error }

func (e *fnError) Error() string { return "list callback" }

func infoOf(key string, st fs.FileInfo) blob.Info {
	mt := st.ModTime()
	return blob.Info{
		Key:     key,
		Size:    st.Size(),
		ETag:    fmt.Sprintf("%x-%x", st.Size(), mt.UnixNano()),
		ModTime: mt,
	}
}

func (s *Store) wrap(op, key string, err error) error {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return fmt.Errorf("local %s: %s %s: %w", s.name, op, key, blob.ErrNotFound)
	}
	return fmt.Errorf("local %s: %s %s: %w", s.name, op, key, err)
}

// ignorableDirSyncErr reports errors from fsync on a directory that only mean
// the platform does not support it.
func ignorableDirSyncErr(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EBADF)
}
