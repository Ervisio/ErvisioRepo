package gitbrowse

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// Entry types.
const (
	TypeBlob      = "blob"
	TypeTree      = "tree"
	TypeSymlink   = "symlink"
	TypeSubmodule = "submodule"
)

// Entry is one item of a directory.
type Entry struct {
	Name string
	Path string
	Type string // TypeBlob, TypeTree, TypeSymlink or TypeSubmodule
	Mode string
	OID  string
	Size int64 // blobs and symlinks only; -1 otherwise
	// LastCommit is the newest commit that touched the entry; nil when the
	// history walk ran out of budget.
	LastCommit *CommitSummary
}

// CommitSummary is the short form of a commit.
type CommitSummary struct {
	SHA     string
	Summary string
	Author  string
	Date    time.Time
}

// Tree is a directory listing.
type Tree struct {
	Ref       Resolved
	Path      string
	Entries   []Entry
	Truncated bool   // more than MaxTreeEntries entries
	Readme    string // path of the README of the directory, if any
}

// Tree lists the directory at path (empty = root) of ref, directories
// first, with the last commit of each entry when lastCommits is set.
func (s *Service) Tree(ctx context.Context, repo Repo, ref, path string, lastCommits bool) (Tree, error) {
	if err := git.ValidateTreePath(path); err != nil {
		return Tree{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	t := Tree{Path: path}
	err := s.read(ctx, repo, func(rp string) error {
		r, err := s.resolve(ctx, rp, repo, ref)
		if err != nil {
			return err
		}
		t.Ref = r
		entries, truncated, err := s.listTree(ctx, rp, r.Commit, path)
		if err != nil {
			return err
		}
		t.Entries, t.Truncated = entries, truncated
		t.Readme = readmeOf(entries)
		if lastCommits && len(entries) > 0 {
			s.fillLastCommits(ctx, rp, repo, r.Commit, path, t.Entries)
		}
		return nil
	})
	return t, err
}

// object returns the type of commit:path, or ErrNotFound.
func (s *Service) object(ctx context.Context, rp, commit, path string) (git.ObjectInfo, error) {
	name := commit + "^{tree}"
	if path != "" {
		name = commit + ":" + path
	}
	info, err := s.cat.Info(ctx, rp, name)
	if errors.Is(err, git.ErrObjectNotFound) {
		return info, notFoundf("path %q", path)
	}
	if err != nil {
		return info, fmt.Errorf("read object: %w", err)
	}
	return info, nil
}

func (s *Service) listTree(ctx context.Context, rp, commit, path string) ([]Entry, bool, error) {
	info, err := s.object(ctx, rp, commit, path)
	if err != nil {
		return nil, false, err
	}
	if info.Type != "tree" {
		return nil, false, invalidf("%q is not a directory", path)
	}
	out, err := run(ctx, rp, readTimeout, "ls-tree", "-z", "--long", info.OID)
	if err != nil {
		return nil, false, fmt.Errorf("list tree: %w", err)
	}
	var entries []Entry
	truncated := false
	for _, rec := range strings.Split(string(out), "\x00") {
		meta, name, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 4 {
			continue
		}
		if len(entries) >= s.o.MaxTreeEntries {
			truncated = true
			break
		}
		e := Entry{Name: name, Mode: f[0], OID: f[2], Size: -1}
		if path != "" {
			e.Path = path + "/" + name
		} else {
			e.Path = name
		}
		switch {
		case f[1] == "tree":
			e.Type = TypeTree
		case f[1] == "commit":
			e.Type = TypeSubmodule
		case f[0] == "120000":
			e.Type = TypeSymlink
		default:
			e.Type = TypeBlob
		}
		if n, err := strconv.ParseInt(f[3], 10, 64); err == nil {
			e.Size = n
		}
		entries = append(entries, e)
	}
	slices.SortStableFunc(entries, func(a, b Entry) int {
		if (a.Type == TypeTree) != (b.Type == TypeTree) {
			if a.Type == TypeTree {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return entries, truncated, nil
}

// fillLastCommits walks the history of dir from commit and sets the newest
// commit that touched each entry, within a time and commit budget. Results
// are cached by (repository, commit, directory): commits are immutable.
func (s *Service) fillLastCommits(ctx context.Context, rp string, repo Repo, commit, dir string, entries []Entry) {
	key := lastCommitKey{repo: repo.ID, commit: commit, dir: dir}
	found, ok := s.cache.get(key)
	if !ok {
		var complete bool
		found, complete = s.walkLastCommits(ctx, rp, commit, dir, entries)
		if complete {
			s.cache.put(key, found)
		}
	}
	for i := range entries {
		if c, ok := found[entries[i].Name]; ok {
			entries[i].LastCommit = &c
		}
	}
}

func (s *Service) walkLastCommits(ctx context.Context, rp, commit, dir string, entries []Entry) (map[string]CommitSummary, bool) {
	want := make(map[string]bool, len(entries))
	for i, e := range entries {
		if i >= lastCommitEntries {
			break
		}
		want[e.Name] = true
	}
	found := make(map[string]CommitSummary, len(want))
	ctx, cancel := context.WithTimeout(ctx, lastCommitBudget)
	defer cancel()
	args := []string{"log", "-z", "--name-only", "--no-renames", "--format=format:\x01%H\x02%at\x02%an\x02%s",
		"--max-count=" + strconv.Itoa(lastCommitCommits), commit, "--"}
	if dir != "" {
		args = append(args, dir)
	}
	st, err := startStream(ctx, rp, lastCommitBudget, args...)
	if err != nil {
		s.log.WarnContext(ctx, "last commits: start git log", "error", err)
		return found, false
	}
	defer func() { _ = st.Close() }()
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	sc := bufio.NewScanner(st)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	sc.Split(splitNUL)
	var cur CommitSummary
	for sc.Scan() {
		tok := strings.TrimLeft(sc.Text(), "\n")
		if strings.HasPrefix(tok, "\x01") {
			hdr, file, _ := strings.Cut(tok[1:], "\n")
			f := strings.SplitN(hdr, "\x02", 4)
			if len(f) != 4 {
				continue
			}
			cur = CommitSummary{SHA: f[0], Date: unixTime(f[1]), Author: f[2], Summary: f[3]}
			tok = file
		}
		if tok == "" || cur.SHA == "" {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(tok, prefix), "/")
		if want[name] {
			if _, done := found[name]; !done {
				found[name] = cur
				if len(found) == len(want) {
					return found, true
				}
			}
		}
	}
	// The whole history was read without error: every entry that was not
	// found has no reachable change (it cannot happen for existing paths).
	return found, sc.Err() == nil && ctx.Err() == nil
}

// splitNUL is a bufio.SplitFunc for NUL-terminated records.
func splitNUL(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == 0 {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

type lastCommitKey struct {
	repo   uuid.UUID
	commit string
	dir    string
}

// lastCommitCache is a small bounded cache of tree listings' last commits.
type lastCommitCache struct {
	mu    sync.Mutex
	max   int
	items map[lastCommitKey]map[string]CommitSummary
	order []lastCommitKey
}

func newLastCommitCache(n int) *lastCommitCache {
	return &lastCommitCache{max: n, items: make(map[lastCommitKey]map[string]CommitSummary, n)}
}

func (c *lastCommitCache) get(k lastCommitKey) (map[string]CommitSummary, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[k]
	return v, ok
}

func (c *lastCommitCache) put(k lastCommitKey, v map[string]CommitSummary) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[k]; ok {
		return
	}
	if len(c.order) >= c.max {
		delete(c.items, c.order[0])
		c.order = c.order[1:]
	}
	c.items[k] = v
	c.order = append(c.order, k)
}

// readmeNames are the README file names, by preference.
var readmeNames = []string{"readme.md", "readme.markdown", "readme.mdown", "readme.rst", "readme.adoc", "readme.txt", "readme"}

// readmeOf returns the path of the README among entries, or "".
func readmeOf(entries []Entry) string {
	best, rank := "", len(readmeNames)
	for _, e := range entries {
		if e.Type != TypeBlob {
			continue
		}
		if i := slices.Index(readmeNames, strings.ToLower(e.Name)); i >= 0 && i < rank {
			best, rank = e.Path, i
		}
	}
	return best
}
