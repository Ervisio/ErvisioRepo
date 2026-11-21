package gitbrowse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

// Cold reads. A repository that is not on the local disk takes a while to
// hydrate (every pack is downloaded and indexed). The code pages do not
// wait for it: ref resolution, tree listings and READMEs are answered from
// the refs snapshot of the remote journal, the metadata cache
// (git_browse_*, keyed by object id, filled while the repository is hot)
// and, for repositories linked to GitHub, the GitHub git API addressed by
// object id. Hydration starts in the background at the same time. What
// none of them knows falls back to the hot path, which waits a little and
// then answers ErrBusy (HTTP 503 + Retry-After).

const (
	// coldTimeout bounds one cold answer (GitHub calls included).
	coldTimeout = 10 * time.Second
	// interactiveWait is how long an interactive read waits for a
	// hydration before answering ErrBusy.
	interactiveWait = 8 * time.Second
	// maxCachedReadme bounds the README contents kept in the database.
	maxCachedReadme = 256 << 10
	// browseCacheTTL: cache rows older than this are pruned.
	browseCacheTTL  = 30 * 24 * time.Hour
	pruneEvery      = 6 * time.Hour
	refsCacheMax    = 1024
	commitTypeEntry = "commit"
)

// errColdMiss: the cold sources do not know the answer.
var errColdMiss = errors.New("gitbrowse: not available without hydration")

// Hydrator starts the hydration of a cold repository in the background
// (satisfied by *gittier.Hydrator).
type Hydrator interface {
	Start(repoID uuid.UUID)
}

// RemoteEntry is one entry of a tree read from a remote (GitHub).
type RemoteEntry struct {
	Name string
	Mode string
	// Type is "blob", "tree" or "commit" (submodule).
	Type string
	OID  string
	Size int64
}

// Remote reads git objects of a repository from the forge it is linked to,
// by object id (so the content is exactly the local one). Methods return
// ErrRemoteMiss when the repository has no remote or the object is not
// there.
type Remote interface {
	CommitTree(ctx context.Context, repoID uuid.UUID, commit string) (tree string, err error)
	Tree(ctx context.Context, repoID uuid.UUID, oid string) (entries []RemoteEntry, truncated bool, err error)
	Blob(ctx context.Context, repoID uuid.UUID, oid string, limit int64) ([]byte, error)
}

// ErrRemoteMiss is returned by a Remote that cannot answer.
var ErrRemoteMiss = errors.New("gitbrowse: not available on the remote")

type interactiveKey struct{}

// Interactive marks ctx as a request of a person waiting for a page: a cold
// repository is answered from the cold sources, or ErrBusy after a short
// wait, instead of waiting for the whole hydration.
func Interactive(ctx context.Context) context.Context {
	return context.WithValue(ctx, interactiveKey{}, true)
}

func isInteractive(ctx context.Context) bool {
	v, _ := ctx.Value(interactiveKey{}).(bool)
	return v
}

// cachedEntry is the stored form of an Entry.
type cachedEntry struct {
	N string `json:"n"`
	M string `json:"m"`
	T string `json:"t"`
	O string `json:"o"`
	S int64  `json:"s"`
}

type refsEntry struct {
	seq  int64
	snap *gitstore.RefsSnapshot
}

// refsCache keeps the last refs snapshot of cold repositories, by
// manifest sequence (a push makes a new one).
type refsCache struct {
	mu sync.Mutex
	m  map[uuid.UUID]refsEntry
}

// written remembers the cache rows this process already stored, so a hot
// page view does not write them again (bounded, cleared when full).
type written struct {
	mu sync.Mutex
	m  map[string]struct{}
}

const writtenMax = 1 << 16

// first reports whether key was not stored yet, and marks it.
func (w *written) first(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.m[key]; ok {
		return false
	}
	if w.m == nil || len(w.m) >= writtenMax {
		w.m = make(map[string]struct{})
	}
	w.m[key] = struct{}{}
	return true
}

// coldState reports whether repo is not hot and cold reads apply. It also
// starts the hydration.
func (s *Service) coldState(ctx context.Context, repo Repo) (dbgen.GetRepoStorageRow, bool) {
	if s.q == nil || !isInteractive(ctx) {
		return dbgen.GetRepoStorageRow{}, false
	}
	row, err := s.q.GetRepoStorage(ctx, repo.ID)
	if err != nil || (row.StorageState != gitstore.StateCold && row.StorageState != gitstore.StateHydrating) {
		return row, false
	}
	if s.o.Hydrator != nil {
		s.o.Hydrator.Start(repo.ID)
	}
	return row, true
}

