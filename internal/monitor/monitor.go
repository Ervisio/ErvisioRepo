// Package monitor keeps the short in-memory histories behind the admin
// overview: requests per minute and samples of the local disk. Nothing is
// persisted, so the histories start empty after a restart.
package monitor

import (
	"context"
	"sync"
	"time"
)

const (
	// minutesKept covers the longest range of the overview (7 days).
	minutesKept = 7 * 24 * 60
	// DiskEvery is the disk sampling period.
	DiskEvery = 10 * time.Minute
	diskKept  = int(7 * 24 * time.Hour / DiskEvery)
)

// DiskSample is the used and total space of the local disk at one time.
type DiskSample struct {
	At          time.Time
	Used, Total int64
}

// Monitor is safe for concurrent use.
type Monitor struct {
	started time.Time
	now     func() time.Time

	mu sync.Mutex
	// counts[i] holds the requests of the unix minute stamps[i]; a slot is
	// reused when its minute is minutesKept old.
	counts [minutesKept]int64
	stamps [minutesKept]int64
	disk   []DiskSample // ring of at most diskKept, oldest at head once full
	head   int
}

// New starts the histories now.
func New() *Monitor {
	return &Monitor{started: time.Now(), now: time.Now}
}

// Started is when the histories began (the server start).
func (m *Monitor) Started() time.Time { return m.started }

// CountRequest records one HTTP request.
func (m *Monitor) CountRequest() {
	minute := m.now().Unix() / 60
	i := minute % minutesKept
	m.mu.Lock()
	if m.stamps[i] != minute {
		m.stamps[i], m.counts[i] = minute, 0
	}
	m.counts[i]++
	m.mu.Unlock()
}

// count returns the requests of a unix minute, 0 when not recorded.
func (m *Monitor) count(minute int64) int64 {
	i := minute % minutesKept
	if m.stamps[i] != minute {
		return 0
	}
	return m.counts[i]
}

// LastMinute returns the requests of the last complete minute.
func (m *Monitor) LastMinute() int64 {
	minute := m.now().Unix()/60 - 1
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count(minute)
}

// RequestRate returns, for n buckets of step from start, the mean requests
// per minute over the complete minutes of each bucket the server was up
// for (0 for a bucket without any).
func (m *Monitor) RequestRate(start time.Time, step time.Duration, n int) []float64 {
	out := make([]float64, n)
	perBucket := int64(step / time.Minute)
	first := start.Unix() / 60
	from := (m.started.Unix() + 59) / 60 // first complete minute after the start
	current := m.now().Unix() / 60
	m.mu.Lock()
	defer m.mu.Unlock()
	for b := range n {
		var sum, minutes int64
		for k := range perBucket {
			minute := first + int64(b)*perBucket + k
			if minute < from || minute >= current {
				continue
			}
			sum += m.count(minute)
			minutes++
		}
		if minutes > 0 {
			out[b] = float64(sum) / float64(minutes)
		}
	}
	return out
}

// SampleDisk records sample() now and then every DiskEvery until ctx ends.
// Failed samples (ok false) are skipped.
func (m *Monitor) SampleDisk(ctx context.Context, sample func() (used, total int64, ok bool)) {
	tick := time.NewTicker(DiskEvery)
	defer tick.Stop()
	for {
		if used, total, ok := sample(); ok {
			m.addDisk(DiskSample{At: m.now(), Used: used, Total: total})
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (m *Monitor) addDisk(s DiskSample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.disk) < diskKept {
		m.disk = append(m.disk, s)
		return
	}
	m.disk[m.head] = s
	m.head = (m.head + 1) % diskKept
}

// Disk returns the samples taken since the given time, oldest first.
func (m *Monitor) Disk(since time.Time) []DiskSample {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]DiskSample, 0, len(m.disk))
	for i := range m.disk {
		s := m.disk[(m.head+i)%len(m.disk)]
		if !s.At.Before(since) {
			out = append(out, s)
		}
	}
	return out
}
