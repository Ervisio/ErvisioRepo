// Package events is the internal event bus (PLAN §5). Producers write an
// Event into the events_outbox table inside the same transaction as the
// change it describes (Emit), so an event exists if and only if the change
// committed. A River drain job moves pending outbox rows to one delivery job
// per registered Handler interested in the event kind; each delivery job is
// retried on its own, so a failing handler never blocks or replays the
// others. Delivery is at least once: handlers must be idempotent.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Event kinds. Actions are listed next to the kind that uses them.
const (
	// KindIssue: opened, edited, closed, reopened, labeled, unlabeled,
	// assigned, unassigned, milestoned, demilestoned, locked, unlocked.
	KindIssue = "issue"
	// KindIssueComment: created, edited, deleted.
	KindIssueComment = "issue_comment"
	// KindLabel: created, edited, deleted.
	KindLabel = "label"
	// KindMilestone: created, edited, closed, opened, deleted.
	KindMilestone = "milestone"
	// KindPush: pushed (one event per receive-pack, with every ref update).
	KindPush = "push"
	// KindPull: opened, edited, closed, reopened, synchronize, merged,
	// labeled, unlabeled, assigned, unassigned, review_requested,
	// ready_for_review, converted_to_draft.
	KindPull = "pull_request"
	// KindPullReview: submitted, edited, dismissed.
	KindPullReview = "pull_request_review"
	// KindPullReviewComment: created, edited, deleted.
	KindPullReviewComment = "pull_request_review_comment"
	// KindRelease: published, edited, deleted.
	KindRelease = "release"
	// KindStatus: created (commit status or check run state change).
	KindStatus = "status"
)

// Event is one fact recorded by a producer. RepoID, ActorID and SubjectID
// may be uuid.Nil (the subject is the issue, comment, label... the event is
// about). Payload is a JSON object whose shape depends on Kind.
type Event struct {
	ID        uuid.UUID
	Kind      string
	Action    string
	RepoID    uuid.UUID
	ActorID   uuid.UUID
	SubjectID uuid.UUID
	Payload   json.RawMessage
	CreatedAt time.Time
}

// Decode unmarshals the payload into v.
func (e Event) Decode(v any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(e.Payload, v); err != nil {
		return fmt.Errorf("decode %s event payload: %w", e.Kind, err)
	}
	return nil
}

// Handler consumes events. An error makes the delivery job retry with
// backoff (up to DeliverMaxAttempts); the event is the same on every try.
type Handler interface {
	Handle(ctx context.Context, ev Event) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, ev Event) error

// Handle calls f.
func (f HandlerFunc) Handle(ctx context.Context, ev Event) error { return f(ctx, ev) }

// RefUpdate is one ref changed by a push. Old is all zeros for a created
// ref, New for a deleted one.
type RefUpdate struct {
	Ref string `json:"ref"`
	Old string `json:"old"`
	New string `json:"new"`
}

// PushPayload is the payload of KindPush events.
type PushPayload struct {
	Updates []RefUpdate `json:"updates"`
}

func nullID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil} }

// Emit records ev in the outbox through q, which should be bound to the
// transaction of the change. A nil payload keeps ev.Payload (an empty
// object when unset); otherwise payload is marshalled to JSON. ev.ID is
// generated when unset. Call Bus.Kick after the commit for a prompt
// delivery; the periodic drain delivers it anyway.
func Emit(ctx context.Context, q *dbgen.Queries, ev Event, payload any) error {
	if ev.Kind == "" || ev.Action == "" {
		return fmt.Errorf("emit event: kind and action are required")
	}
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("emit %s event: marshal payload: %w", ev.Kind, err)
		}
		ev.Payload = b
	}
	if len(ev.Payload) == 0 {
		ev.Payload = json.RawMessage(`{}`)
	}
	if ev.ID == uuid.Nil {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("emit %s event: id: %w", ev.Kind, err)
		}
		ev.ID = id
	}
	if err := q.InsertOutboxEvent(ctx, dbgen.InsertOutboxEventParams{ID: ev.ID, Kind: ev.Kind, Action: ev.Action,
		RepoID: nullID(ev.RepoID), ActorID: nullID(ev.ActorID), SubjectID: nullID(ev.SubjectID),
		Payload: ev.Payload}); err != nil {
		return fmt.Errorf("emit %s event: %w", ev.Kind, err)
	}
	return nil
}
