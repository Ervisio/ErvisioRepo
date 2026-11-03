package auth

import (
	"sync"
	"time"
)

// LimiterConfig tunes the brute-force protection.
type LimiterConfig struct {
	// FreeFailuresAccount and FreeFailuresIP are the failures allowed
	// before the backoff starts, per account and per client IP.
	FreeFailuresAccount int
	FreeFailuresIP      int
	// BackoffBase doubles at every further failure, up to BackoffMax.
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// ActionsPerHour bounds the unauthenticated actions that send mail or
	// create accounts (register, password reset, resend), per client IP.
	ActionsPerHour int
}

// Limiter keys: one per account and one per client IP.
const (
	keyAccount = "acct:"
	keyIP      = "ip:"
)

// maxLimiterEntries bounds the memory of the limiter; when it is reached
// stale entries are dropped first, then the whole table.
const maxLimiterEntries = 100_000

// Limiter slows down repeated authentication failures with an exponential
// backoff, per account and per client IP, and rate-limits mail-sending
// actions per IP. State is in memory: it resets on restart and is per
// process (PLAN §8.1).
type Limiter struct {
	cfg LimiterConfig
	now func() time.Time

	mu       sync.Mutex
	failures map[string]*failure
	actions  map[string]*bucket
}

type failure struct {
	count int
	until time.Time // no attempt before this instant
	last  time.Time
}

type bucket struct {
	start time.Time
	count int
}

// NewLimiter returns a Limiter. now may be nil (time.Now).
func NewLimiter(cfg LimiterConfig, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = time.Second
	}
	if cfg.BackoffMax < cfg.BackoffBase {
		cfg.BackoffMax = cfg.BackoffBase
	}
	return &Limiter{cfg: cfg, now: now, failures: map[string]*failure{}, actions: map[string]*bucket{}}
}

// AccountKey and IPKey build the keys passed to the limiter.
func AccountKey(login string) string { return keyAccount + login }

// IPKey is the limiter key of a client IP.
func IPKey(ip string) string { return keyIP + ip }

// LoginWait reports how long the caller must wait before the next login
// attempt for the given keys; zero means the attempt may proceed.
func (l *Limiter) LoginWait(keys ...string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var wait time.Duration
	for _, k := range keys {
		if f := l.failures[k]; f != nil && f.until.After(now) {
			wait = max(wait, f.until.Sub(now))
		}
	}
	return wait
}

// Fail records a failed attempt on every key.
func (l *Limiter) Fail(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	for _, k := range keys {
		f := l.failures[k]
		if f == nil {
			f = &failure{}
			l.failures[k] = f
		} else if now.Sub(f.last) > l.cfg.BackoffMax*4 {
			*f = failure{} // forgive old failures
		}
		f.count++
		f.last = now
		free := l.cfg.FreeFailuresIP
		if len(k) > len(keyAccount) && k[:len(keyAccount)] == keyAccount {
			free = l.cfg.FreeFailuresAccount
		}
		if over := f.count - free; over > 0 {
			f.until = now.Add(l.backoff(over))
		}
	}
}

func (l *Limiter) backoff(over int) time.Duration {
	d := l.cfg.BackoffBase
	for i := 1; i < over && d < l.cfg.BackoffMax; i++ {
		d *= 2
	}
	return min(d, l.cfg.BackoffMax)
}

// Reset forgets the failures of key, after a successful login.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// AllowAction counts one mail-sending action for key and reports whether it
// is within the hourly budget, or how long to wait otherwise.
func (l *Limiter) AllowAction(key string) (bool, time.Duration) {
	if l.cfg.ActionsPerHour <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	b := l.actions[key]
	if b == nil || now.Sub(b.start) >= time.Hour {
		b = &bucket{start: now}
		l.actions[key] = b
	}
	if b.count >= l.cfg.ActionsPerHour {
		return false, b.start.Add(time.Hour).Sub(now)
	}
	b.count++
	return true, 0
}

// gc drops stale entries once the tables grow large. Called with mu held.
func (l *Limiter) gc(now time.Time) {
	if len(l.failures)+len(l.actions) < maxLimiterEntries {
		return
	}
	for k, f := range l.failures {
		if now.Sub(f.last) > l.cfg.BackoffMax*4 {
			delete(l.failures, k)
		}
	}
	for k, b := range l.actions {
		if now.Sub(b.start) >= time.Hour {
			delete(l.actions, k)
		}
	}
	if len(l.failures)+len(l.actions) >= maxLimiterEntries {
		l.failures, l.actions = map[string]*failure{}, map[string]*bucket{}
	}
}
