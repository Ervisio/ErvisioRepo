package dashboard

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
)

// EventKinds are the bus kinds the activity feed records.
var EventKinds = []string{events.KindPush, events.KindIssue, events.KindIssueComment, events.KindPull,
	events.KindRelease}

// activity kinds as stored in activity_events.kind.
const (
	kindPush    = "push"
	kindIssue   = "issue"
	kindPull    = "pull_request"
	kindRelease = "release"
	kindComment = "comment"
)

type eventPayload struct {
	Number    int64  `json:"number"`
	IssueKind string `json:"issue_kind"`
	Title     string `json:"title"`
	Tag       string `json:"tag"`
	Name      string `json:"name"`
}

// EventHandler records the notable bus events in activity_events. A repeated
// delivery changes nothing (the row keeps the event ID).
func (s *Service) EventHandler() events.Handler { return events.HandlerFunc(s.record) }

func (s *Service) record(ctx context.Context, ev events.Event) error {
	if ev.RepoID == uuid.Nil {
		return nil
	}
	var p eventPayload
	if err := ev.Decode(&p); err != nil {
		return err
	}
	kind, number, title, ok := classify(ev, p)
	if !ok {
		return nil
	}
	err := s.q.InsertActivityEvent(ctx, dbgen.InsertActivityEventParams{ID: ev.ID, RepoID: ev.RepoID,
		ActorID: uuid.NullUUID{UUID: ev.ActorID, Valid: ev.ActorID != uuid.Nil}, Kind: kind, Action: ev.Action,
		Number: number, Title: title, CreatedAt: pgtype.Timestamptz{Time: ev.CreatedAt, Valid: true}})
	if err != nil {
		return fmt.Errorf("record activity of event %s: %w", ev.ID, err)
	}
	return nil
}

// classify maps an event to an activity entry; false means it is not shown.
func classify(ev events.Event, p eventPayload) (kind string, number int64, title string, ok bool) {
	switch ev.Kind {
	case events.KindPush:
		var pp events.PushPayload
		if err := ev.Decode(&pp); err != nil || len(pp.Updates) == 0 {
			return "", 0, "", false
		}
		return kindPush, 0, strings.TrimPrefix(pp.Updates[0].Ref, "refs/heads/"), true
	case events.KindIssue:
		if p.IssueKind == "pull" { // the pull request events cover these
			return "", 0, "", false
		}
		switch ev.Action {
		case "opened", "closed", "reopened":
			return kindIssue, p.Number, p.Title, true
		}
	case events.KindPull:
		switch ev.Action {
		case "opened", "closed", "reopened", "merged":
			return kindPull, p.Number, p.Title, true
		}
	case events.KindIssueComment:
		if ev.Action == "created" {
			return kindComment, p.Number, p.Title, true
		}
	case events.KindRelease:
		if ev.Action == "published" {
			tag := p.Tag
			if tag == "" {
				tag = p.Name
			}
			return kindRelease, 0, tag, true
		}
	}
	return "", 0, "", false
}
