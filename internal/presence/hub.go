package presence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Event kinds, as named on the streams.
const (
	KindSnapshot = "snapshot"
	KindPresence = "presence"
	KindGone     = "gone"
	KindLock     = "lock"
	KindUnlock   = "unlock"
	KindDisabled = "disabled"
)

// Notification kinds (the same names).
const (
	kindPresence = KindPresence
	kindGone     = KindGone
	kindLock     = KindLock
	kindUnlock   = KindUnlock
	kindDisabled = KindDisabled
)

// subBuffer is the event backlog of a stream; a stream that falls further
// behind is closed (its client reconnects and gets a new snapshot).
const subBuffer = 256

// note is the payload of a notification: it only names the change, far
// below the 8000-byte limit of NOTIFY; every instance reloads what it needs.
type note struct {
	Repo   uuid.UUID `json:"r"`
	Kind   string    `json:"k"`
	User   uuid.UUID `json:"u"`
	Client uuid.UUID `json:"c"`
	ID     uuid.UUID `json:"i"`
	Login  string    `json:"l,omitempty"`
}

// notify sends n on the presence channel; inside a transaction it is
// delivered at commit.
func notify(ctx context.Context, q *dbgen.Queries, n note) error {
	b, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("encode notification: %w", err)
	}
	if err := q.NotifyPresence(ctx, string(b)); err != nil {
		return fmt.Errorf("notify presence: %w", err)
	}
	return nil
}

// Gone is a presence that ended.
type Gone struct {
	Login    string
	ClientID uuid.UUID
}

// Event is one change delivered to a stream; the field matching Kind is set.
type Event struct {
	Kind     string
	Presence *Presence
	Gone     *Gone
	Lock     *Lock
	LockID   uuid.UUID // KindUnlock
	userID   uuid.UUID // author of a presence or gone event
}

// Subscription receives the events of one repository. Events is closed when
// the stream must end (feature off, stream too slow, events lost).
type Subscription struct {
	RepoID uuid.UUID
	UserID uuid.UUID
	ch     chan Event
	hub    *hub
}

// Events returns the event channel.
func (sub *Subscription) Events() <-chan Event { return sub.ch }

// Close ends the subscription.
func (sub *Subscription) Close() { sub.hub.remove(sub) }

// hub fans the events of this instance out to the subscriptions.
type hub struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[*Subscription]struct{}
}

func newHub() *hub { return &hub{subs: map[uuid.UUID]map[*Subscription]struct{}{}} }

func (h *hub) add(repo, user uuid.UUID) *Subscription {
	sub := &Subscription{RepoID: repo, UserID: user, ch: make(chan Event, subBuffer), hub: h}
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.subs[repo]
	if !ok {
		m = map[*Subscription]struct{}{}
		h.subs[repo] = m
	}
	m[sub] = struct{}{}
	return sub
}

// dropLocked removes sub and closes its channel; h.mu is held.
func (h *hub) dropLocked(sub *Subscription) {
	m := h.subs[sub.RepoID]
	if _, ok := m[sub]; !ok {
		return
	}
	delete(m, sub)
	if len(m) == 0 {
		delete(h.subs, sub.RepoID)
	}
	close(sub.ch)
}

func (h *hub) remove(sub *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dropLocked(sub)
}

func (h *hub) has(repo uuid.UUID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[repo]) > 0
}

// publish delivers ev to the subscriptions of repo, except a user's own
// presence events. A disabled event also ends the subscriptions.
func (h *hub) publish(repo uuid.UUID, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs[repo] {
		if (ev.Kind == KindPresence || ev.Kind == KindGone) && ev.userID == sub.UserID {
			continue
		}
		select {
		case sub.ch <- ev:
			if ev.Kind == KindDisabled {
				h.dropLocked(sub)
			}
		default:
			h.dropLocked(sub)
		}
	}
}

// closeAll ends every subscription (events may have been lost).
func (h *hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range h.subs {
		for sub := range m {
			h.dropLocked(sub)
		}
	}
}

