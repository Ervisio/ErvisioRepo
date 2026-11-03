package issues

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// DefaultLabelColor is used when a label is created without a color.
const DefaultLabelColor = "ededed"

var colorRe = regexp.MustCompile(`^[0-9a-f]{6}$`)

// LabelInput creates or edits a label; nil fields are kept (Name is
// required on create).
type LabelInput struct {
	Name        *string
	Color       *string
	Description *string
}

func normColor(c string) (string, error) {
	c = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(c), "#"))
	if !colorRe.MatchString(c) {
		return "", invalid("color must be six hexadecimal digits")
	}
	return c, nil
}

func checkLabelName(n string) (string, error) {
	n, err := checkTitle("name", n, MaxLabelName)
	if err != nil {
		return "", err
	}
	if strings.Contains(n, ",") {
		return "", invalid("label names must not contain commas")
	}
	return n, nil
}

// ListLabels returns the labels of owner/name, by name.
func (s *Service) ListLabels(ctx context.Context, who auth.Principal, owner, name string, offset, limit int) ([]Label, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListLabels(ctx, r.ID)
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	off, lim := pageArgs(offset, limit)
	out := []Label{}
	for i := int(off); i < len(rows) && len(out) < int(lim); i++ {
		out = append(out, labelFrom(rows[i]))
	}
	return out, nil
}

func (s *Service) labelByName(ctx context.Context, r repos.Repo, label string) (dbgen.Label, error) {
	l, err := s.q.GetLabelByName(ctx, dbgen.GetLabelByNameParams{RepoID: r.ID, Name: label})
	if err != nil {
		return dbgen.Label{}, notFound(err, ErrLabelNotFound, "get label")
	}
	return l, nil
}

// CreateLabel creates a label (triage).
func (s *Service) CreateLabel(ctx context.Context, who auth.Principal, owner, name string, in LabelInput, c identity.Client) (Label, error) {
	r, err := s.triager(ctx, who, owner, name)
	if err != nil {
		return Label{}, err
	}
	if in.Name == nil {
		return Label{}, invalid("name is required")
	}
	n, err := checkLabelName(*in.Name)
	if err != nil {
		return Label{}, err
	}
	color := DefaultLabelColor
	if in.Color != nil && *in.Color != "" {
		if color, err = normColor(*in.Color); err != nil {
			return Label{}, err
		}
	}
	desc := ""
	if in.Description != nil {
		desc = strings.TrimSpace(*in.Description)
		if err := checkText("description", desc, MaxLabelDescription); err != nil {
			return Label{}, err
		}
	}
	id, err := newID()
	if err != nil {
		return Label{}, err
	}
	var row dbgen.Label
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		var err error
		row, err = q.InsertLabel(ctx, dbgen.InsertLabelParams{ID: id, RepoID: r.ID, Name: n, Color: color, Description: desc})
		if isUnique(err) {
			return ErrLabelTaken
		}
		if err != nil {
			return fmt.Errorf("insert label: %w", err)
		}
		if err := s.emit(ctx, q, events.KindLabel, "created", r.ID, who.UserID, row.ID, EventPayload{Label: row.Name}); err != nil {
			return err
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionLabelCreated, r, "label", row.ID, c, map[string]any{"name": row.Name}))
	})
	if err != nil {
		return Label{}, err
	}
	return labelFrom(row), nil
}

// UpdateLabel edits the label called label (triage).
func (s *Service) UpdateLabel(ctx context.Context, who auth.Principal, owner, name, label string, in LabelInput, c identity.Client) (Label, error) {
	r, err := s.triager(ctx, who, owner, name)
	if err != nil {
		return Label{}, err
	}
	cur, err := s.labelByName(ctx, r, label)
	if err != nil {
		return Label{}, err
	}
	n, color, desc := cur.Name, cur.Color, cur.Description
	if in.Name != nil {
		if n, err = checkLabelName(*in.Name); err != nil {
			return Label{}, err
		}
	}
	if in.Color != nil {
		if color, err = normColor(*in.Color); err != nil {
			return Label{}, err
		}
	}
	if in.Description != nil {
		desc = strings.TrimSpace(*in.Description)
		if err := checkText("description", desc, MaxLabelDescription); err != nil {
			return Label{}, err
		}
	}
	var row dbgen.Label
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		var err error
		row, err = q.UpdateLabel(ctx, dbgen.UpdateLabelParams{ID: cur.ID, RepoID: r.ID, Name: n, Color: color, Description: desc})
		if isUnique(err) {
			return ErrLabelTaken
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLabelNotFound
		}
		if err != nil {
			return fmt.Errorf("update label: %w", err)
		}
		if err := s.emit(ctx, q, events.KindLabel, "edited", r.ID, who.UserID, row.ID, EventPayload{Label: row.Name}); err != nil {
			return err
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionLabelUpdated, r, "label", row.ID, c,
			map[string]any{"name": row.Name, "previous_name": cur.Name}))
	})
	if err != nil {
		return Label{}, err
	}
	return labelFrom(row), nil
}

// DeleteLabel deletes a label and removes it from every issue (triage).
func (s *Service) DeleteLabel(ctx context.Context, who auth.Principal, owner, name, label string, c identity.Client) error {
	r, err := s.triager(ctx, who, owner, name)
	if err != nil {
		return err
	}
	cur, err := s.labelByName(ctx, r, label)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteLabel(ctx, dbgen.DeleteLabelParams{ID: cur.ID, RepoID: r.ID})
		if err != nil {
			return fmt.Errorf("delete label: %w", err)
		}
		if n == 0 {
			return ErrLabelNotFound
		}
		if err := s.emit(ctx, q, events.KindLabel, "deleted", r.ID, who.UserID, cur.ID, EventPayload{Label: cur.Name}); err != nil {
			return err
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionLabelDeleted, r, "label", cur.ID, c, map[string]any{"name": cur.Name}))
	})
}
