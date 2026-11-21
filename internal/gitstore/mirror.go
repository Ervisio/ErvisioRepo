package gitstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// mirrorTimeout bounds one copy of a manifest to a replica.
const mirrorTimeout = 2 * time.Minute

// manifestMirror copies every saved manifest to the replicas of the git
// class, in the background. The manifest is mutable and written with a
// conditional put on the primary only, so the replication job (which
// copies immutable, content-addressed objects) never sees it; without this
// copy a replica (the Storage Box backup, say) would hold every pack and
// snapshot but no manifest, and could not be restored from. The newest
// manifest of a repository wins: a copy in flight is followed by the last
// one saved meanwhile, never by an older one.
type manifestMirror struct {
	mu      sync.Mutex
	pending map[string][]byte // key -> newest content not copied yet
	running map[string]bool
}

func (s *Store) mirrorManifest(key string, data []byte) {
	reps := s.class.Replicas()
	if len(reps) == 0 {
		return
	}
	m := &s.mirror
	m.mu.Lock()
	if m.pending == nil {
		m.pending, m.running = map[string][]byte{}, map[string]bool{}
	}
	m.pending[key] = data
	if m.running[key] {
		m.mu.Unlock()
		return
	}
	m.running[key] = true
	m.mu.Unlock()
	go func() {
		for {
			m.mu.Lock()
			d, ok := m.pending[key]
			delete(m.pending, key)
			if !ok {
				delete(m.running, key)
				m.mu.Unlock()
				return
			}
			m.mu.Unlock()
			sum := sha256.Sum256(d)
			for _, r := range reps {
				ctx, cancel := context.WithTimeout(context.Background(), mirrorTimeout)
				_, err := r.Store.Put(ctx, key, bytes.NewReader(d), int64(len(d)),
					blob.PutOpts{SHA256: fmt.Sprintf("%x", sum), ContentType: "application/json"})
				cancel()
				if err != nil && s.log != nil {
					s.log.Warn("copy git manifest to replica", "target", r.Name, "key", key, "error", err)
				}
			}
		}
	}()
}

// MirrorManifests copies the current manifest of every repository from the
// primary to the replicas (a replica added to the git policy, or a full
// sync asked by an admin). It returns how many were copied.
func (s *Store) MirrorManifests(ctx context.Context) (int, error) {
	reps := s.class.Replicas()
	if len(reps) == 0 {
		return 0, nil
	}
	root := s.class.FullKey("")
	var keys []string
	if err := s.primary.List(ctx, root, func(i blob.Info) error {
		if k, err := ParseKey(strings.TrimPrefix(i.Key, root)); err == nil && k.Kind == KindManifest {
			keys = append(keys, i.Key)
		}
		return nil
	}); err != nil {
		return 0, fmt.Errorf("list manifests: %w", err)
	}
	n := 0
	for _, key := range keys {
		rc, _, err := s.primary.Get(ctx, key, nil)
		if err != nil {
			continue // deleted meanwhile
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxManifestMirror))
		_ = rc.Close()
		if err != nil {
			return n, fmt.Errorf("read %s: %w", key, err)
		}
		sum := sha256.Sum256(data)
		for _, r := range reps {
			if _, err := r.Store.Put(ctx, key, bytes.NewReader(data), int64(len(data)),
				blob.PutOpts{SHA256: fmt.Sprintf("%x", sum), ContentType: "application/json"}); err != nil {
				return n, fmt.Errorf("copy %s to %s: %w", key, r.Name, err)
			}
		}
		n++
	}
	return n, nil
}

// maxManifestMirror bounds a manifest read for copying.
const maxManifestMirror = 64 << 20
