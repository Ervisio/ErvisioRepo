package ghsync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

// errSkip ends a run without recording it (repository cold or gone);
// errUnchanged ends a backup run with nothing to push.
var (
	errSkip      = errors.New("ghsync: run skipped")
	errUnchanged = errors.New("ghsync: nothing to push")
)

// remote is an opened connection ready for git and the API.
type remote struct {
	api  *API
	url  string
	env  []string
	kind string
}

// open authenticates the connection of a link (or an import) and returns
// the clone URL of owner/name with the git environment carrying the token.
func (s *Service) open(ctx context.Context, connID uuid.UUID, owner, name string) (remote, error) {
	row, err := s.q.GetGitHubConnection(ctx, connID)
	if noRows(err) {
		return remote{}, errors.New("the GitHub connection was deleted")
	}
	if err != nil {
		return remote{}, fmt.Errorf("get GitHub connection: %w", err)
	}
	cred, err := s.credential(row.GithubConnection)
	if err != nil {
		return remote{}, err
	}
	tok, err := s.tokens.token(ctx, cred)
	if err != nil {
		return remote{}, err
	}
	return remote{
		api:  &API{base: APIURL(cred.baseURL), token: tok, http: s.tokens.http},
		url:  cloneURL(cred.baseURL, owner, name),
		env:  gitAuthEnv(tok),
		kind: cred.kind,
	}, nil
}

// acquire takes a lease on a hot repository; errSkip when it is cold or
// gone, so that a sync never hydrates a repository by itself.
func (s *Service) acquire(ctx context.Context, repoID uuid.UUID, mode gitstore.Mode) (*gitstore.Lease, error) {
	row, err := s.q.GetRepoStorage(ctx, repoID)
	if noRows(err) {
		return nil, errSkip
	}
	if err != nil {
		return nil, fmt.Errorf("load repository state: %w", err)
	}
	if row.StorageState != gitstore.StateHot {
		return nil, errSkip
	}
	l, err := s.o.Tier.Acquire(ctx, repoID, mode)
	if errors.Is(err, gitstore.ErrNotFound) || errors.Is(err, gitstore.ErrCold) {
		return nil, errSkip
	}
	if err != nil {
		return nil, fmt.Errorf("acquire repository: %w", err)
	}
	return l, nil
}

// prevStates loads the per-ref state of a link.
func (s *Service) prevStates(ctx context.Context, linkID uuid.UUID) (map[string]refState, error) {
	rows, err := s.q.ListGitHubSyncRefs(ctx, linkID)
	if err != nil {
		return nil, fmt.Errorf("list sync refs: %w", err)
	}
	m := make(map[string]refState, len(rows))
	for _, r := range rows {
		m[r.Ref] = stateOf(r)
	}
	return m, nil
}

// saveStates writes the per-ref state of a run; refs gone on both sides
// lose their row.
func (s *Service) saveStates(ctx context.Context, linkID uuid.UUID, states, prev map[string]refState) error {
	for ref, st := range states {
		if old, ok := prev[ref]; ok && old == st {
			continue
		}
		if err := s.q.UpsertGitHubSyncRef(ctx, dbgen.UpsertGitHubSyncRefParams{LinkID: linkID, Ref: ref,
			LocalSha: st.local, RemoteSha: st.remote, Status: st.status, LastError: st.lastError,
			Retries: st.retries, ConflictSha: st.conflict, Resolution: st.resolution}); err != nil {
			return fmt.Errorf("save sync ref: %w", err)
		}
	}
	for ref := range prev {
		if _, ok := states[ref]; !ok {
			if err := s.q.DeleteGitHubSyncRef(ctx, dbgen.DeleteGitHubSyncRefParams{LinkID: linkID, Ref: ref}); err != nil {
				return fmt.Errorf("delete sync ref: %w", err)
			}
		}
	}
	return nil
}

// finish records the outcome of a run on the link: a failure (of the run
// or of a ref) schedules the next attempt after an exponential backoff.
func (s *Service) finish(ctx context.Context, link dbgen.GithubSyncLink, runErr error, states map[string]refState, lfs bool) error {
	status, msg := stateOK, ""
	refErrs, diverged := 0, 0
	for _, st := range states {
		switch st.status {
		case stateError:
			refErrs++
		case stateDiverged:
			diverged++
		}
	}
	switch {
	case runErr != nil:
		status, msg = stateError, safeError(runErr)
	case refErrs > 0:
		status, msg = stateError, fmt.Sprintf("%d refs could not be synchronized", refErrs)
	case diverged > 0:
		status, msg = stateDiverged, fmt.Sprintf("%d branches diverged from GitHub", diverged)
	}
	failures := int32(0)
	next := pgtype.Timestamptz{}
	if status == stateError {
		failures = link.Failures + 1
		next = pgtype.Timestamptz{Time: time.Now().Add(backoff(failures)), Valid: true}
	}
	if err := s.q.FinishGitHubSyncRun(ctx, dbgen.FinishGitHubSyncRunParams{ID: link.ID, Status: status,
		LastError: msg, Failures: failures, NextAttemptAt: next, LfsSkipped: lfs}); err != nil {
		return fmt.Errorf("finish sync run: %w", err)
	}
	if runErr != nil {
		s.o.Log.WarnContext(ctx, "github sync failed", "repo", link.RepoID, "mode", link.Mode, "error", msg)
	}
	return nil
}

