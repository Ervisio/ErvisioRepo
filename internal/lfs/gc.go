package lfs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
)

// GCArgs runs the LFS garbage collector.
//
// The collector is conservative:
//   - rows of deleted repositories are dropped and their quota released;
//   - rows of a transferred repository are re-charged to the new owner;
//   - in hot repositories, an object is dropped only when no blob reachable
//     from any ref or reflog is a pointer to it and it was neither uploaded
//     nor seen referenced within GCGrace (uploads precede the push);
//   - stored content is deleted only when no repository references it and
//     the storage record is older than GCGrace.
//
// Cold repositories are never scanned: their rows are kept.
type GCArgs struct{}

// Kind implements river.JobArgs.
func (GCArgs) Kind() string { return "lfs_gc" }

// InsertOpts: a failed run is retried by the next periodic run.
func (GCArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

// GC tuning.
const (
	GCInterval    = 6 * time.Hour
	GCGrace       = 14 * 24 * time.Hour
	gcBatch       = 500
	gcRepos       = 50
	maxPointerLen = 1024
)

// JobsOption registers the GC worker and its periodic schedule.
func (s *Server) JobsOption() jobs.Option {
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &gcWorker{s: s})
		r.Periodic = append(r.Periodic, river.NewPeriodicJob(river.PeriodicInterval(GCInterval),
			func() (river.JobArgs, *river.InsertOpts) { return GCArgs{}, nil }, nil))
	}
}

type gcWorker struct {
	river.WorkerDefaults[GCArgs]
	s *Server
}

func (w *gcWorker) Timeout(*river.Job[GCArgs]) time.Duration { return 2 * time.Hour }

func (w *gcWorker) Work(ctx context.Context, _ *river.Job[GCArgs]) error {
	s := w.s
	before := pgtype.Timestamptz{Time: s.now().Add(-GCGrace), Valid: true}
	var errs []error
	if n, err := s.q.DeleteExpiredLFSTokens(ctx, pgtype.Timestamptz{Time: s.now(), Valid: true}); err != nil {
		errs = append(errs, fmt.Errorf("delete expired lfs tokens: %w", err))
	} else if n > 0 {
		s.o.Log.InfoContext(ctx, "git lfs gc: expired tokens removed", "count", n)
	}
	errs = append(errs, s.gcOrphans(ctx), s.gcCharges(ctx))
	if s.o.Git != nil {
		errs = append(errs, s.gcRepos(ctx, before))
	}
	errs = append(errs, s.gcStorage(ctx, before))
	return errors.Join(errs...)
}

// drop deletes the row of oid in repo and releases its quota.
func (s *Server) drop(ctx context.Context, repoID uuid.UUID, oid string) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		row, err := q.DeleteLFSObject(ctx, dbgen.DeleteLFSObjectParams{RepoID: repoID, Oid: oid})
		if err != nil {
			return fmt.Errorf("delete lfs object: %w", err)
		}
		ok, err := q.NamespaceExists(ctx, row.ChargedTo)
		if err != nil {
			return fmt.Errorf("namespace exists: %w", err)
		}
		if !ok {
			return nil
		}
		return quota.Release(ctx, q, row.ChargedTo, quota.Delta{Bytes: row.Size})
	})
}

func (s *Server) gcOrphans(ctx context.Context) error {
	rows, err := s.q.ListOrphanLFSObjects(ctx, gcBatch)
	if err != nil {
		return fmt.Errorf("list orphan lfs objects: %w", err)
	}
	for _, r := range rows {
		if err := s.drop(ctx, r.RepoID, r.Oid); err != nil {
			return err
		}
	}
	if len(rows) > 0 {
		s.o.Log.InfoContext(ctx, "git lfs gc: rows of deleted repositories removed", "count", len(rows))
	}
	return nil
}

