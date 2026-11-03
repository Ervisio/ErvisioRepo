package issues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// TimelineEvent is an entry of the timeline of an issue. Only the fields
// relevant to Event are set.
type TimelineEvent struct {
	ID          uuid.UUID
	Event       string
	Actor       string
	CreatedAt   time.Time
	Label       *Label
	Assignee    string
	Milestone   string
	RenameFrom  string
	RenameTo    string
	StateReason string
	LockReason  string
	Comment     *Comment
	Source      *TimelineSource
}

// TimelineSource tells where an issue was referenced or closed from. When
// Redacted is set the source is in a repository the viewer cannot read and
// nothing else is set.
type TimelineSource struct {
	Kind     string // issue, pull or commit
	Redacted bool
	Repo     string // owner/name
	Number   int64
	Title    string
	Commit   string
}

type eventData struct {
	LabelID         uuid.UUID     `json:"label_id"`
	Name            string        `json:"name"`
	Color           string        `json:"color"`
	Description     string        `json:"description"`
	UserID          uuid.UUID     `json:"user_id"`
	Title           string        `json:"title"`
	From            string        `json:"from"`
	To              string        `json:"to"`
	Reason          string        `json:"reason"`
	Commit          string        `json:"commit"`
	Pull            int64         `json:"pull"`
	RepoID          uuid.UUID     `json:"repo_id"`
	SourceRepoID    uuid.UUID     `json:"source_repo_id"`
	SourceIssueID   uuid.UUID     `json:"source_issue_id"`
	SourceCommentID uuid.NullUUID `json:"source_comment_id"`
}

// visibility caches which repositories the viewer can read.
type visibility struct {
	s     *Service
	who   auth.Principal
	repos map[uuid.UUID]*repos.Repo
}

func (v *visibility) repo(ctx context.Context, id uuid.UUID) (*repos.Repo, error) {
	if r, ok := v.repos[id]; ok {
		return r, nil
	}
	r, err := v.s.o.Repos.GetByID(ctx, v.who, id)
	var out *repos.Repo
	switch {
	case errors.Is(err, repos.ErrNotFound), errors.Is(err, repos.ErrForbidden):
	case err != nil:
		return nil, fmt.Errorf("timeline source repository: %w", err)
	default:
		out = &r
	}
	v.repos[id] = out
	return out, nil
}

// ListTimeline returns the timeline of an issue, oldest first. References
// from repositories the viewer cannot read are redacted.
func (s *Service) ListTimeline(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]TimelineEvent, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	i, err := s.get(ctx, s.q, r, number)
	if err != nil {
		return nil, err
	}
	off, lim := pageArgs(offset, limit)
	rows, err := s.q.ListIssueEvents(ctx, dbgen.ListIssueEventsParams{IssueID: i.ID, Off: off, Lim: lim})
	if err != nil {
		return nil, fmt.Errorf("list timeline: %w", err)
	}
	data := make([]eventData, len(rows))
	var userIDs, commentIDs []uuid.UUID
	for k, e := range rows {
		if len(e.Data) > 0 {
			if err := json.Unmarshal(e.Data, &data[k]); err != nil {
				return nil, fmt.Errorf("decode %s event: %w", e.Event, err)
			}
		}
		if e.ActorID.Valid {
			userIDs = append(userIDs, e.ActorID.UUID)
		}
		if data[k].UserID != uuid.Nil {
			userIDs = append(userIDs, data[k].UserID)
		}
		if e.CommentID.Valid {
			commentIDs = append(commentIDs, e.CommentID.UUID)
		}
	}
	names, err := s.names(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	comments := map[uuid.UUID]*Comment{}
	if len(commentIDs) > 0 {
		cs, err := s.q.GetIssueCommentsByIDs(ctx, commentIDs)
		if err != nil {
			return nil, fmt.Errorf("timeline comments: %w", err)
		}
		hc, err := s.hydrateComments(ctx, who.UserID, cs)
		if err != nil {
			return nil, err
		}
		for k := range hc {
			comments[hc[k].ID] = &hc[k]
		}
	}
	vis := &visibility{s: s, who: who, repos: map[uuid.UUID]*repos.Repo{r.ID: &r}}
	out := make([]TimelineEvent, 0, len(rows))
	for k, e := range rows {
		d := data[k]
		t := TimelineEvent{ID: e.ID, Event: e.Event, CreatedAt: e.CreatedAt.Time}
		if e.ActorID.Valid {
			t.Actor = names[e.ActorID.UUID]
		}
		switch e.Event {
		case "commented":
			c, ok := comments[e.CommentID.UUID]
			if !ok {
				continue // deleted meanwhile
			}
			t.Comment = c
		case "labeled", "unlabeled":
			t.Label = &Label{ID: d.LabelID, Name: d.Name, Color: d.Color, Description: d.Description}
		case "assigned", "unassigned":
			t.Assignee = names[d.UserID]
		case "milestoned", "demilestoned":
			t.Milestone = d.Title
		case "renamed":
			t.RenameFrom, t.RenameTo = d.From, d.To
		case "closed":
			t.StateReason = d.Reason
			if t.Source, err = s.closeSource(ctx, vis, d); err != nil {
				return nil, err
			}
		case "reopened":
			t.StateReason = ReasonReopened
		case "locked":
			t.LockReason = d.Reason
		case "referenced":
			if t.Source, err = s.commitSource(ctx, vis, d.RepoID, d.Commit); err != nil {
				return nil, err
			}
		case "cross-referenced":
			if t.Source, err = s.issueSource(ctx, vis, d.SourceRepoID, d.SourceIssueID); err != nil {
				return nil, err
			}
			if t.Source == nil {
				continue // the source issue was deleted
			}
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Service) commitSource(ctx context.Context, vis *visibility, repoID uuid.UUID, commit string) (*TimelineSource, error) {
	if commit == "" {
		return nil, nil
	}
	sr, err := vis.repo(ctx, repoID)
	if err != nil {
		return nil, err
	}
	if sr == nil {
		return &TimelineSource{Kind: "commit", Redacted: true}, nil
	}
	return &TimelineSource{Kind: "commit", Repo: sr.FullName(), Commit: commit}, nil
}

func (s *Service) closeSource(ctx context.Context, vis *visibility, d eventData) (*TimelineSource, error) {
	switch {
	case d.Commit != "":
		return s.commitSource(ctx, vis, d.RepoID, d.Commit)
	case d.Pull > 0 && d.RepoID != uuid.Nil:
		sr, err := vis.repo(ctx, d.RepoID)
		if err != nil {
			return nil, err
		}
		if sr == nil {
			return &TimelineSource{Kind: KindPull, Redacted: true}, nil
		}
		return &TimelineSource{Kind: KindPull, Repo: sr.FullName(), Number: d.Pull}, nil
	}
	return nil, nil
}

func (s *Service) issueSource(ctx context.Context, vis *visibility, repoID, issueID uuid.UUID) (*TimelineSource, error) {
	sr, err := vis.repo(ctx, repoID)
	if err != nil {
		return nil, err
	}
	if sr == nil {
		return &TimelineSource{Kind: KindIssue, Redacted: true}, nil
	}
	si, err := s.q.GetIssueByID(ctx, issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("timeline source issue: %w", err)
	}
	return &TimelineSource{Kind: si.Kind, Repo: sr.FullName(), Number: si.Number, Title: si.Title}, nil
}
