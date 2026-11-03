package ghsync

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

// Kinds of the github_import_map rows (GitHub id -> local id), which make
// the importers idempotent.
const (
	MapLabel         = "label"
	MapMilestone     = "milestone"
	MapIssue         = "issue" // GitHub issue ids
	MapPull          = "pull"  // GitHub pull request ids (their issue rows)
	MapComment       = "comment"
	MapReview        = "review"
	MapReviewComment = "review_comment"
	MapRelease       = "release"
	MapAsset         = "asset"
)

// GitHubUser is the user object of the GitHub API.
type GitHubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// Repo is the API path of the GitHub repository ("/repos/o/n").
func (ic ImportContext) Repo() string {
	return "/repos/" + url.PathEscape(ic.RemoteOwner) + "/" + url.PathEscape(ic.RemoteName)
}

// Includes reports whether the import covers the kind of data (one of
// ImportKinds), so an importer can take over what another one would do.
func (ic ImportContext) Includes(kind string) bool {
	if ic.st == nil || len(ic.st.imp.Include) == 0 {
		return true
	}
	return slices.Contains(ic.st.imp.Include, kind)
}

// Mapped returns the local id of the GitHub object id of kind (Map*)
// imported into the repository; ok is false when it was not imported yet.
func (ic ImportContext) Mapped(ctx context.Context, q *dbgen.Queries, kind string, id int64) (local uuid.UUID, ok bool, err error) {
	local, err = q.GetGitHubImportMap(ctx, dbgen.GetGitHubImportMapParams{RepoID: ic.RepoID, Kind: kind, GithubID: id})
	switch {
	case noRows(err):
		return uuid.Nil, false, nil
	case err != nil:
		return uuid.Nil, false, fmt.Errorf("get import map: %w", err)
	}
	return local, true, nil
}

// Remember records that the GitHub object id of kind was imported as
// local; it belongs in the transaction that creates local, which makes
// the import of the object happen once across retries.
func (ic ImportContext) Remember(ctx context.Context, q *dbgen.Queries, kind string, id int64, local uuid.UUID) error {
	if err := q.InsertGitHubImportMap(ctx, dbgen.InsertGitHubImportMapParams{
		RepoID: ic.RepoID, Kind: kind, GithubID: id, LocalID: local}); err != nil {
		return fmt.Errorf("record import map: %w", err)
	}
	return nil
}

// Time converts a GitHub timestamp; the zero time is NULL.
func Time(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}

// Stamp converts a required GitHub timestamp; the zero time (absent from
// the response) becomes the current time.
func Stamp(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		t = time.Now()
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// TimePtr converts an optional GitHub timestamp.
func TimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return Time(*t)
}

// importState is shared by the importers of one import job.
type importState struct {
	s       *Service
	imp     dbgen.GithubImport
	issuers []string

	mu    sync.Mutex
	users map[int64]uuid.NullUUID
}

// issuersOf returns the issuers an external identity of the GitHub server
// at base can be recorded with.
func issuersOf(base string) []string {
	api := APIURL(base)
	return []string{base, base + "/", api, api + "/", base + "/login/oauth"}
}

// User returns the local user linked to the GitHub account u (an external
// identity of provider "github", or whose issuer is this GitHub server,
// with the numeric id of u as subject); not valid when there is none. It
// never matches on e-mail addresses.
func (ic ImportContext) User(ctx context.Context, u *GitHubUser) (uuid.NullUUID, error) {
	if u == nil || u.ID <= 0 || ic.st == nil {
		return uuid.NullUUID{}, nil
	}
	st := ic.st
	st.mu.Lock()
	id, ok := st.users[u.ID]
	st.mu.Unlock()
	if ok {
		return id, nil
	}
	uid, err := st.s.q.FindLinkedGitHubUser(ctx, dbgen.FindLinkedGitHubUserParams{
		Subject: strconv.FormatInt(u.ID, 10), Issuers: st.issuers})
	switch {
	case noRows(err):
	case err != nil:
		return uuid.NullUUID{}, fmt.Errorf("find linked user: %w", err)
	default:
		id = uuid.NullUUID{UUID: uid, Valid: true}
	}
	st.mu.Lock()
	st.users[u.ID] = id
	st.mu.Unlock()
	return id, nil
}

