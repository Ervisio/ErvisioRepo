package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Page sizes of List.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Lister is the subset of dbgen.Querier used to read events.
type Lister interface {
	ListAuditEvents(ctx context.Context, arg dbgen.ListAuditEventsParams) ([]dbgen.ListAuditEventsRow, error)
}

// Filter selects audit entries; zero fields match everything.
type Filter struct {
	// Before is the cursor: only entries older than this id.
	Before uuid.UUID
	Actor  uuid.UUID
	OrgID  uuid.UUID
	// Action matches exactly, or as a prefix when it ends with ".".
	Action string
	// Limit is clamped to [1, MaxLimit]; zero means DefaultLimit.
	Limit int
}

// Entry is one recorded event.
type Entry struct {
	ID         uuid.UUID
	At         time.Time
	Actor      uuid.UUID // zero for anonymous callers
	ActorName  string    // empty when anonymous or deleted
	Action     string
	TargetType string
	TargetID   string
	IP         string
	Data       map[string]any
	OrgID      uuid.UUID
}

// Page is a page of entries, newest first. Next is the cursor of the next
// page, zero when this is the last one.
type Page struct {
	Entries []Entry
	Next    uuid.UUID
}

func nullUUID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil} }

// List returns a page of entries matching f.
func List(ctx context.Context, q Lister, f Filter) (Page, error) {
	limit := f.Limit
	switch {
	case limit <= 0:
		limit = DefaultLimit
	case limit > MaxLimit:
		limit = MaxLimit
	}
	rows, err := q.ListAuditEvents(ctx, dbgen.ListAuditEventsParams{
		Before: nullUUID(f.Before), Actor: nullUUID(f.Actor), Org: nullUUID(f.OrgID),
		Action: pgtype.Text{String: f.Action, Valid: f.Action != ""}, MaxRows: int32(limit + 1), //nolint:gosec // limit ≤ MaxLimit
	})
	if err != nil {
		return Page{}, fmt.Errorf("list audit events: %w", err)
	}
	var p Page
	if len(rows) > limit {
		rows = rows[:limit]
		p.Next = rows[limit-1].ID
	}
	p.Entries = make([]Entry, 0, len(rows))
	for _, r := range rows {
		e := Entry{ID: r.ID, At: r.At.Time, Actor: r.ActorID.UUID, ActorName: r.ActorName.String,
			Action: r.Action, TargetType: r.TargetType, TargetID: r.TargetID, IP: r.Ip, OrgID: r.OrgID.UUID}
		if len(r.Data) > 0 {
			if err := json.Unmarshal(r.Data, &e.Data); err != nil {
				return Page{}, fmt.Errorf("decode audit data %s: %w", r.ID, err)
			}
		}
		p.Entries = append(p.Entries, e)
	}
	return p, nil
}