// coldRefs returns the refs snapshot of a cold repository.
func (s *Service) coldRefs(ctx context.Context, row dbgen.GetRepoStorageRow) (*gitstore.RefsSnapshot, error) {
	s.refs.mu.Lock()
	e, ok := s.refs.m[row.ID]
	s.refs.mu.Unlock()
	if ok && e.seq == row.ManifestSeq {
		return e.snap, nil
	}
	store := s.o.Tier.Store()
	if store == nil {
		return nil, errColdMiss
	}
	m, err := store.LoadManifest(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: manifest: %w", errColdMiss, err)
	}
	snap := &gitstore.RefsSnapshot{Refs: map[string]string{}}
	if m.RefsSeq > 0 {
		if snap, err = store.LoadRefs(ctx, row.ID, m.RefsSeq); err != nil {
			return nil, fmt.Errorf("%w: refs: %w", errColdMiss, err)
		}
	}
	s.refs.mu.Lock()
	if len(s.refs.m) >= refsCacheMax {
		clear(s.refs.m)
	}
	s.refs.m[row.ID] = refsEntry{seq: row.ManifestSeq, snap: snap}
	s.refs.mu.Unlock()
	return snap, nil
}

// coldCommit maps a commit, or an annotated tag, to its commit and root tree.
func (s *Service) coldCommit(ctx context.Context, repoID uuid.UUID, oid string) (commit, tree string, err error) {
	c, err := s.q.GetBrowseCommit(ctx, dbgen.GetBrowseCommitParams{RepoID: repoID, Oid: oid})
	if err == nil {
		return c.CommitSha, c.TreeOid, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("browse cache: %w", err)
	}
	if s.o.Remote == nil {
		return "", "", errColdMiss
	}
	tree, err = s.o.Remote.CommitTree(ctx, repoID, oid)
	if err != nil {
		return "", "", s.remoteErr(err)
	}
	s.rememberCommit(repoID, oid, oid, tree)
	return oid, tree, nil
}

func (s *Service) remoteErr(err error) error {
	if errors.Is(err, ErrRemoteMiss) {
		return errColdMiss
	}
	s.log.Warn("cold read from the remote failed", "error", err)
	return fmt.Errorf("%w: %w", errColdMiss, err)
}

// coldResolve resolves ref from the refs snapshot.
func (s *Service) coldResolve(ctx context.Context, row dbgen.GetRepoStorageRow, repo Repo, ref string) (Resolved, string, error) {
	if ref == "" {
		ref = repo.DefaultBranch
	}
	r := Resolved{Ref: ref}
	var names []string
	switch {
	case strings.HasPrefix(ref, "refs/"):
		names = []string{ref}
	case isHex(ref) && (len(ref) == 40 || len(ref) == 64):
		commit, tree, err := s.coldCommit(ctx, repo.ID, ref)
		if err != nil {
			return r, "", err
		}
		r.Kind, r.Commit = KindCommit, commit
		return r, tree, nil
	default:
		names = []string{"refs/heads/" + ref, "refs/tags/" + ref}
	}
	snap, err := s.coldRefs(ctx, row)
	if err != nil {
		return r, "", err
	}
	for _, n := range names {
		oid, ok := snap.Refs[n]
		if !ok {
			continue
		}
		commit, tree, err := s.coldCommit(ctx, repo.ID, oid)
		if err != nil {
			return r, "", err
		}
		r.Name, r.Commit = n, commit
		switch {
		case strings.HasPrefix(n, "refs/heads/"):
			r.Kind = KindBranch
		case strings.HasPrefix(n, "refs/tags/"):
			r.Kind = KindTag
		default:
			r.Kind = KindCommit
		}
		return r, tree, nil
	}
	if ref == repo.DefaultBranch && len(snap.Refs) == 0 {
		return r, "", ErrEmpty
	}
	if strings.HasPrefix(ref, "refs/") || !isHex(ref) {
		return r, "", notFoundf("ref %q", ref)
	}
	return r, "", errColdMiss // an abbreviated commit id: needs the objects
}

