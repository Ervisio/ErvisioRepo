package gitstore

import (
	"context"
	"sync"
)

// rwlock is a context-aware readers/writer lock. A waiting exclusive locker
// blocks new readers, so a steady stream of reads cannot starve eviction.
type rwlock struct {
	mu        sync.Mutex
	readers   int
	writer    bool
	pendingEx int
	wake      chan struct{} // closed and replaced on every release
}

func (l *rwlock) lock(ctx context.Context, exclusive bool) error {
	l.mu.Lock()
	if l.wake == nil {
		l.wake = make(chan struct{})
	}
	if exclusive {
		l.pendingEx++
	}
	for {
		if exclusive && !l.writer && l.readers == 0 {
			l.pendingEx--
			l.writer = true
			l.mu.Unlock()
			return nil
		}
		if !exclusive && !l.writer && l.pendingEx == 0 {
			l.readers++
			l.mu.Unlock()
			return nil
		}
		wake := l.wake
		l.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			l.mu.Lock()
			if exclusive {
				l.pendingEx--
				l.broadcastLocked() // readers held back by us may go
			}
			l.mu.Unlock()
			return ctx.Err()
		}
		l.mu.Lock()
	}
}

func (l *rwlock) unlock(exclusive bool) {
	l.mu.Lock()
	if exclusive {
		l.writer = false
	} else {
		l.readers--
	}
	l.broadcastLocked()
	l.mu.Unlock()
}

func (l *rwlock) broadcastLocked() {
	if l.wake != nil {
		close(l.wake)
	}
	l.wake = make(chan struct{})
}

// mutex is a context-aware mutex.
type mutex chan struct{}

func newMutex() mutex { return make(mutex, 1) }

func (m mutex) lock(ctx context.Context) error {
	select {
	case m <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m mutex) unlock() { <-m }
