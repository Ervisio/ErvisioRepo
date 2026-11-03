// Package gittier moves repositories between the hot tier (bare
// repositories on the local disk) and the remote pack journal (PLAN §4.3):
// hydration with progress reporting, eviction and pinning.
package gittier

import (
	"sync"

	"github.com/google/uuid"
)

// Hydration phases reported in Event.Phase.
const (
	PhaseStart    = "start"
	PhaseManifest = "manifest"
	PhasePacks    = "packs"
	PhaseRefs     = "refs"
	PhaseIndex    = "index"
	PhaseVerify   = "verify"
	PhaseDone     = "done"
	PhaseFailed   = "failed"
)

// Event is the progress of one hydration.
type Event struct {
	Repo  uuid.UUID `json:"repo"`
	Phase string    `json:"phase"`
	// Done and Total count the pack bytes downloaded so far and overall.
	Done    int64 `json:"done"`
	Total   int64 `json:"total"`
	Percent int   `json:"percent"`
	// State is the storage state once the hydration ended (done or failed).
	State string `json:"state,omitempty"`
	Error string `json:"error,omitempty"`
}

// Terminal reports whether no more events follow for this hydration.
func (e Event) Terminal() bool { return e.Phase == PhaseDone || e.Phase == PhaseFailed }

// Hub fans hydration progress out to subscribers (SSE clients, SSH
// sessions). Slow subscribers only miss intermediate events: each channel
// holds the latest one. It is safe for concurrent use.
type Hub struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[chan Event]struct{}
	last map[uuid.UUID]Event
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: map[uuid.UUID]map[chan Event]struct{}{}, last: map[uuid.UUID]Event{}}
}

// Subscribe returns a channel receiving the events of repo, starting with
// the last one of a hydration in progress, and the function that ends the
// subscription. The channel is never closed.
func (h *Hub) Subscribe(repo uuid.UUID) (<-chan Event, func()) {
	ch := make(chan Event, 1)
	h.mu.Lock()
	set := h.subs[repo]
	if set == nil {
		set = map[chan Event]struct{}{}
		h.subs[repo] = set
	}
	set[ch] = struct{}{}
	if e, ok := h.last[repo]; ok {
		ch <- e
	}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if set := h.subs[repo]; set != nil {
				delete(set, ch)
				if len(set) == 0 {
					delete(h.subs, repo)
				}
			}
		})
	}
}

// Last returns the latest event of a hydration in progress.
func (h *Hub) Last(repo uuid.UUID) (Event, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.last[repo]
	return e, ok
}

// Publish delivers e to every subscriber of e.Repo, replacing an event they
// have not read yet. A terminal event also clears the last event.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e.Terminal() {
		delete(h.last, e.Repo)
	} else {
		h.last[e.Repo] = e
	}
	for ch := range h.subs[e.Repo] {
		for {
			select {
			case ch <- e:
			default:
				select {
				case <-ch: // drop the unread event
				default:
				}
				continue
			}
			break
		}
	}
}