// coldTreeByOID lists the tree oid from the cache or the remote.
func (s *Service) coldTreeByOID(ctx context.Context, repoID uuid.UUID, oid, dir string) ([]Entry, bool, error) {
	row, err := s.q.GetBrowseTree(ctx, dbgen.GetBrowseTreeParams{RepoID: repoID, Oid: oid})
	if err == nil {
		var list []cachedEntry
		if err := json.Unmarshal(row.Entries, &list); err != nil {
			return nil, false, fmt.Errorf("%w: cached tree: %w", errColdMiss, err)
		}
		return s.entriesOf(list, dir), row.Truncated, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("browse cache: %w", err)
	}
	if s.o.Remote == nil {
		return nil, false, errColdMiss
	}
	rem, truncated, err := s.o.Remote.Tree(ctx, repoID, oid)
	if err != nil {
		return nil, false, s.remoteErr(err)
	}
	list := make([]cachedEntry, 0, len(rem))
	for _, e := range rem {
		list = append(list, cachedEntry{N: e.Name, M: e.Mode, T: remoteType(e), O: e.OID, S: e.Size})
	}
	entries := s.entriesOf(list, dir)
	if len(entries) > s.o.MaxTreeEntries {
		entries, truncated = entries[:s.o.MaxTreeEntries], true
	}
	s.rememberTree(repoID, oid, entries, truncated)
	return entries, truncated, nil
}

func remoteType(e RemoteEntry) string {
	switch {
	case e.Type == "tree":
		return TypeTree
	case e.Type == commitTypeEntry:
		return TypeSubmodule
	case e.Mode == "120000":
		return TypeSymlink
	}
	return TypeBlob
}

// entriesOf rebuilds the entries of a listing of dir, sorted like listTree.
func (s *Service) entriesOf(list []cachedEntry, dir string) []Entry {
	out := make([]Entry, 0, len(list))
	for _, c := range list {
		e := Entry{Name: c.N, Mode: c.M, Type: c.T, OID: c.O, Size: c.S, Path: c.N}
		if dir != "" {
			e.Path = dir + "/" + c.N
		}
		if e.Type != TypeBlob && e.Type != TypeSymlink {
			e.Size = -1
		}
		out = append(out, e)
	}
	sortEntries(out)
	return out
}

// coldTree lists dir of a commit with root tree root.
func (s *Service) coldTree(ctx context.Context, repoID uuid.UUID, root, dir string) ([]Entry, bool, error) {
	oid := root
	cur := ""
	if dir != "" {
		for _, part := range strings.Split(dir, "/") {
			entries, _, err := s.coldTreeByOID(ctx, repoID, oid, cur)
			if err != nil {
				return nil, false, err
			}
			i := slices.IndexFunc(entries, func(e Entry) bool { return e.Name == part })
			if i < 0 {
				return nil, false, notFoundf("path %q", dir)
			}
			if entries[i].Type != TypeTree {
				return nil, false, invalidf("%q is not a directory", dir)
			}
			oid = entries[i].OID
			cur = entries[i].Path
		}
	}
	return s.coldTreeByOID(ctx, repoID, oid, dir)
}

// coldBlob returns a small text blob from the cache or the remote.
func (s *Service) coldBlob(ctx context.Context, repoID uuid.UUID, r Resolved, root, p string) (Blob, error) {
	b := Blob{Ref: r, Path: p}
	dir, name := "", p
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		dir, name = p[:i], p[i+1:]
	}
	entries, _, err := s.coldTree(ctx, repoID, root, dir)
	if err != nil {
		return b, err
	}
	i := slices.IndexFunc(entries, func(e Entry) bool { return e.Name == name })
	if i < 0 {
		return b, notFoundf("path %q", p)
	}
	e := entries[i]
	if e.Type != TypeBlob {
		return b, errColdMiss // directories, symlinks and submodules: the hot path says what they are
	}
	b.OID, b.Mode, b.Size = e.OID, e.Mode, e.Size
	if e.Size > s.o.MaxBlobSize {
		return b, errColdMiss
	}
	data, err := s.q.GetBrowseBlob(ctx, dbgen.GetBrowseBlobParams{RepoID: repoID, Oid: e.OID})
	switch {
	case err == nil:
	case !errors.Is(err, pgx.ErrNoRows):
		return b, fmt.Errorf("browse cache: %w", err)
	case s.o.Remote == nil:
		return b, errColdMiss
	default:
		if data, err = s.o.Remote.Blob(ctx, repoID, e.OID, s.o.MaxBlobSize); err != nil {
			return b, s.remoteErr(err)
		}
		if int64(len(data)) != e.Size {
			return b, errColdMiss
		}
		if isReadme(name) {
			s.rememberBlob(repoID, e.OID, data)
		}
	}
	if ptr, ok := ParseLFSPointer(data); ok {
		b.LFS = ptr
	}
	b.Binary = IsBinary(data)
	if !b.Binary {
		b.Content = data
	}
	b.ContentType = ContentType(p, b.Binary)
	return b, nil
}

func isReadme(name string) bool { return slices.Contains(readmeNames, strings.ToLower(name)) }

// ---- filling the cache (best effort, in the background)

func (s *Service) background(fn func(ctx context.Context, q *dbgen.Queries) error) {
	if s.q == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := fn(ctx, s.q); err != nil {
			s.log.Debug("browse cache write", "error", err)
		}
	}()
}

