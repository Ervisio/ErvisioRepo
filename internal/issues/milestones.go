package issues

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// MilestoneInput creates or edits a milestone; nil fields are kept (Title is
// required on create). DueOn "" clears the due date; otherwise it is an
// RFC 3339 time or a YYYY-MM-DD date. State is open or closed.
type MilestoneInput struct {
	Title       *string
	Description *string
	DueOn       *string
	State       *string
}

func parseDue(v string) (pgtype.Timestamptz, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return pgtype.Timestamptz{}, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return ts(t), nil
	}
	if t, err := time.Parse(time.DateOnly, v); err == nil {
		return ts(t), nil
	}
	return pgtype.Timestamptz{}, invalid("dueOn must be an RFC 3339 time or a YYYY-MM-DD date")
}

// ListMilestones returns the milestones of owner/name. state is open
// (default), closed or all.
func (s *Service) ListMilestones(ctx context.Context, who auth.Principal, owner, name, state string, offset, limit int) ([]Milestone, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	switch state {
	case "":
		state = StateOpen
	case StateOpen, StateClosed:
	case "all":
		state = ""
	default:
		return nil, invalid("state must be open, closed or all")
	}
	off, lim := pageArgs(offset, limit)
	rows, err := s.q.ListMilestones(ctx, dbgen.ListMilestonesParams{RepoID: r.ID, State: state, Off: off, Lim: lim})
	if err != nil {
		return nil, fmt.Errorf("list milestones: %w", err)
	}
	out := make([]Milestone, 0, len(rows))
	for _, m := range rows {
		out = append(out, milestoneFrom(dbgen.Milestone{ID: m.ID, RepoID: m.RepoID, Title: m.Title, Description: m.Description,
			State: m.State, DueOn: m.DueOn, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, ClosedAt: m.ClosedAt},
			m.OpenIssues, m.ClosedIssues))
	}
	return out, nil
}

func (s *Service) milestone(ctx context.Context, r repos.Repo, id uuid.UUID) (Milestone, error) {
	ms, err := s.q.GetMilestonesByIDs(ctx, dbgen.GetMilestonesByIDsParams{RepoID: r.ID, Ids: []uuid.UUID{id}})
	if err != nil {
		return Milestone{}, fmt.Errorf("get milestone: %w", err)
	}
	if len(ms) == 0 {
		return Milestone{}, ErrMilestoneNotFound
	}
	m := ms[0]
	return milestoneFrom(dbgen.Milestone{ID: m.ID, RepoID: m.RepoID, Title: m.Title, Description: m.Description,
		State: m.State, DueOn: m.DueOn, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, ClosedAt: m.ClosedAt},
		m.OpenIssues, m.ClosedIssues), nil
}

// GetMilestone returns a milestone of owner/name.
func (s *Service) GetMilestone(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (Milestone, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return Milestone{}, err
	}
	return s.milestone(ctx, r, id)
}

// CreateMilestone creates a milestone (triage).
func (s *Service) CreateMilestone(ctx context.Context, who auth.Principal, owner, name string, in MilestoneInput, c identity.Client) (Milestone, error) {
	r, err := s.triager(ctx, who, owner, name)
	if err != nil {
		return Milestone{}, err
	}
	if in.Title == nil {
		return Milestone{}, invalid("title is required")
	}
	title, err := checkTitle("title", *in.Title, MaxMilestoneTitle)
	if err != nil {
		return Milestone{}, err
	}
	desc := ""
	if in.Description != nil {
		desc = *in.Description
		if err := checkText("description", desc, MaxMilestoneDesc); err != nil {
			return Milestone{}, err
		}
	}
	var due pgtype.Timestamptz
	if in.DueOn != nil {
		if due, err = parseDue(*in.DueOn); err != nil {
			return Milestone{}, err
		}
	}
	id, err := newID()
	if err != nil {
		return Milestone{}, err
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		row, err := q.InsertMilestone(ctx, dbgen.InsertMilestoneParams{ID: id, RepoID: r.ID, Title: title, Description: desc, DueOn: due})
		if isUnique(err) {
			return ErrMilestoneTaken
		}
		if err != nil {
			return fmt.Errorf("insert milestone: %w", err)
		}
		if err := s.emit(ctx, q, events.KindMilestone, "created", r.ID, who.UserID, row.ID, EventPayload{Milestone: row.Title}); err != nil {
			return err
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionMilestoneCreated, r, "milestone", row.ID, c, map[string]any{"title": row.Title}))
	})
	if err != nil {
		return Milestone{}, err
	}
	return s.milestone(ctx, r, id)
}

// UpdateMilestone edits, closes or reopens a milestone (triage).
func (s *Service) UpdateMilestone(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, in MilestoneInput, c identity.Client) (Milestone, error) {
	r, err := s.triager(ctx, who, owner, name)
	if err != nil {
		return Milestone{}, err
	}
	cur, err := s.milestone(ctx, r, id)
	if err != nil {
		return Milestone{}, err
	}
	title, desc, state := cur.Title, cur.Description, cur.State
	due := pgtype.Timestamptz{}
	if cur.DueOn != nil {
		due = ts(*cur.DueOn)
	}
	if in.Title != nil {
		if title, err = checkTitle("title", *in.Title, MaxMilestoneTitle); err != nil {
			return Milestone{}, err
		}
	}
	if in.Description != nil {
		desc = *in.Description
		if err := checkText("description", desc, MaxMilestoneDesc); err != nil {
			return Milestone{}, err
		}
	}
	if in.DueOn != nil {
		if due, err = parseDue(*in.DueOn); err != nil {
			return Milestone{}, err
		}
	}
	if in.State != nil {
		if *in.State != StateOpen && *in.State != StateClosed {
			return Milestone{}, invalid("state must be open or closed")
		}
		state = *in.State
	}
	action := "edited"
	switch {
	case state == StateClosed && cur.State != StateClosed:
		action = "closed"
	case state == StateOpen && cur.State != StateOpen:
		action = "opened"
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		row, err := q.UpdateMilestone(ctx, dbgen.UpdateMilestoneParams{ID: id, RepoID: r.ID, Title: title, Description: desc,
			DueOn: due, State: state})
		if isUnique(err) {
			return ErrMilestoneTaken
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMilestoneNotFound
		}
		if err != nil {
			return fmt.Errorf("update milestone: %w", err)
		}
		if err := s.emit(ctx, q, events.KindMilestone, action, r.ID, who.UserID, row.ID, EventPayload{Milestone: row.Title, State: row.State}); err != nil {
			return err
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionMilestoneUpdated, r, "milestone", row.ID, c,
			map[string]any{"title": row.Title, "state": row.State}))
	})
	if err != nil {
		return Milestone{}, err
	}
	return s.milestone(ctx, r, id)
}

// DeleteMilestone deletes a milestone; its issues keep no milestone (triage).
func (s *Service) DeleteMilestone(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, c identity.Client) error {
	r, err := s.triager(ctx, who, owner, name)
	if err != nil {
		return err
	}
	cur, err := s.milestone(ctx, r, id)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteMilestone(ctx, dbgen.DeleteMilestoneParams{ID: id, RepoID: r.ID})
		if err != nil {
			return fmt.Errorf("delete milestone: %w", err)
		}
		if n == 0 {
			return ErrMilestoneNotFound
		}
		if err := s.emit(ctx, q, events.KindMilestone, "deleted", r.ID, who.UserID, id, EventPayload{Milestone: cur.Title}); err != nil {
			return err
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionMilestoneDeleted, r, "milestone", id, c, map[string]any{"title": cur.Title}))
	})
}
