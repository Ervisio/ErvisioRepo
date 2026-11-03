package ghsync

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

var (
	remoteOwnerRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
	remoteNameRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

const (
	maxTrustPatterns = 50
	maxIntervalMins  = 7 * 24 * 60
)

// Resolutions of a divergence.
const (
	ResolveKeepErvisio  = "keep_ervisio"
	ResolveKeepGitHub   = "keep_github"
	ResolveMergeRequest = "merge_request"
)

// Link is the GitHub counterpart of a repository.
type Link struct {
	ID                      uuid.UUID
	RepoID                  uuid.UUID
	ConnectionID            uuid.UUID
	Mode                    string
	RemoteOwner, RemoteName string
	Prune                   bool
	TrustGitHub             []string
	CreateMissing           bool
	IntervalMinutes         int
	Enabled                 bool
	Status, LastError       string
	Failures                int
	NextAttemptAt           *time.Time
	LastRunAt               *time.Time
	LastSuccessAt           *time.Time
	LFSSkipped              bool
	CreatedAt, UpdatedAt    time.Time
}

func toLink(l dbgen.GithubSyncLink) Link {
	trust := l.TrustGithub
	if trust == nil {
		trust = []string{}
	}
	return Link{ID: l.ID, RepoID: l.RepoID, ConnectionID: l.ConnectionID, Mode: l.Mode,
		RemoteOwner: l.RemoteOwner, RemoteName: l.RemoteName, Prune: l.Prune, TrustGitHub: trust,
		CreateMissing: l.CreateMissing, IntervalMinutes: int(l.IntervalSeconds / 60), Enabled: l.Enabled,
		Status: l.Status, LastError: l.LastError, Failures: int(l.Failures), NextAttemptAt: ts(l.NextAttemptAt),
		LastRunAt: ts(l.LastRunAt), LastSuccessAt: ts(l.LastSuccessAt), LFSSkipped: l.LfsSkipped,
		CreatedAt: l.CreatedAt.Time, UpdatedAt: l.UpdatedAt.Time}
}

// LinkInput sets the link of a repository; nil fields take their default.
type LinkInput struct {
	ConnectionID            uuid.UUID
	Mode                    string
	RemoteOwner, RemoteName string
	Prune                   *bool
	TrustGitHub             []string
	CreateMissing           *bool
	IntervalMinutes         *int
	Enabled                 *bool
}

// Ref is the sync state of one ref.
type Ref struct {
	Ref, LocalSHA, RemoteSHA, Status, LastError string
	Retries                                     int
	ConflictRef, ConflictSHA                    string // set while diverged
	Resolution                                  string
	UpdatedAt                                   time.Time
}

func toRef(r dbgen.GithubSyncRef) Ref {
	out := Ref{Ref: r.Ref, LocalSHA: r.LocalSha, RemoteSHA: r.RemoteSha, Status: r.Status,
		LastError: r.LastError, Retries: int(r.Retries), ConflictSHA: r.ConflictSha,
		Resolution: r.Resolution, UpdatedAt: r.UpdatedAt.Time}
	if r.ConflictSha != "" {
		out.ConflictRef = ConflictRef(r.Ref)
	}
	return out
}

func (s *Service) settingsRepo(ctx context.Context, who auth.Principal, owner, name string, act authz.Action) (repos.Repo, error) {
	rs, err := s.repoSvc()
	if err != nil {
		return repos.Repo{}, err
	}
	return rs.Authorize(ctx, who, owner, name, act)
}

func (s *Service) linkOf(ctx context.Context, repoID uuid.UUID) (dbgen.GithubSyncLink, error) {
	l, err := s.q.GetGitHubSyncLinkByRepo(ctx, repoID)
	if noRows(err) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, fmt.Errorf("get sync link: %w", err)
	}
	return l, nil
}

