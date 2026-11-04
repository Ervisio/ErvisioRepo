package actions

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// hub fans the Postgres notifications of one listening connection out to
// the runners polling for tasks and the clients following logs.
type hub struct {
	mu    sync.Mutex
	tasks chan struct{}
	logs  map[uuid.UUID]chan struct{}
}

func newHub() *hub {
	return &hub{tasks: make(chan struct{}), logs: map[uuid.UUID]chan struct{}{}}
}

// tasksChanged is closed at the next tasks notification.
func (h *hub) tasksChanged() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tasks
}

// logChanged is closed at the next log notification of a job.
func (h *hub) logChanged(job uuid.UUID) <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.logs[job]
	if !ok {
		c = make(chan struct{})
		h.logs[job] = c
	}
	return c
}

func (h *hub) fireTasks() {
	h.mu.Lock()
	defer h.mu.Unlock()
	close(h.tasks)
	h.tasks = make(chan struct{})
}

func (h *hub) fireLog(job uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.logs[job]; ok {
		close(c)
		delete(h.logs, job)
	}
}

// fireAll wakes everybody, after a reconnection may have lost
// notifications.
func (h *hub) fireAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	close(h.tasks)
	h.tasks = make(chan struct{})
	for id, c := range h.logs {
		close(c)
		delete(h.logs, id)
	}
}

// Listen keeps one connection listening to the Actions channels until ctx
// ends. Without it, polling falls back to short sleeps.
func (s *Service) Listen(ctx context.Context) {
	s.listening.Store(true)
	defer s.listening.Store(false)
	for ctx.Err() == nil {
		if err := s.listenOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("actions listener", "err", err)
		}
		s.hub.fireAll()
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *Service) listenOnce(ctx context.Context) error {
	c, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	conn := c.Hijack()
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(cctx)
	}()
	q := dbgen.New(conn)
	if err := q.ListenActionsTasks(ctx); err != nil {
		return err
	}
	if err := q.ListenActionsLogs(ctx); err != nil {
		return err
	}
	s.hub.fireAll()
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		switch n.Channel {
		case tasksChannel:
			s.hub.fireTasks()
		case logChannel:
			id, _, _ := strings.Cut(n.Payload, ":")
			if job, err := uuid.Parse(id); err == nil {
				s.hub.fireLog(job)
			}
		}
	}
}

// wait blocks until ch is closed, d elapses or ctx ends. Without a
// listener it waits at most a second so that polling stays responsive.
func (s *Service) wait(ctx context.Context, ch <-chan struct{}, d time.Duration) {
	if !s.listening.Load() {
		d = min(d, time.Second)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-ch:
	case <-t.C:
	}
}
