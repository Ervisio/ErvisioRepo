package gitstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// Errors returned by Store.
var (
	// ErrNoManifest: the repository has no remote copy yet.
	ErrNoManifest = errors.New("gitstore: no manifest")
	// ErrConflict: SaveManifest lost the compare-and-swap race; reload and retry.
	ErrConflict = errors.New("gitstore: manifest changed concurrently")
	// ErrNoConditionalPut: the git primary cannot do compare-and-swap writes.
	ErrNoConditionalPut = errors.New("gitstore: git primary does not support conditional writes")
)

// Store reads and writes the remote pack journal of every repository.
// It is safe for concurrent use; serializing writers of one repository is
// the caller's job (Tier.Acquire in Write mode), the CAS on the manifest
// only detects a violation.
type Store struct {
	class   *tier.Class
	primary blob.Store
	cas     blob.ConditionalPutter
}

// NewStore binds the store to the "git" class of t. The class primary must
// implement blob.ConditionalPutter (local and s3 do; sftp does not).
func NewStore(t *tier.Tier) (*Store, error) {
	c, err := t.Class(Policy)
	if err != nil {
		return nil, fmt.Errorf("gitstore: %w", err)
	}
	return newStore(c)
}

func newStore(c *tier.Class) (*Store, error) {
	p := c.Primary().Store
	cp, ok := p.(blob.ConditionalPutter)
	if !ok || !p.Caps().ConditionalPut {
		return nil, fmt.Errorf("%w (target %s)", ErrNoConditionalPut, c.Primary().Name)
	}
	return &Store{class: c, primary: p, cas: cp}, nil
}

// Class is the underlying storage class, for pack and snapshot transfers
// that need more than the helpers below (ranges, Stat, EvictCached).
func (s *Store) Class() *tier.Class { return s.class }

// LoadManifest reads the current manifest and validates it. The returned
// manifest carries the ETag to pass back to SaveManifest. A missing
// manifest is ErrNoManifest; an unreadable one wraps ErrInvalidManifest
// (the repository should go to the degraded state).
func (s *Store) LoadManifest(ctx context.Context, repo uuid.UUID) (*Manifest, error) {
	key := s.class.FullKey(ManifestKey(repo))
	rc, info, err := s.primary.Get(ctx, key, nil)
	if errors.Is(err, blob.ErrNotFound) {
		return nil, fmt.Errorf("%s: %w", repo, ErrNoManifest)
	}
	if err != nil {
		return nil, fmt.Errorf("load manifest %s: %w", repo, err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, MaxManifestSize+1))
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", repo, err)
	}
	if len(data) > MaxManifestSize {
		return nil, fmt.Errorf("%w: %s larger than %d bytes", ErrInvalidManifest, repo, MaxManifestSize)
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalidManifest, repo, err)
	}
	if m.Segments == nil {
		m.Segments = []Segment{}
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", repo, err)
	}
	if m.Repo != repo {
		return nil, fmt.Errorf("%w: %s holds the manifest of %s", ErrInvalidManifest, repo, m.Repo)
	}
	m.ETag = info.ETag
	return &m, nil
}

