package webhooks

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/events"
)

// GitHubEvent is one GitHub-shaped payload built for a bus event.
type GitHubEvent struct {
	Event   string // GitHub event name, e.g. "push" or "pull_request"
	Action  string
	Payload []byte // JSON
}

// ErrNoRepo means the repository of the event no longer exists.
var ErrNoRepo = errors.New("webhooks: repository of the event not found")

// GitHubEvents builds the GitHub-shaped payloads of ev, the same that a
// webhook subscribed to every event would receive. Ervisio Actions uses
// them as the "github.event" context of workflow runs.
func (s *Service) GitHubEvents(ctx context.Context, ev events.Event) ([]GitHubEvent, error) {
	b := s.newBuilder(ctx)
	if err := b.loadRepo(ev.RepoID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoRepo
		}
		return nil, err
	}
	items, err := b.items(ev)
	if err != nil {
		return nil, fmt.Errorf("github payloads: %w", err)
	}
	out := make([]GitHubEvent, 0, len(items))
	for _, it := range items {
		out = append(out, GitHubEvent{Event: it.event, Action: it.action, Payload: it.payload})
	}
	return out, nil
}