// runLink runs a link once. force skips the equal-SHA shortcut of a
// backup (scheduled checks). It returns gitstore.ErrBusy when the
// repository is busy, so that the job is retried.
func (s *Service) runLink(ctx context.Context, repoID uuid.UUID, force bool) error {
	link, err := s.q.GetGitHubSyncLinkByRepo(ctx, repoID)
	if noRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get sync link: %w", err)
	}
	if !link.Enabled {
		return nil
	}
	prev, err := s.prevStates(ctx, link.ID)
	if err != nil {
		return err
	}
	states, lfs, err := s.runMode(ctx, link, prev, force)
	if errors.Is(err, errSkip) {
		return nil
	}
	if errors.Is(err, gitstore.ErrBusy) {
		return err
	}
	if errors.Is(err, errUnchanged) {
		err = nil
	}
	if err == nil && states != nil {
		if serr := s.saveStates(ctx, link.ID, states, prev); serr != nil {
			err = serr
		}
	}
	if states == nil {
		states = prev
	}
	return s.finish(ctx, link, err, states, lfs)
}

func (s *Service) runMode(ctx context.Context, link dbgen.GithubSyncLink, prev map[string]refState, force bool) (map[string]refState, bool, error) {
	lfs := false
	if link.Mode != ModeMirror {
		has, err := s.q.RepoHasLFSObjects(ctx, link.RepoID)
		if err != nil {
			return nil, false, fmt.Errorf("check LFS objects: %w", err)
		}
		lfs = has
	}
	mode := gitstore.Write
	if link.Mode == ModeBackup {
		mode = gitstore.Read
	}
	l, err := s.acquire(ctx, link.RepoID, mode)
	if err != nil {
		return nil, lfs, err
	}
	defer l.Release()
	local, err := listRefs(ctx, l.Path, headsPrefix, tagsPrefix)
	if err != nil {
		return nil, lfs, err
	}
	if link.Mode == ModeBackup && !force && unchanged(local, prev) {
		return nil, lfs, errUnchanged // loop prevention: nothing new since the last run
	}
	r, err := s.open(ctx, link.ConnectionID, link.RemoteOwner, link.RemoteName)
	if err != nil {
		return nil, lfs, err
	}
	if link.CreateMissing && link.Mode != ModeMirror {
		if _, err := getRepo(ctx, r.api, link.RemoteOwner, link.RemoteName); notFound(err) {
			if err := createRepo(ctx, r.api, r.kind, link.RemoteOwner, link.RemoteName); err != nil {
				return nil, lfs, err
			}
		} else if err != nil {
			return nil, lfs, fmt.Errorf("get GitHub repository: %w", err)
		}
	}
	var states map[string]refState
	switch link.Mode {
	case ModeBackup:
		states, err = s.runBackup(ctx, l, link, r, local, prev)
	case ModeMirror:
		states, err = s.runMirror(ctx, l, link, r, local, prev)
	default:
		states, err = s.runSync(ctx, l, link, r, local, prev)
	}
	return states, lfs, err
}

// unchanged reports whether every local branch and tag has the value
// recorded by the last successful push.
func unchanged(local map[string]string, prev map[string]refState) bool {
	n := 0
	for ref, sha := range local {
		if !syncable(ref) {
			continue
		}
		n++
		st, ok := prev[ref]
		if !ok || st.status != stateOK || st.local != sha || st.remote != sha {
			return false
		}
	}
	tracked := 0
	for _, st := range prev {
		if st.local == "" {
			continue // deleted here, kept on GitHub (no prune)
		}
		tracked++
	}
	return n == tracked
}

