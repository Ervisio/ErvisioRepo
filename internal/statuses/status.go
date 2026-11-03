package statuses

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/protection"
)

func validState(s string) bool {
	switch s {
	case StateError, StateFailure, StatePending, StateSuccess:
		return true
	}
	return false
}

// CreateStatus records a commit status on the commit ref resolves to. It
// needs the push permission.
func (s *Service) CreateStatus(ctx context.Context, who auth.Principal, owner, name, ref string, in StatusInput) (Status, error) {
	in.State = strings.TrimSpace(in.State)
	if !validState(in.State) {
		return Status{}, invalid("state must be one of error, failure, pending, success")
	}
	in.Context = strings.TrimSpace(in.Context)
	if in.Context == "" {
		in.Context = "default"
	}
	if err := checkLen("context", in.Context, 255); err != nil {
		return Status{}, err
	}
	if err := checkLen("description", in.Description, 1024); err != nil {
		return Status{}, err
	}
	if err := checkURL("target_url", in.TargetURL); err != nil {
		return Status{}, err
	}
	r, err := s.access(ctx, who, owner, name, true)
	if err != nil {
		return Status{}, err
	}
	sha, _, err := s.resolve(ctx, r, ref)
	if err != nil {
		return Status{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Status{}, fmt.Errorf("status id: %w", err)
	}
	var row dbgen.CommitStatus
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.CountCommitStatuses(ctx, dbgen.CountCommitStatusesParams{RepoID: r.ID, Sha: sha, Context: in.Context})
		if err != nil {
			return fmt.Errorf("count statuses: %w", err)
		}
		if n >= MaxStatusesPerContext {
			return invalid("this commit already has %d statuses for the context %q", MaxStatusesPerContext, in.Context)
		}
		row, err = q.InsertCommitStatus(ctx, dbgen.InsertCommitStatusParams{ID: id, RepoID: r.ID, Sha: sha,
			State: in.State, Context: in.Context, Description: in.Description, TargetUrl: in.TargetURL,
			CreatorID: nullID(who.UserID)})
		if err != nil {
			return fmt.Errorf("insert status: %w", err)
		}
		return events.Emit(ctx, q, events.Event{Kind: events.KindStatus, Action: "created", RepoID: r.ID,
			ActorID: who.UserID, SubjectID: id}, events.StatusPayload{ID: id, SHA: sha, Context: in.Context,
			State: in.State, Description: in.Description, TargetURL: in.TargetURL, CreatedAt: row.CreatedAt.Time})
	})
	if err != nil {
		return Status{}, err
	}
	s.kick(ctx)
	s.log.InfoContext(ctx, "commit status created", "repo", r.ID, "sha", sha, "context", in.Context, "state", in.State)
	st := statusOf(row.ID, row.RepoID, row.Sha, row.State, row.Context, row.Description, row.TargetUrl, row.CreatorID, who.Username, row.CreatedAt)
	return st, nil
}

// ListStatuses lists the statuses of the commit ref resolves to, newest
// first. It returns the resolved sha too.
func (s *Service) ListStatuses(ctx context.Context, who auth.Principal, owner, name, ref string, offset, limit int) ([]Status, string, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return nil, "", err
	}
	sha, _, err := s.resolve(ctx, r, ref)
	if err != nil {
		return nil, "", err
	}
	off, lim := page(offset, limit)
	rows, err := s.q.ListCommitStatuses(ctx, dbgen.ListCommitStatusesParams{RepoID: r.ID, Sha: sha, Off: off, Lim: lim})
	if err != nil {
		return nil, "", fmt.Errorf("list statuses: %w", err)
	}
	out := make([]Status, 0, len(rows))
	for _, x := range rows {
		out = append(out, statusOf(x.ID, x.RepoID, x.Sha, x.State, x.Context, x.Description, x.TargetUrl, x.CreatorID, x.CreatorName, x.CreatedAt))
	}
	return out, sha, nil
}

// CombinedStatus answers the latest status of each context of the commit
// ref resolves to, and their aggregate: failure when any is error or
// failure, pending when any is pending or there is none, success otherwise.
func (s *Service) CombinedStatus(ctx context.Context, who auth.Principal, owner, name, ref string) (Combined, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return Combined{}, err
	}
	sha, _, err := s.resolve(ctx, r, ref)
	if err != nil {
		return Combined{}, err
	}
	list, err := s.latest(ctx, r.ID, sha)
	if err != nil {
		return Combined{}, err
	}
	return Combined{SHA: sha, State: combine(list), Statuses: list}, nil
}

func (s *Service) latest(ctx context.Context, repoID uuid.UUID, sha string) ([]Status, error) {
	rows, err := s.q.LatestCommitStatuses(ctx, dbgen.LatestCommitStatusesParams{RepoID: repoID, Sha: sha})
	if err != nil {
		return nil, fmt.Errorf("latest statuses: %w", err)
	}
	out := make([]Status, 0, len(rows))
	for _, x := range rows {
		out = append(out, statusOf(x.ID, x.RepoID, x.Sha, x.State, x.Context, x.Description, x.TargetUrl, x.CreatorID, x.CreatorName, x.CreatedAt))
	}
	return out, nil
}

func combine(list []Status) string {
	if len(list) == 0 {
		return StatePending
	}
	state := StateSuccess
	for _, st := range list {
		switch st.State {
		case StateError, StateFailure:
			return StateFailure
		case StatePending:
			state = StatePending
		}
	}
	return state
}

// Statuses implements protection.CheckSource: the latest commit status of
// each context, and the latest check run of each name mapped to a status
// state (success, neutral and skipped pass; other conclusions fail; a run
// that is not completed is pending).
func (s *Service) Statuses(ctx context.Context, repoID uuid.UUID, sha string) ([]protection.CheckStatus, error) {
	list, err := s.latest(ctx, repoID, sha)
	if err != nil {
		return nil, err
	}
	runs, err := s.q.LatestCheckRunStates(ctx, dbgen.LatestCheckRunStatesParams{RepoID: repoID, HeadSha: sha})
	if err != nil {
		return nil, fmt.Errorf("latest check runs: %w", err)
	}
	out := make([]protection.CheckStatus, 0, len(list)+len(runs))
	for _, st := range list {
		out = append(out, protection.CheckStatus{Context: st.Context, State: st.State})
	}
	for _, run := range runs {
		out = append(out, protection.CheckStatus{Context: run.Name, State: runState(run.Status, run.Conclusion)})
	}
	return out, nil
}

func runState(status, conclusion string) string {
	if status != StatusCompleted {
		return StatePending
	}
	switch conclusion {
	case "success", "neutral", "skipped":
		return StateSuccess
	}
	return StateFailure
}

func statusOf(id, repoID uuid.UUID, sha, state, ctxName, desc, target string, creator uuid.NullUUID, creatorName string, at pgtype.Timestamptz) Status {
	return Status{ID: id, RepoID: repoID, SHA: sha, State: state, Context: ctxName, Description: desc,
		TargetURL: target, CreatorID: creator.UUID, Creator: creatorName, CreatedAt: at.Time}
}