// Subscribe opens a stream on owner/name for a caller that can use the
// feature and returns it with the current state. Close the subscription
// when done.
func (s *Service) Subscribe(ctx context.Context, who auth.Principal, owner, name string) (*Subscription, State, error) {
	a, err := s.lookup(ctx, who, owner, name)
	if err != nil {
		return nil, State{}, err
	}
	if !a.use {
		return nil, State{}, ErrForbidden
	}
	if !a.enabled {
		return nil, State{}, ErrDisabled
	}
	// Subscribe first so that no change between the snapshot and the
	// subscription is lost; a duplicate is harmless.
	sub := s.hub.add(a.repo.ID, who.UserID)
	st, err := s.state(ctx, who, a)
	if err != nil {
		sub.Close()
		return nil, State{}, err
	}
	return sub, st, nil
}

// Check re-checks that the caller of a stream may still use the feature:
// it returns ErrDisabled when the feature was turned off, another error
// when the caller lost access.
func (s *Service) Check(ctx context.Context, who auth.Principal, sub *Subscription) error {
	if s.repos == nil {
		return ErrUnavailable
	}
	r, err := s.repos.GetByID(ctx, who, sub.RepoID)
	if err != nil {
		return err
	}
	a, err := s.accessFor(ctx, who, r)
	if err != nil {
		return err
	}
	if !a.use {
		return ErrForbidden
	}
	if !a.enabled {
		return ErrDisabled
	}
	return nil
}

// Run delivers the notifications of every instance to the streams of this
// one and sweeps expired presences, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	go s.sweep(ctx)
	first := true
	for ctx.Err() == nil {
		err := s.listenOnce(ctx, &first)
		if ctx.Err() != nil {
			return
		}
		s.log.Warn("presence listener", "error", err)
		// Notifications may have been missed: the clients reconnect and
		// get a fresh snapshot.
		s.hub.closeAll()
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *Service) listenOnce(ctx context.Context, first *bool) error {
	c, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	conn := c.Hijack()
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = conn.Close(cctx)
	}()
	if err := dbgen.New(conn).ListenPresence(ctx); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if !*first {
		s.hub.closeAll()
	}
	*first = false
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("wait: %w", err)
		}
		var nt note
		if err := json.Unmarshal([]byte(n.Payload), &nt); err != nil {
			s.log.Warn("presence: bad notification", "error", err)
			continue
		}
		s.dispatch(ctx, nt)
	}
}

// dispatch turns a notification into an event for the local streams.
func (s *Service) dispatch(ctx context.Context, n note) {
	if !s.hub.has(n.Repo) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ev := Event{Kind: n.Kind, userID: n.User}
	switch n.Kind {
	case kindPresence:
		r, err := s.q.GetLivePresence(ctx, dbgen.GetLivePresenceParams{RepoID: n.Repo, UserID: n.User, ClientID: n.Client})
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				s.log.Warn("presence: load presence", "repo", n.Repo, "error", err)
			}
			return
		}
		p := presenceFrom(r)
		ev.Presence = &p
	case kindGone:
		ev.Gone = &Gone{Login: n.Login, ClientID: n.Client}
	case kindLock:
		r, err := s.q.GetPresenceLock(ctx, dbgen.GetPresenceLockParams{RepoID: n.Repo, ID: n.ID})
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				s.log.Warn("presence: load lock", "repo", n.Repo, "error", err)
			}
			return
		}
		l := lockFrom(r.ID, r.Path, r.Refname, r.OwnerID, r.CreatedAt, r.Login, r.DisplayName, r.AvatarUrl)
		ev.Lock = &l
	case kindUnlock:
		ev.LockID = n.ID
	case kindDisabled:
	default:
		return
	}
	s.hub.publish(n.Repo, ev)
}

// sweep deletes the presences that expired and announces them as gone.
// Several instances may sweep: each row is deleted, and announced, once.
func (s *Service) sweep(ctx context.Context) {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := s.sweepOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("presence: sweep", "error", err)
		}
	}
}

func (s *Service) sweepOnce(ctx context.Context) error {
	const batch = 500
	for {
		var n int
		err := s.inTx(ctx, func(q *dbgen.Queries) error {
			rows, err := q.DeleteExpiredPresence(ctx, dbgen.DeleteExpiredPresenceParams{
				Before: pgtype.Timestamptz{Time: s.now().Add(-TTL), Valid: true}, MaxRows: batch})
			if err != nil {
				return fmt.Errorf("delete expired presence: %w", err)
			}
			n = len(rows)
			for _, r := range rows {
				if err := notify(ctx, q, note{Repo: r.RepoID, Kind: kindGone, User: r.UserID, Client: r.ClientID, Login: r.Login}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil || n < batch {
			return err
		}
	}
}