// Author returns the local author of something u wrote on GitHub: the
// linked user (see User) with body unchanged, or else the user who
// started the import with body prefixed by an attribution line naming u.
// The result is clipped to limit characters.
func (ic ImportContext) Author(ctx context.Context, u *GitHubUser, body string, limit int) (uuid.NullUUID, string, error) {
	id, err := ic.User(ctx, u)
	if err != nil {
		return uuid.NullUUID{}, "", err
	}
	if id.Valid {
		return id, Clip(body, limit), nil
	}
	return nullUser(ic.CreatedBy), Clip(Attribution(u)+body, limit), nil
}

// Attribution is the line that credits the GitHub author u of imported
// content without a local account.
func Attribution(u *GitHubUser) string {
	login := "ghost"
	if u != nil && u.Login != "" {
		login = u.Login
	}
	return "_Originally by @" + login + " on GitHub._\n\n"
}

// Clip cuts s to at most limit characters (not bytes) without splitting
// a UTF-8 sequence; invalid sequences and NUL bytes (refused by
// PostgreSQL) are replaced.
func Clip(s string, limit int) string {
	s = strings.ReplaceAll(strings.ToValidUTF8(s, string(utf8.RuneError)), string(rune(0)), string(utf8.RuneError))
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	n := 0
	for i := range s {
		if n == limit {
			return s[:i]
		}
		n++
	}
	return s
}

// FetchPullHeads fetches refs/pull/<n>/head of every pull request of the
// GitHub repository into the local refs/pull/<n>/head (the head commits of
// closed pull requests whose branch is gone, and of fork pull requests,
// exist only there) and returns their commits by number. The write goes
// through a hook session (journal) with ImportProtocol, so it publishes
// no push.
func (ic ImportContext) FetchPullHeads(ctx context.Context) (map[int64]string, error) {
	if ic.st == nil {
		return nil, fmt.Errorf("fetch pull request heads: no import")
	}
	return ic.st.s.fetchPullHeads(ctx, ic.st.imp)
}

func (s *Service) fetchPullHeads(ctx context.Context, imp dbgen.GithubImport) (map[int64]string, error) {
	r, err := s.open(ctx, imp.ConnectionID.UUID, imp.RemoteOwner, imp.RemoteName)
	if err != nil {
		return nil, err
	}
	l, err := s.o.Tier.Acquire(ctx, imp.RepoID, gitstore.Write)
	if err != nil {
		return nil, fmt.Errorf("acquire repository: %w", err)
	}
	defer l.Release()
	if err := git.Exec(ctx, git.Cmd{RepoPath: l.Path, Env: r.env, Timeout: fetchTimeout, Args: []string{
		"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules",
		r.url, "+" + pullsPrefix + "*/head:" + scratchPulls + "*"}}); err != nil {
		return nil, fmt.Errorf("fetch pull request heads from GitHub: %w", err)
	}
	scratch, err := listRefs(ctx, l.Path, scratchPulls)
	if err != nil {
		return nil, err
	}
	local, err := listRefs(ctx, l.Path, pullsPrefix)
	if err != nil {
		return nil, err
	}
	heads := make(map[int64]string, len(scratch))
	var edits []edit
	for _, ref := range sortedKeys(scratch) {
		n, err := strconv.ParseInt(strings.TrimPrefix(ref, scratchPulls), 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		sha, target := scratch[ref], pullsPrefix+strconv.FormatInt(n, 10)+"/head"
		heads[n] = sha
		if local[target] != sha {
			edits = append(edits, edit{ref: target, newSHA: sha, oldSHA: local[target], verify: true})
		}
	}
	drop := cleanup(scratch).edits
	if len(edits) == 0 {
		return heads, updateRefsRaw(ctx, l.Path, drop)
	}
	if err := s.updateRefsAs(ctx, l, append(edits, drop...), ImportProtocol); err != nil {
		return nil, err
	}
	return heads, nil
}