func (s *Service) rememberCommit(repoID uuid.UUID, oid, commit, tree string) {
	if !isHex(oid) || !isHex(commit) || !isHex(tree) || !s.written.first("c"+repoID.String()+oid) {
		return
	}
	s.background(func(ctx context.Context, q *dbgen.Queries) error {
		return q.PutBrowseCommit(ctx, dbgen.PutBrowseCommitParams{RepoID: repoID, Oid: oid, CommitSha: commit, TreeOid: tree})
	})
}

func (s *Service) rememberTree(repoID uuid.UUID, oid string, entries []Entry, truncated bool) {
	if !isHex(oid) || !s.written.first("t"+repoID.String()+oid) {
		return
	}
	list := make([]cachedEntry, 0, len(entries))
	for _, e := range entries {
		list = append(list, cachedEntry{N: e.Name, M: e.Mode, T: e.Type, O: e.OID, S: e.Size})
	}
	b, err := json.Marshal(list)
	if err != nil {
		return
	}
	s.background(func(ctx context.Context, q *dbgen.Queries) error {
		return q.PutBrowseTree(ctx, dbgen.PutBrowseTreeParams{RepoID: repoID, Oid: oid, Entries: b, Truncated: truncated})
	})
}

func (s *Service) rememberBlob(repoID uuid.UUID, oid string, data []byte) {
	if !isHex(oid) || len(data) > maxCachedReadme || !s.written.first("b"+repoID.String()+oid) {
		return
	}
	c := slices.Clone(data)
	s.background(func(ctx context.Context, q *dbgen.Queries) error {
		return q.PutBrowseBlob(ctx, dbgen.PutBrowseBlobParams{RepoID: repoID, Oid: oid, Content: c})
	})
}

// pruneLoop deletes old cache rows until stop is closed.
func (s *Service) pruneLoop(stop <-chan struct{}) {
	t := time.NewTicker(pruneEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		before := pgtype.Timestamptz{Time: time.Now().Add(-browseCacheTTL), Valid: true}
		var n int64
		for _, f := range []func(context.Context, pgtype.Timestamptz) (int64, error){
			s.q.PruneBrowseTrees, s.q.PruneBrowseCommits, s.q.PruneBrowseBlobs,
		} {
			k, err := f(ctx, before)
			if err != nil {
				s.log.Warn("prune browse cache", "error", err)
			}
			n += k
		}
		cancel()
		if n > 0 {
			s.log.Info("pruned browse cache", "rows", n)
		}
	}
}

// ---- cold variants of the public reads

// coldTreeRead answers Tree for a cold repository; ok is false when the
// hot path must answer.
func (s *Service) coldTreeRead(ctx context.Context, repo Repo, ref, path string) (Tree, bool, error) {
	row, cold := s.coldState(ctx, repo)
	if !cold {
		return Tree{}, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, coldTimeout)
	defer cancel()
	t := Tree{Path: path}
	r, root, err := s.coldResolve(ctx, row, repo, ref)
	if err == nil {
		t.Ref = r
		t.Entries, t.Truncated, err = s.coldTree(ctx, repo.ID, root, path)
		t.Readme = readmeOf(t.Entries)
	}
	if errors.Is(err, errColdMiss) {
		return Tree{}, false, nil
	}
	return t, true, err
}

func (s *Service) coldBlobRead(ctx context.Context, repo Repo, ref, path string, readme bool) (Blob, bool, error) {
	row, cold := s.coldState(ctx, repo)
	if !cold {
		return Blob{}, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, coldTimeout)
	defer cancel()
	r, root, err := s.coldResolve(ctx, row, repo, ref)
	var b Blob
	if err == nil {
		p := path
		if readme {
			var entries []Entry
			if entries, _, err = s.coldTree(ctx, repo.ID, root, path); err == nil {
				if p = readmeOf(entries); p == "" {
					err = notFoundf("no README in %q", path)
				}
			}
		}
		if err == nil {
			b, err = s.coldBlob(ctx, repo.ID, r, root, p)
		}
	}
	if errors.Is(err, errColdMiss) {
		return Blob{}, false, nil
	}
	return b, true, err
}

func (s *Service) coldResolveRead(ctx context.Context, repo Repo, ref string) (Resolved, bool, error) {
	row, cold := s.coldState(ctx, repo)
	if !cold {
		return Resolved{}, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, coldTimeout)
	defer cancel()
	r, _, err := s.coldResolve(ctx, row, repo, ref)
	if errors.Is(err, errColdMiss) {
		return Resolved{}, false, nil
	}
	return r, true, err
}