// GetLink returns the link of a repository (readers of the repository
// see it: the UI shows the mirror banner and the backup state).
func (s *Service) GetLink(ctx context.Context, who auth.Principal, owner, name string) (Link, error) {
	repo, err := s.settingsRepo(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return Link{}, err
	}
	l, err := s.linkOf(ctx, repo.ID)
	if err != nil {
		return Link{}, err
	}
	return toLink(l), nil
}

func checkTrust(patterns []string) ([]string, error) {
	if len(patterns) > maxTrustPatterns {
		return nil, invalidf("at most %d trustGithub patterns", maxTrustPatterns)
	}
	out := []string{}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || len(p) > 255 {
			return nil, invalidf("invalid trustGithub pattern")
		}
		if _, err := path.Match(p, ""); err != nil {
			return nil, invalidf("invalid trustGithub pattern %q", p)
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// PutLink creates or replaces the link of a repository (repository admins).
// A new mode or GitHub repository forgets the per-ref state. The first
// run is scheduled at once.
func (s *Service) PutLink(ctx context.Context, who auth.Principal, owner, name string, in LinkInput) (Link, error) {
	repo, err := s.settingsRepo(ctx, who, owner, name, authz.ActSettings)
	if err != nil {
		return Link{}, err
	}
	switch in.Mode {
	case ModeMirror, ModeBackup, ModeSync:
	default:
		return Link{}, invalidf("mode must be mirror, backup or sync")
	}
	if err := checkRemote(in.RemoteOwner, in.RemoteName); err != nil {
		return Link{}, err
	}
	trust, err := checkTrust(in.TrustGitHub)
	if err != nil {
		return Link{}, err
	}
	if len(trust) > 0 && in.Mode != ModeSync {
		return Link{}, invalidf("trustGithub applies to sync links only")
	}
	interval := int(MinInterval / time.Minute)
	if in.IntervalMinutes != nil {
		interval = *in.IntervalMinutes
	}
	if interval < int(MinInterval/time.Minute) || interval > maxIntervalMins {
		return Link{}, invalidf("intervalMinutes must be between %d and %d", int(MinInterval/time.Minute), maxIntervalMins)
	}
	if imp, err := s.q.GetGitHubImportByRepo(ctx, repo.ID); err == nil && (imp.Status == "pending" || imp.Status == "running") {
		return Link{}, ErrImported
	}
	conn, err := s.usable(ctx, who, in.ConnectionID, repo.OwnerID)
	if err != nil {
		return Link{}, err
	}
	old, oldErr := s.linkOf(ctx, repo.ID)
	if oldErr != nil && !errors.Is(oldErr, ErrNotFound) {
		return Link{}, oldErr
	}
	id := uuid.New()
	if oldErr == nil {
		id = old.ID
	}
	row, err := s.q.UpsertGitHubSyncLink(ctx, dbgen.UpsertGitHubSyncLinkParams{ID: id, RepoID: repo.ID,
		ConnectionID: conn.ID, Mode: in.Mode, RemoteOwner: in.RemoteOwner, RemoteName: in.RemoteName,
		Prune: in.Prune != nil && *in.Prune, TrustGithub: trust,
		CreateMissing:   in.CreateMissing != nil && *in.CreateMissing && in.Mode != ModeMirror,
		IntervalSeconds: int32(interval * 60), Enabled: in.Enabled == nil || *in.Enabled,
		CreatedBy: nullUser(who.UserID)})
	if err != nil {
		return Link{}, fmt.Errorf("save sync link: %w", err)
	}
	if oldErr == nil && (old.Mode != row.Mode || old.ConnectionID != row.ConnectionID ||
		!strings.EqualFold(old.RemoteOwner, row.RemoteOwner) || !strings.EqualFold(old.RemoteName, row.RemoteName)) {
		if err := s.q.ResetGitHubSyncRefsOnModeChange(ctx, row.ID); err != nil {
			return Link{}, fmt.Errorf("reset sync refs: %w", err)
		}
	}
	if row.Enabled {
		if err := s.enqueue(ctx, RunArgs{RepoID: row.RepoID}); err != nil {
			s.o.Log.WarnContext(ctx, "enqueue github sync", "repo", repo.ID, "error", err)
		}
	}
	s.o.Log.InfoContext(ctx, "github sync link saved", "repo", repo.ID, "mode", row.Mode)
	return toLink(row), nil
}

// DeleteLink removes the link of a repository. Refs already written stay.
func (s *Service) DeleteLink(ctx context.Context, who auth.Principal, owner, name string) error {
	repo, err := s.settingsRepo(ctx, who, owner, name, authz.ActSettings)
	if err != nil {
		return err
	}
	n, err := s.q.DeleteGitHubSyncLinkByRepo(ctx, repo.ID)
	if err != nil {
		return fmt.Errorf("delete sync link: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListRefs returns the per-ref state of the link of a repository.
func (s *Service) ListRefs(ctx context.Context, who auth.Principal, owner, name string) ([]Ref, error) {
	repo, err := s.settingsRepo(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return nil, err
	}
	l, err := s.linkOf(ctx, repo.ID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListGitHubSyncRefs(ctx, l.ID)
	if err != nil {
		return nil, fmt.Errorf("list sync refs: %w", err)
	}
	out := make([]Ref, 0, len(rows))
	for _, r := range rows {
		out = append(out, toRef(r))
	}
	return out, nil
}

// Run schedules a run now, ignoring the backoff (repository admins).
func (s *Service) Run(ctx context.Context, who auth.Principal, owner, name string) error {
	repo, err := s.settingsRepo(ctx, who, owner, name, authz.ActSettings)
	if err != nil {
		return err
	}
	l, err := s.linkOf(ctx, repo.ID)
	if err != nil {
		return err
	}
	if !l.Enabled {
		return invalidf("the link is disabled")
	}
	if err := s.enqueue(ctx, RunArgs{RepoID: l.RepoID, Force: true}); err != nil {
		return fmt.Errorf("enqueue github sync: %w", err)
	}
	return nil
}

// Resolve settles a divergence of a sync link (repository admins):
// keep_ervisio pushes the local branch over GitHub, keep_github moves the
// local branch to the GitHub commit (the local one is kept at
// refs/heads/sync-conflict/ervisio/<branch>), merge_request records that
// the divergence is settled by a merge request from the conflict branch:
// once merged, the next run pushes the merge.
func (s *Service) Resolve(ctx context.Context, who auth.Principal, owner, name, ref, resolution string) (Ref, error) {
	repo, err := s.settingsRepo(ctx, who, owner, name, authz.ActSettings)
	if err != nil {
		return Ref{}, err
	}
	ref, err = branchRef(ref)
	if err != nil {
		return Ref{}, err
	}
	if err := git.ValidateRefName(ref); err != nil {
		return Ref{}, invalidf("invalid ref")
	}
	switch resolution {
	case ResolveKeepErvisio, ResolveKeepGitHub, ResolveMergeRequest:
	default:
		return Ref{}, invalidf("resolution must be keep_ervisio, keep_github or merge_request")
	}
	l, err := s.linkOf(ctx, repo.ID)
	if err != nil {
		return Ref{}, err
	}
	row, err := s.q.GetGitHubSyncRef(ctx, dbgen.GetGitHubSyncRefParams{LinkID: l.ID, Ref: ref})
	if noRows(err) {
		return Ref{}, ErrNotDiverged
	}
	if err != nil {
		return Ref{}, fmt.Errorf("get sync ref: %w", err)
	}
	if row.Status != stateDiverged || row.ConflictSha == "" {
		return Ref{}, ErrNotDiverged
	}
	st := stateOf(row)
	switch resolution {
	case ResolveMergeRequest:
		st.resolution = ResolveMergeRequest
	case ResolveKeepErvisio:
		st, err = s.keepErvisio(ctx, l, ref, st)
	case ResolveKeepGitHub:
		st, err = s.keepGitHub(ctx, l, ref, st)
	}
	if err != nil {
		return Ref{}, err
	}
	if err := s.q.UpsertGitHubSyncRef(ctx, dbgen.UpsertGitHubSyncRefParams{LinkID: l.ID, Ref: ref,
		LocalSha: st.local, RemoteSha: st.remote, Status: st.status, LastError: st.lastError,
		Retries: st.retries, ConflictSha: st.conflict, Resolution: st.resolution}); err != nil {
		return Ref{}, fmt.Errorf("save sync ref: %w", err)
	}
	s.o.Log.InfoContext(ctx, "github divergence resolved", "repo", repo.ID, "ref", ref, "resolution", resolution)
	out, err := s.q.GetGitHubSyncRef(ctx, dbgen.GetGitHubSyncRefParams{LinkID: l.ID, Ref: ref})
	if err != nil {
		return Ref{}, fmt.Errorf("get sync ref: %w", err)
	}
	return toRef(out), nil
}

// conflictError turns a refused resolution into a 409 with its reason.
type conflictError struct{ msg string }

func (e *conflictError) Error() string { return e.msg }

// IsConflict reports whether err is a resolution GitHub or the local
// repository refused (HTTP 409).
func IsConflict(err error) bool {
	var c *conflictError
	return errors.As(err, &c) || errors.Is(err, ErrNotDiverged)
}

func (s *Service) keepErvisio(ctx context.Context, link dbgen.GithubSyncLink, ref string, st refState) (refState, error) {
	r, err := s.open(ctx, link.ConnectionID, link.RemoteOwner, link.RemoteName)
	if err != nil {
		return st, err
	}
	l, err := s.acquire(ctx, link.RepoID, gitstore.Write)
	if errors.Is(err, errSkip) {
		return st, &conflictError{"the repository is not available, try again later"}
	}
	if err != nil {
		return st, err
	}
	defer l.Release()
	local, err := listRefs(ctx, l.Path, ref, ConflictRef(ref))
	if err != nil {
		return st, err
	}
	sha := local[ref]
	if sha == "" {
		return st, &conflictError{"the branch no longer exists"}
	}
	res, err := push(ctx, l.Path, r.url, r.env, []string{"--force-with-lease=" + ref + ":" + st.conflict},
		[]string{"+" + sha + ":" + ref})
	if err != nil {
		return st, err
	}
	if pr, ok := res[ref]; !ok || !pr.ok {
		return st, &conflictError{"GitHub refused the push (the branch changed on GitHub since the divergence was found): " + pr.summary}
	}
	if c := local[ConflictRef(ref)]; c == st.conflict {
		if err := s.updateRefs(ctx, l, []edit{{ref: ConflictRef(ref), oldSHA: c, verify: true}}); err != nil {
			s.o.Log.WarnContext(ctx, "delete conflict branch", "repo", link.RepoID, "error", err)
		}
	}
	return refState{local: sha, remote: sha, status: stateOK}, nil
}

func (s *Service) keepGitHub(ctx context.Context, link dbgen.GithubSyncLink, ref string, st refState) (refState, error) {
	l, err := s.acquire(ctx, link.RepoID, gitstore.Write)
	if errors.Is(err, errSkip) {
		return st, &conflictError{"the repository is not available, try again later"}
	}
	if err != nil {
		return st, err
	}
	defer l.Release()
	local, err := listRefs(ctx, l.Path, ref, ConflictRef(ref))
	if err != nil {
		return st, err
	}
	cur := local[ref]
	branch := strings.TrimPrefix(ref, headsPrefix)
	edits := []edit{{ref: ref, newSHA: st.conflict, oldSHA: cur, verify: true}}
	if cur != "" {
		edits = append(edits, edit{ref: ConflictErvisio + branch, newSHA: cur})
	}
	if c := local[ConflictRef(ref)]; c == st.conflict {
		edits = append(edits, edit{ref: ConflictRef(ref), oldSHA: c, verify: true})
	}
	if err := s.updateRefs(ctx, l, edits); err != nil {
		return st, &conflictError{"the branch could not be updated: " + refError(err)}
	}
	return refState{local: st.conflict, remote: st.conflict, status: stateOK}, nil
}
