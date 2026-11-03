package lfs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/githttp"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

const (
	defaultLockLimit = 100
	maxLockLimit     = 1000
	maxLockPath      = 4096
)

var (
	errLockExists = errors.New("path is already locked")
	errBadLock    = errors.New("invalid lock request")
)

// Lock is a file lock.
type Lock struct {
	ID        uuid.UUID
	Path      string
	Refname   string
	OwnerID   uuid.UUID
	OwnerName string
	LockedAt  time.Time
}

func lockFrom(id uuid.UUID, path, ref string, owner uuid.UUID, name string, at pgtype.Timestamptz) Lock {
	return Lock{ID: id, Path: path, Refname: ref, OwnerID: owner, OwnerName: name, LockedAt: at.Time.UTC()}
}

// lockFilter selects locks; zero fields do not filter.
type lockFilter struct {
	Path     string
	ID       string
	Cursor   string
	Limit    int
	Owner    uuid.UUID // only locks of this user
	NotOwner uuid.UUID // only locks of other users
}

// createLock locks path for the caller. When the path is already locked it
// returns the existing lock and errLockExists.
func (s *Server) createLock(ctx context.Context, repo repos.Repo, who auth.Principal, path, ref string) (Lock, error) {
	if path == "" || len(path) > maxLockPath || strings.ContainsRune(path, 0) {
		return Lock{}, fmt.Errorf("%w: invalid path", errBadLock)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Lock{}, fmt.Errorf("lock id: %w", err)
	}
	row, err := s.q.CreateLFSLock(ctx, dbgen.CreateLFSLockParams{ID: id, RepoID: repo.ID, Path: path, Refname: ref, OwnerID: who.UserID})
	if err == nil {
		return lockFrom(row.ID, row.Path, row.Refname, row.OwnerID, who.Username, row.CreatedAt), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Lock{}, fmt.Errorf("create lock: %w", err)
	}
	ex, err := s.q.GetLFSLockByPath(ctx, dbgen.GetLFSLockByPathParams{RepoID: repo.ID, Path: path})
	if err != nil {
		return Lock{}, fmt.Errorf("get existing lock: %w", err)
	}
	return lockFrom(ex.ID, ex.Path, ex.Refname, ex.OwnerID, ex.OwnerName, ex.CreatedAt), errLockExists
}

// listLocks returns a page of locks and the cursor of the next page.
func (s *Server) listLocks(ctx context.Context, repo repos.Repo, f lockFilter) ([]Lock, string, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLockLimit
	}
	limit = min(limit, maxLockLimit)
	p := dbgen.ListLFSLocksParams{RepoID: repo.ID, Path: f.Path, MaxRows: int32(limit + 1)}
	if f.ID != "" {
		id, err := uuid.Parse(f.ID)
		if err != nil {
			return nil, "", nil
		}
		p.LockID = uuid.NullUUID{UUID: id, Valid: true}
	}
	if f.Owner != uuid.Nil {
		p.OwnerID = uuid.NullUUID{UUID: f.Owner, Valid: true}
	}
	if f.NotOwner != uuid.Nil {
		p.NotOwnerID = uuid.NullUUID{UUID: f.NotOwner, Valid: true}
	}
	if f.Cursor != "" {
		ts, id, ok := parseCursor(f.Cursor)
		if !ok {
			return nil, "", fmt.Errorf("%w: invalid cursor", errBadLock)
		}
		p.AfterCreated = pgtype.Timestamptz{Time: ts, Valid: true}
		p.AfterID = uuid.NullUUID{UUID: id, Valid: true}
	}
	rows, err := s.q.ListLFSLocks(ctx, p)
	if err != nil {
		return nil, "", fmt.Errorf("list locks: %w", err)
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = strconv.FormatInt(last.CreatedAt.Time.UnixMicro(), 10) + "." + last.ID.String()
	}
	out := make([]Lock, len(rows))
	for i, r := range rows {
		out[i] = lockFrom(r.ID, r.Path, r.Refname, r.OwnerID, r.OwnerName, r.CreatedAt)
	}
	return out, next, nil
}

func parseCursor(c string) (time.Time, uuid.UUID, bool) {
	a, b, ok := strings.Cut(c, ".")
	if !ok {
		return time.Time{}, uuid.Nil, false
	}
	us, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	id, err := uuid.Parse(b)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	return time.UnixMicro(us), id, true
}