// runBackup pushes every branch and tag to GitHub; deletions only with
// prune. LFS objects are not sent.
func (s *Service) runBackup(ctx context.Context, l *gitstore.Lease, link dbgen.GithubSyncLink, r remote, local map[string]string, prev map[string]refState) (map[string]refState, error) {
	var opts []string
	if link.Prune {
		opts = append(opts, "--prune")
	}
	res, err := push(ctx, l.Path, r.url, r.env, opts, []string{
		"+" + headsPrefix + "*:" + headsPrefix + "*", "+" + tagsPrefix + "*:" + tagsPrefix + "*"})
	if err != nil {
		return nil, err
	}
	states := map[string]refState{}
	for ref, sha := range local {
		if !syncable(ref) {
			continue
		}
		st := refState{local: sha, remote: sha, status: stateOK}
		if pr, ok := res[ref]; ok && !pr.ok {
			st = refState{local: sha, remote: prev[ref].remote, status: stateError,
				lastError: "GitHub refused the push: " + pr.summary, retries: prev[ref].retries + 1}
		}
		states[ref] = st
	}
	if !link.Prune {
		// Refs deleted here stay on GitHub; keep them visible.
		for ref, st := range prev {
			if _, ok := local[ref]; !ok && st.remote != "" {
				states[ref] = refState{remote: st.remote, status: stateOK}
			}
		}
	}
	return states, nil
}

// fetchRemote fetches GitHub into the scratch namespace and returns the
// scratch refs and the GitHub refs.
func fetchRemote(ctx context.Context, l *gitstore.Lease, r remote) (map[string]string, map[string]string, error) {
	if err := fetch(ctx, l.Path, r.url, r.env); err != nil {
		return nil, nil, err
	}
	scratch, err := listRefs(ctx, l.Path, scratchPrefix)
	if err != nil {
		return nil, nil, err
	}
	return scratch, remoteRefs(scratch), nil
}

// runMirror makes the repository follow GitHub.
func (s *Service) runMirror(ctx context.Context, l *gitstore.Lease, link dbgen.GithubSyncLink, r remote, local map[string]string, prev map[string]refState) (map[string]refState, error) {
	scratch, rem, err := fetchRemote(ctx, l, r)
	if err != nil {
		return nil, err
	}
	p := planMirror(link, local, rem)
	failed := s.apply(ctx, l, p.groups, cleanup(scratch))
	markFailed(p.states, failed, local, rem, prev)
	return p.states, nil
}

// runSync fast-forwards both sides and records divergences.
func (s *Service) runSync(ctx context.Context, l *gitstore.Lease, link dbgen.GithubSyncLink, r remote, local map[string]string, prev map[string]refState) (map[string]refState, error) {
	scratch, rem, err := fetchRemote(ctx, l, r)
	if err != nil {
		return nil, err
	}
	p, err := s.planSync(ctx, l.Path, link, local, rem, prev)
	if err != nil {
		_ = updateRefsRaw(ctx, l.Path, cleanup(scratch).edits)
		return nil, err
	}
	p.groups = append(p.groups, resolvedConflicts(local, p.states, prev)...)
	failed := s.apply(ctx, l, p.groups, cleanup(scratch))
	markFailed(p.states, failed, local, rem, prev)
	if err := pushSpecs(ctx, l.Path, r.url, r.env, p.pushes, p.states, prev); err != nil {
		return nil, err
	}
	return p.states, nil
}

// resolvedConflicts deletes the conflict branch of every divergence that
// the run found settled, if it still holds the GitHub commit.
func resolvedConflicts(local map[string]string, states, prev map[string]refState) []group {
	var gs []group
	for _, ref := range sortedKeys(toSet(prev)) {
		old, st := prev[ref], states[ref]
		if old.status != stateDiverged || st.status == stateDiverged || old.conflict == "" {
			continue
		}
		cref := ConflictRef(ref)
		if local[cref] == old.conflict {
			gs = append(gs, group{ref: cref, edits: []edit{{ref: cref, oldSHA: old.conflict, verify: true}}})
		}
	}
	return gs
}

func toSet(m map[string]refState) map[string]string {
	out := make(map[string]string, len(m))
	for k := range m {
		out[k] = ""
	}
	return out
}

// markFailed turns the refs whose local write failed into errors that keep
// both observed values.
func markFailed(states map[string]refState, failed map[string]error, local, rem map[string]string, prev map[string]refState) {
	for ref, err := range failed {
		if !syncable(ref) {
			continue // conflict branch cleanup: retried next run
		}
		states[ref] = refState{local: local[ref], remote: rem[ref], status: stateError,
			lastError: "local update refused: " + refError(err), retries: prev[ref].retries + 1,
			conflict: prev[ref].conflict, resolution: prev[ref].resolution}
	}
}

// branchRef validates a branch name of the API and returns its ref.
func branchRef(branch string) (string, error) {
	branch = strings.TrimPrefix(branch, headsPrefix)
	if branch == "" || strings.HasPrefix(headsPrefix+branch, conflictPrefix) {
		return "", invalidf("invalid branch")
	}
	return headsPrefix + branch, nil
}