// SaveManifest writes m if the stored manifest still has m.ETag (or does not
// exist, when m.ETag is empty). On success m.ETag is updated; when another
// writer got there first it returns ErrConflict and m is unchanged.
//
// Every pack and snapshot the manifest references must already be stored:
// the manifest is the commit point of a push.
func (s *Store) SaveManifest(ctx context.Context, m *Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	data = append(data, '\n')
	cond := blob.Condition{IfMatch: m.ETag, IfNoneMatch: m.ETag == ""}
	sum := sha256.Sum256(data)
	info, err := s.cas.PutConditional(ctx, s.class.FullKey(ManifestKey(m.Repo)), bytes.NewReader(data), int64(len(data)),
		cond, blob.PutOpts{SHA256: fmt.Sprintf("%x", sum), ContentType: "application/json"})
	if errors.Is(err, blob.ErrPreconditionFailed) {
		return fmt.Errorf("save manifest %s: %w", m.Repo, ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("save manifest %s: %w", m.Repo, err)
	}
	m.ETag = info.ETag
	return nil
}

// PutObject stores an immutable journal object (pack, index or snapshot)
// under a class-relative key built with BaseKey, SegmentKey or RefsKey.
// sha256 may be nil; size must be known.
func (s *Store) PutObject(ctx context.Context, key string, r io.Reader, size int64, sha256 []byte) (tier.Object, error) {
	k, err := ParseKey(key)
	if err != nil {
		return tier.Object{}, err
	}
	if k.Kind == KindManifest {
		return tier.Object{}, fmt.Errorf("%w: use SaveManifest for %q", ErrInvalidKey, key)
	}
	return s.class.Put(ctx, key, r, size, sha256)
}

// OpenObject opens a journal object, or the part selected by rng.
func (s *Store) OpenObject(ctx context.Context, key string, rng *blob.Range) (io.ReadCloser, tier.Object, error) {
	k, err := ParseKey(key)
	if err != nil {
		return nil, tier.Object{}, err
	}
	if k.Kind == KindManifest {
		return nil, tier.Object{}, fmt.Errorf("%w: use LoadManifest for %q", ErrInvalidKey, key)
	}
	return s.class.Get(ctx, key, rng)
}

// MaxRefsSize bounds what LoadRefs reads.
const MaxRefsSize = 256 << 20

// PutRefs stores a ref snapshot under RefsKey(repo, snap.Seq).
func (s *Store) PutRefs(ctx context.Context, repo uuid.UUID, snap *RefsSnapshot) error {
	if err := snap.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("encode refs: %w", err)
	}
	sum := sha256.Sum256(data)
	_, err = s.class.Put(ctx, RefsKey(repo, snap.Seq), bytes.NewReader(data), int64(len(data)), sum[:])
	return err
}

// LoadRefs reads and validates the ref snapshot of seq.
func (s *Store) LoadRefs(ctx context.Context, repo uuid.UUID, seq int64) (*RefsSnapshot, error) {
	rc, _, err := s.class.Get(ctx, RefsKey(repo, seq), nil)
	if err != nil {
		return nil, fmt.Errorf("load refs %s/%d: %w", repo, seq, err)
	}
	defer func() { _ = rc.Close() }()
	var snap RefsSnapshot
	dec := json.NewDecoder(io.LimitReader(rc, MaxRefsSize))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&snap); err != nil {
		return nil, fmt.Errorf("%w: refs %s/%d: %v", ErrInvalidManifest, repo, seq, err)
	}
	if err := snap.Validate(); err != nil {
		return nil, err
	}
	if snap.Seq != seq {
		return nil, fmt.Errorf("%w: refs %s/%d holds seq %d", ErrInvalidManifest, repo, seq, snap.Seq)
	}
	return &snap, nil
}

// DeleteRepo removes every remote object of a repository: the manifest
// first (so a half-deleted repository is never hydrated), then the packs
// and snapshots with their storage_objects rows. It is idempotent and meant
// for the purge job that runs after a repository is deleted.
func (s *Store) DeleteRepo(ctx context.Context, repo uuid.UUID) error {
	if err := s.primary.Delete(ctx, s.class.FullKey(ManifestKey(repo))); err != nil {
		return fmt.Errorf("delete manifest %s: %w", repo, err)
	}
	for _, r := range s.class.Replicas() {
		if err := r.Store.Delete(ctx, s.class.FullKey(ManifestKey(repo))); err != nil {
			return fmt.Errorf("delete manifest %s from %s: %w", repo, r.Name, err)
		}
	}
	prefix := s.class.FullKey(RepoPrefix(repo))
	var keys []string
	if err := s.primary.List(ctx, prefix, func(i blob.Info) error {
		keys = append(keys, strings.TrimPrefix(i.Key, Policy+"/"))
		return nil
	}); err != nil {
		return fmt.Errorf("list %s: %w", prefix, err)
	}
	var errs []error
	for _, k := range keys {
		if err := s.class.Delete(ctx, k); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
