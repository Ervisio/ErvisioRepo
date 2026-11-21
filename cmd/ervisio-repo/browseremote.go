package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/ghsync"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
)

// githubObjects reads git objects of repositories linked to (or imported
// from) GitHub, by object id, for the cold reads of the code pages. An
// object id names its content, so what GitHub returns for it is exactly
// what the local repository holds; the caller has already checked the
// local permission.
type githubObjects struct {
	gh *ghsync.Service
	mu sync.Mutex
	// remotes caches the client of a repository (or its absence) briefly.
	remotes map[uuid.UUID]remoteEntry
}

type remoteEntry struct {
	r   *ghsync.Remote
	exp time.Time
}

const remoteCacheTTL = 5 * time.Minute

func newGitHubObjects(gh *ghsync.Service) *githubObjects {
	return &githubObjects{gh: gh, remotes: map[uuid.UUID]remoteEntry{}}
}

func (g *githubObjects) remote(ctx context.Context, repoID uuid.UUID) (*ghsync.Remote, error) {
	g.mu.Lock()
	e, ok := g.remotes[repoID]
	g.mu.Unlock()
	if ok && time.Now().Before(e.exp) {
		if e.r == nil {
			return nil, gitbrowse.ErrRemoteMiss
		}
		return e.r, nil
	}
	r, err := g.gh.RemoteOf(ctx, repoID)
	var rp *ghsync.Remote
	switch {
	case errors.Is(err, ghsync.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		rp = &r
	}
	g.mu.Lock()
	if len(g.remotes) > 4096 {
		clear(g.remotes)
	}
	g.remotes[repoID] = remoteEntry{r: rp, exp: time.Now().Add(remoteCacheTTL)}
	g.mu.Unlock()
	if rp == nil {
		return nil, gitbrowse.ErrRemoteMiss
	}
	return rp, nil
}

func validOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}

// get calls the GitHub API; a missing object is ErrRemoteMiss.
func (g *githubObjects) get(ctx context.Context, repoID uuid.UUID, kind, oid string, out any) error {
	if !validOID(oid) {
		return gitbrowse.ErrRemoteMiss
	}
	r, err := g.remote(ctx, repoID)
	if err != nil {
		return err
	}
	err = r.API.Do(ctx, http.MethodGet, r.Repo()+"/git/"+kind+"/"+oid, nil, out)
	var ae *ghsync.APIError
	if errors.As(err, &ae) && (ae.Status == http.StatusNotFound || ae.Status == http.StatusUnprocessableEntity ||
		ae.Status == http.StatusConflict) {
		return gitbrowse.ErrRemoteMiss
	}
	if err != nil {
		return fmt.Errorf("github %s %s: %w", kind, oid, err)
	}
	return nil
}

func (g *githubObjects) CommitTree(ctx context.Context, repoID uuid.UUID, commit string) (string, error) {
	var c struct {
		SHA  string `json:"sha"`
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := g.get(ctx, repoID, "commits", commit, &c); err != nil {
		return "", err
	}
	if c.SHA != commit || !validOID(c.Tree.SHA) {
		return "", gitbrowse.ErrRemoteMiss
	}
	return c.Tree.SHA, nil
}

func (g *githubObjects) Tree(ctx context.Context, repoID uuid.UUID, oid string) ([]gitbrowse.RemoteEntry, bool, error) {
	var t struct {
		SHA  string `json:"sha"`
		Tree []struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := g.get(ctx, repoID, "trees", oid, &t); err != nil {
		return nil, false, err
	}
	if t.SHA != oid {
		return nil, false, gitbrowse.ErrRemoteMiss
	}
	out := make([]gitbrowse.RemoteEntry, 0, len(t.Tree))
	for _, e := range t.Tree {
		if e.Path == "" || strings.Contains(e.Path, "/") || !validOID(e.SHA) {
			continue
		}
		size := e.Size
		if e.Type != "blob" {
			size = -1
		}
		out = append(out, gitbrowse.RemoteEntry{Name: e.Path, Mode: e.Mode, Type: e.Type, OID: e.SHA, Size: size})
	}
	return out, t.Truncated, nil
}

func (g *githubObjects) Blob(ctx context.Context, repoID uuid.UUID, oid string, limit int64) ([]byte, error) {
	var b struct {
		SHA      string `json:"sha"`
		Size     int64  `json:"size"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := g.get(ctx, repoID, "blobs", oid, &b); err != nil {
		return nil, err
	}
	if b.SHA != oid || b.Size > limit || b.Encoding != "base64" {
		return nil, gitbrowse.ErrRemoteMiss
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(b.Content, "\n", ""))
	if err != nil || int64(len(data)) != b.Size {
		return nil, gitbrowse.ErrRemoteMiss
	}
	return data, nil
}