// gcCharges moves the usage of transferred repositories to the new owner.
func (s *Server) gcCharges(ctx context.Context) error {
	rows, err := s.q.ListMischargedLFSObjects(ctx, gcBatch)
	if err != nil {
		return fmt.Errorf("list mischarged lfs objects: %w", err)
	}
	for _, r := range rows {
		err := s.tx(ctx, func(q *dbgen.Queries) error {
			n, err := q.SetLFSObjectCharge(ctx, dbgen.SetLFSObjectChargeParams{RepoID: r.RepoID, Oid: r.Oid, OwnerID: r.OwnerID, ChargedTo: r.ChargedTo})
			if err != nil || n == 0 {
				return err
			}
			if ok, err := q.NamespaceExists(ctx, r.ChargedTo); err != nil {
				return fmt.Errorf("namespace exists: %w", err)
			} else if ok {
				if err := quota.Adjust(ctx, q, r.ChargedTo, quota.Delta{Bytes: -r.Size}); err != nil {
					return err
				}
			}
			return quota.Adjust(ctx, q, r.OwnerID, quota.Delta{Bytes: r.Size})
		})
		if err != nil {
			return fmt.Errorf("move lfs charge: %w", err)
		}
	}
	return nil
}

func (s *Server) gcRepos(ctx context.Context, before pgtype.Timestamptz) error {
	ids, err := s.q.ListLFSGCRepos(ctx, dbgen.ListLFSGCReposParams{Before: before, MaxRepos: gcRepos})
	if err != nil {
		return fmt.Errorf("list lfs gc repositories: %w", err)
	}
	var errs []error
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		if err := s.gcRepo(ctx, id, before.Time); err != nil {
			errs = append(errs, fmt.Errorf("lfs gc %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Server) gcRepo(ctx context.Context, repoID uuid.UUID, before time.Time) error {
	lease, err := s.o.Git.Acquire(ctx, repoID, gitstore.Read)
	if err != nil {
		if errors.Is(err, gitstore.ErrCold) {
			return nil
		}
		return fmt.Errorf("acquire: %w", err)
	}
	refs, err := scanPointers(ctx, lease.Path)
	lease.Release()
	if err != nil {
		return err
	}
	rows, err := s.q.ListRepoLFSObjects(ctx, repoID)
	if err != nil {
		return fmt.Errorf("list lfs objects: %w", err)
	}
	var seen []string
	removed := 0
	for _, r := range rows {
		if refs[r.Oid] {
			seen = append(seen, r.Oid)
			continue
		}
		if !r.SeenAt.Time.Before(before) || !r.CreatedAt.Time.Before(before) {
			continue
		}
		if err := s.drop(ctx, repoID, r.Oid); err != nil {
			return err
		}
		removed++
	}
	if len(seen) > 0 {
		if err := s.q.TouchLFSObjects(ctx, dbgen.TouchLFSObjectsParams{RepoID: repoID, Oids: seen}); err != nil {
			return fmt.Errorf("touch lfs objects: %w", err)
		}
	}
	if removed > 0 {
		s.o.Log.InfoContext(ctx, "git lfs gc: unreferenced objects removed", "repo", repoID, "count", removed)
	}
	return nil
}

// gcStorage deletes stored content that no repository references.
func (s *Server) gcStorage(ctx context.Context, before pgtype.Timestamptz) error {
	cl := s.o.Storage
	rows, err := s.q.ListUnreferencedLFSStorage(ctx, dbgen.ListUnreferencedLFSStorageParams{
		Target: cl.Primary().Name, Before: before, MaxRows: gcBatch})
	if err != nil {
		return fmt.Errorf("list unreferenced lfs storage: %w", err)
	}
	var freed int64
	n := 0
	for _, r := range rows {
		rel := strings.TrimPrefix(r.Key, cl.Name()+"/")
		oid := rel[strings.LastIndexByte(rel, '/')+1:]
		if !validOID(oid) || rel != objectKey(oid) {
			continue
		}
		// Re-check right before deleting: an upload may have just
		// referenced it again.
		if c, err := s.q.CountLFSObjectRefs(ctx, oid); err != nil || c > 0 {
			continue
		}
		if err := cl.Delete(ctx, rel); err != nil {
			s.o.Log.WarnContext(ctx, "git lfs gc: delete object", "key", r.Key, "error", err)
			continue
		}
		freed += r.Size
		n++
	}
	if n > 0 {
		s.o.Log.InfoContext(ctx, "git lfs gc: stored objects deleted", "count", n, "bytes", freed)
	}
	return nil
}

// scanPointers returns the oids of the LFS pointers among the blobs
// reachable from every ref and reflog of the repository.
func scanPointers(ctx context.Context, path string) (map[string]bool, error) {
	small, err := smallBlobs(ctx, path)
	if err != nil {
		return nil, err
	}
	refs := map[string]bool{}
	if len(small) == 0 {
		return refs, nil
	}
	var in bytes.Buffer
	for _, b := range small {
		in.WriteString(b)
		in.WriteByte('\n')
	}
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		err := git.Exec(ctx, git.Cmd{RepoPath: path, Args: []string{"cat-file", "--batch"}, Stdin: &in, Stdout: pw})
		_ = pw.CloseWithError(err)
		errc <- err
	}()
	br := bufio.NewReader(pr)
	var perr error
	for {
		hdr, err := br.ReadString('\n')
		if err == io.EOF && hdr == "" {
			break
		}
		if err != nil {
			perr = err
			break
		}
		f := strings.Fields(hdr)
		if len(f) != 3 {
			continue // "<oid> missing"
		}
		n, err := strconv.Atoi(f[2])
		if err != nil || n < 0 || n > maxPointerLen {
			perr = fmt.Errorf("unexpected cat-file header %q", hdr)
			break
		}
		buf := make([]byte, n+1)
		if _, err := io.ReadFull(br, buf); err != nil {
			perr = err
			break
		}
		if oid, ok := parsePointer(buf[:n]); ok {
			refs[oid] = true
		}
	}
	_, _ = io.Copy(io.Discard, pr)
	if err := errors.Join(<-errc, perr); err != nil {
		return nil, fmt.Errorf("read pointers: %w", err)
	}
	return refs, nil
}

// smallBlobs lists the reachable blobs small enough to be pointers.
func smallBlobs(ctx context.Context, path string) ([]string, error) {
	pr, pw := io.Pipe()
	revs, err := git.Start(ctx, git.Cmd{RepoPath: path, Args: []string{"rev-list", "--objects", "--all", "--reflog"}, Stdout: pw})
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}
	var revErr error
	done := make(chan struct{})
	go func() {
		revErr = revs.Wait()
		_ = pw.CloseWithError(revErr)
		close(done)
	}()
	cr, cw := io.Pipe()
	catErrc := make(chan error, 1)
	go func() {
		err := git.Exec(ctx, git.Cmd{RepoPath: path,
			Args:  []string{"cat-file", "--batch-check=%(objecttype) %(objectname) %(objectsize)"},
			Stdin: pr, Stdout: cw})
		_ = cw.CloseWithError(err)
		catErrc <- err
	}()
	var out []string
	sc := bufio.NewScanner(cr)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 || f[0] != "blob" {
			continue
		}
		if n, err := strconv.Atoi(f[2]); err == nil && n <= maxPointerLen {
			out = append(out, f[1])
		}
	}
	scanErr := sc.Err()
	_, _ = io.Copy(io.Discard, cr)
	catErr := <-catErrc
	_ = pr.Close()
	<-done
	if err := errors.Join(revErr, catErr, scanErr); err != nil {
		return nil, fmt.Errorf("scan objects: %w", err)
	}
	return out, nil
}

// parsePointer extracts the oid of an LFS pointer file.
func parsePointer(b []byte) (string, bool) {
	if !bytes.HasPrefix(b, []byte("version ")) {
		return "", false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "oid sha256:"); ok && validOID(v) {
			return v, true
		}
	}
	return "", false
}