// unlock removes a lock. Other users' locks need force and repository admin
// (or instance admin) rights.
func (s *Server) unlock(ctx context.Context, repo repos.Repo, who auth.Principal, id string, force bool) (Lock, error) {
	lid, err := uuid.Parse(id)
	if err != nil {
		return Lock{}, errNotFound
	}
	row, err := s.q.GetLFSLock(ctx, dbgen.GetLFSLockParams{RepoID: repo.ID, ID: lid})
	if errors.Is(err, pgx.ErrNoRows) {
		return Lock{}, errNotFound
	}
	if err != nil {
		return Lock{}, fmt.Errorf("get lock: %w", err)
	}
	l := lockFrom(row.ID, row.Path, row.Refname, row.OwnerID, row.OwnerName, row.CreatedAt)
	if row.OwnerID != who.UserID {
		if !force {
			return l, fmt.Errorf("%w: the lock belongs to %s; use --force", errForbidden, row.OwnerName)
		}
		if !canAdmin(repo, who) {
			return l, fmt.Errorf("%w: only repository admins can break other users' locks", errForbidden)
		}
	}
	if _, err := s.q.DeleteLFSLock(ctx, dbgen.DeleteLFSLockParams{RepoID: repo.ID, ID: lid}); err != nil {
		return l, fmt.Errorf("delete lock: %w", err)
	}
	if row.OwnerID != who.UserID {
		s.o.Log.InfoContext(ctx, "git lfs: lock broken", "repo", repo.ID, "lock", lid, "by", who.UserID, "owner", row.OwnerID)
	}
	return l, nil
}

// HTTP Locks API.

type lockOwner struct {
	Name string `json:"name"`
}

type lockJSON struct {
	ID       string    `json:"id"`
	Path     string    `json:"path"`
	LockedAt string    `json:"locked_at"`
	Owner    lockOwner `json:"owner"`
}

func toJSON(l Lock) lockJSON {
	return lockJSON{ID: l.ID.String(), Path: l.Path, LockedAt: l.LockedAt.Format(time.RFC3339), Owner: lockOwner{Name: l.OwnerName}}
}

func toJSONs(ls []Lock) []lockJSON {
	out := make([]lockJSON, len(ls))
	for i, l := range ls {
		out[i] = toJSON(l)
	}
	return out
}

func (s *Server) lockFail(w http.ResponseWriter, r *http.Request, err error, what string) {
	if errors.Is(err, errBadLock) {
		githttp.LFSError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.fail(w, r, err, what)
}

func (s *Server) createLockHTTP(w http.ResponseWriter, r *http.Request, req githttp.LFSRequest) {
	if !canWrite(req.Repo, req.Caller) {
		deny(w, req)
		return
	}
	var in struct {
		Path string    `json:"path"`
		Ref  *batchRef `json:"ref"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ref := ""
	if in.Ref != nil {
		ref = in.Ref.Name
	}
	l, err := s.createLock(r.Context(), req.Repo, req.Caller, in.Path, ref)
	if errors.Is(err, errLockExists) {
		writeJSON(w, http.StatusConflict, struct {
			Lock    lockJSON `json:"lock"`
			Message string   `json:"message"`
		}{toJSON(l), "already created lock"})
		return
	}
	if err != nil {
		s.lockFail(w, r, err, "create lock")
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Lock lockJSON `json:"lock"`
	}{toJSON(l)})
}

func (s *Server) listLocksHTTP(w http.ResponseWriter, r *http.Request, req githttp.LFSRequest) {
	if !canRead(req.Repo, req.Caller) {
		deny(w, req)
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	locks, next, err := s.listLocks(r.Context(), req.Repo, lockFilter{Path: q.Get("path"), ID: q.Get("id"), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		s.lockFail(w, r, err, "list locks")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Locks      []lockJSON `json:"locks"`
		NextCursor string     `json:"next_cursor,omitempty"`
	}{toJSONs(locks), next})
}

func (s *Server) verifyLocksHTTP(w http.ResponseWriter, r *http.Request, req githttp.LFSRequest) {
	if !canWrite(req.Repo, req.Caller) {
		deny(w, req)
		return
	}
	var in struct {
		Cursor string `json:"cursor"`
		Limit  int    `json:"limit"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	// Pages are taken over all locks so that one cursor covers both lists.
	locks, next, err := s.listLocks(r.Context(), req.Repo, lockFilter{Cursor: in.Cursor, Limit: in.Limit})
	if err != nil {
		s.lockFail(w, r, err, "verify locks")
		return
	}
	ours, theirs := []lockJSON{}, []lockJSON{}
	for _, l := range locks {
		if l.OwnerID == req.Caller.UserID {
			ours = append(ours, toJSON(l))
		} else {
			theirs = append(theirs, toJSON(l))
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Ours       []lockJSON `json:"ours"`
		Theirs     []lockJSON `json:"theirs"`
		NextCursor string     `json:"next_cursor,omitempty"`
	}{ours, theirs, next})
}

func (s *Server) unlockHTTP(w http.ResponseWriter, r *http.Request, req githttp.LFSRequest, id string) {
	if !canWrite(req.Repo, req.Caller) {
		deny(w, req)
		return
	}
	var in struct {
		Force bool `json:"force"`
	}
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	l, err := s.unlock(r.Context(), req.Repo, req.Caller, id, in.Force)
	if err != nil {
		s.lockFail(w, r, err, "unlock")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Lock lockJSON `json:"lock"`
	}{toJSON(l)})
}
