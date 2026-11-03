package webhooks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
)

// EventKinds are the bus kinds delivered to hooks.
var EventKinds = []string{
	events.KindIssue, events.KindIssueComment, events.KindLabel, events.KindMilestone, events.KindPush,
	events.KindPull, events.KindPullReview, events.KindPullReviewComment, events.KindRelease, events.KindStatus,
	events.KindCheckRun, events.KindCheckSuite,
}

// Namespaces of the delivery ids and GUIDs derived from an event.
var (
	nsDelivery = uuid.MustParse("2f6c1a3e-6b0e-4d0c-9a51-6f3f0e7d9b11")
	nsGUID     = uuid.MustParse("8d0b6f5a-1c2e-4f7a-b3d4-0e9a8c7b6d22")
)

// nativeSeq is the sequence number of the one ervisio delivery of an event.
const nativeSeq = 255

// EventHandler queues the deliveries of a bus event to the active hooks of
// its repository and of the organization owning it. Delivery ids derive
// from the event, so a repeated run queues nothing new.
func (s *Service) EventHandler() events.Handler { return events.HandlerFunc(s.handle) }

func subscribed(list []string, event string) bool {
	return event != "" && (slices.Contains(list, "*") || slices.Contains(list, event))
}

func (s *Service) handle(ctx context.Context, ev events.Event) error {
	if ev.RepoID == uuid.Nil {
		return nil
	}
	hooks, err := s.q.ActiveWebhooksForRepo(ctx, uuid.NullUUID{UUID: ev.RepoID, Valid: true})
	if err != nil {
		return fmt.Errorf("active webhooks: %w", err)
	}
	if len(hooks) == 0 {
		return nil
	}
	b := s.newBuilder(ctx)
	if err := b.loadRepo(ev.RepoID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // repository deleted since
		}
		return err
	}
	name := githubEvent(ev)
	var gh []item
	var ghBuilt bool
	var native []byte
	for _, h := range hooks {
		if h.Format == FormatErvisio {
			if !subscribed(h.Events, name) {
				continue
			}
			if native == nil {
				if native, err = b.native(ev); err != nil {
					return err
				}
			}
			if err := s.queue(ctx, h, ev, nativeSeq, name, ev.Action, native); err != nil {
				return err
			}
			continue
		}
		if !ghBuilt {
			if gh, err = b.items(ev); err != nil {
				return err
			}
			ghBuilt = true
		}
		for i, it := range gh {
			if i >= nativeSeq {
				break
			}
			if !subscribed(h.Events, it.event) {
				continue
			}
			if err := s.queue(ctx, h, ev, i, it.event, it.action, it.payload); err != nil {
				return err
			}
		}
	}
	return nil
}

// queue records one delivery of an event and enqueues it. A delivery that
// exists already is enqueued again only if it never ran (the previous run
// may have stopped between the insert and the enqueue; the job itself is
// unique by delivery).
func (s *Service) queue(ctx context.Context, h dbgen.Webhook, ev events.Event, seq int, event, action string, payload []byte) error {
	seed := make([]byte, 0, 36)
	seed = append(seed, ev.ID[:]...)
	seed = append(seed, h.ID[:]...)
	seed = strconv.AppendInt(seed, int64(seq), 10)
	id := uuid.NewSHA1(nsDelivery, seed)
	_, err := s.q.InsertWebhookDelivery(ctx, dbgen.InsertWebhookDeliveryParams{ID: id, WebhookID: h.ID,
		EventID: uuid.NullUUID{UUID: ev.ID, Valid: true}, Guid: uuid.NewSHA1(nsGUID, seed), Event: event,
		Action: action, Payload: payload})
	if errors.Is(err, pgx.ErrNoRows) {
		d, err := s.q.GetWebhookDelivery(ctx, id)
		if err != nil {
			return fmt.Errorf("get delivery: %w", err)
		}
		if d.Status != StatusPending || d.Attempts > 0 {
			return nil
		}
	} else if err != nil {
		return fmt.Errorf("insert delivery: %w", err)
	}
	return s.enqueue(ctx, id)
}
