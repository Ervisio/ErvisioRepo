package gitstore

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// ManifestVersion is the format version written by this code.
const ManifestVersion = 1

// MaxManifestSize bounds what LoadManifest reads.
const MaxManifestSize = 16 << 20

// Base is the base pack of a generation.
type Base struct {
	Gen       int64  `json:"gen"`
	SHA       string `json:"sha"`    // git pack checksum
	Pack      string `json:"pack"`   // class-relative key, BaseKey(repo, gen, "pack")
	Idx       string `json:"idx"`    // class-relative key, BaseKey(repo, gen, "idx")
	SHA256    string `json:"sha256"` // sha256 of the pack file, lowercase hex
	IdxSHA256 string `json:"idx_sha256"`
	Size      int64  `json:"size"` // pack size in bytes
}

// Segment holds the objects added by one push.
type Segment struct {
	Seq       int64  `json:"seq"`
	SHA       string `json:"sha"`  // git pack checksum
	Pack      string `json:"pack"` // SegmentKey(repo, seq, sha, "pack")
	Idx       string `json:"idx"`  // SegmentKey(repo, seq, sha, "idx")
	SHA256    string `json:"sha256"`
	IdxSHA256 string `json:"idx_sha256"`
	Size      int64  `json:"size"`
}

// Manifest is the root of a repository's remote copy. A repository is fully
// described by Base (absent until the first compaction), the Segments after
// it in Seq order and the ref snapshot RefsKey(Repo, RefsSeq).
//
// Seq numbers are shared by segments and ref snapshots: every committed ref
// update gets the next RefsSeq; a push that brought objects also has a
// segment with that Seq. A push that only deletes or moves refs has none.
type Manifest struct {
	Version    int       `json:"version"`
	Repo       uuid.UUID `json:"repo"`
	Generation int64     `json:"generation"` // equals Base.Gen when Base is set
	Base       *Base     `json:"base,omitempty"`
	Segments   []Segment `json:"segments"`
	RefsSeq    int64     `json:"refs_seq"` // 0: no snapshot yet (empty repository)

	// ETag is the blob ETag the manifest was loaded with (or saved as). It is
	// not serialized: SaveManifest uses it as the compare-and-swap condition,
	// and an empty ETag means "the manifest must not exist yet".
	ETag string `json:"-"`
}

// NewManifest returns the manifest of an empty repository.
func NewManifest(repo uuid.UUID) *Manifest {
	return &Manifest{Version: ManifestVersion, Repo: repo, Segments: []Segment{}}
}

// Seq is the last committed sequence number.
func (m *Manifest) Seq() int64 { return m.RefsSeq }

// Packs returns every pack referenced by the manifest, base first.
func (m *Manifest) Packs() []Segment {
	out := make([]Segment, 0, len(m.Segments)+1)
	if b := m.Base; b != nil {
		out = append(out, Segment{Seq: 0, SHA: b.SHA, Pack: b.Pack, Idx: b.Idx, SHA256: b.SHA256, IdxSHA256: b.IdxSHA256, Size: b.Size})
	}
	return append(out, m.Segments...)
}

// Clone returns a deep copy.
func (m *Manifest) Clone() *Manifest {
	c := *m
	if m.Base != nil {
		b := *m.Base
		c.Base = &b
	}
	c.Segments = slices.Clone(m.Segments)
	if c.Segments == nil {
		c.Segments = []Segment{}
	}
	return &c
}

// ErrInvalidManifest wraps every Validate failure.
var ErrInvalidManifest = errors.New("gitstore: invalid manifest")

// Validate checks the manifest is internally consistent: known version,
// canonical keys for its own repository, increasing segment sequence
// numbers not beyond RefsSeq, well-formed digests.
func (m *Manifest) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidManifest, fmt.Sprintf(format, a...))
	}
	if m.Version != ManifestVersion {
		return bad("unsupported version %d", m.Version)
	}
	if m.Repo == uuid.Nil {
		return bad("missing repository")
	}
	if m.Generation < 0 || m.RefsSeq < 0 {
		return bad("negative generation or seq")
	}
	if b := m.Base; b != nil {
		if b.Gen != m.Generation {
			return bad("base generation %d != manifest generation %d", b.Gen, m.Generation)
		}
		if err := checkPack(b.SHA, b.SHA256, b.IdxSHA256, b.Size); err != nil {
			return bad("base: %v", err)
		}
		if b.Pack != BaseKey(m.Repo, b.Gen, ExtPack) || b.Idx != BaseKey(m.Repo, b.Gen, ExtIdx) {
			return bad("base: non-canonical keys")
		}
	}
	last := int64(0)
	seen := map[string]bool{}
	for i, s := range m.Segments {
		if s.Seq <= last {
			return bad("segment %d: seq %d not after %d", i, s.Seq, last)
		}
		last = s.Seq
		if err := checkPack(s.SHA, s.SHA256, s.IdxSHA256, s.Size); err != nil {
			return bad("segment %d: %v", s.Seq, err)
		}
		if s.Pack != SegmentKey(m.Repo, s.Seq, s.SHA, ExtPack) || s.Idx != SegmentKey(m.Repo, s.Seq, s.SHA, ExtIdx) {
			return bad("segment %d: non-canonical keys", s.Seq)
		}
		if seen[s.SHA] {
			return bad("segment %d: duplicate pack %s", s.Seq, s.SHA)
		}
		seen[s.SHA] = true
	}
	if last > m.RefsSeq {
		return bad("segment seq %d beyond refs_seq %d", last, m.RefsSeq)
	}
	return nil
}

func checkPack(sha, sha256, idxSHA256 string, size int64) error {
	if err := git.ValidateObjectID(sha); err != nil {
		return fmt.Errorf("pack checksum: %w", err)
	}
	if !isHex64(sha256) || !isHex64(idxSHA256) {
		return errors.New("sha256 must be 64 lowercase hex digits")
	}
	if size <= 0 {
		return fmt.Errorf("size %d", size)
	}
	return nil
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// RefsSnapshot is the content of refs/<seq>.json: every ref of the
// repository after push Seq.
type RefsSnapshot struct {
	Version int    `json:"version"`
	Seq     int64  `json:"seq"`
	Head    string `json:"head"` // symbolic target of HEAD, e.g. refs/heads/main
	// Refs maps full ref names to object IDs. Peeled tags are not stored:
	// they are recomputed from the objects.
	Refs map[string]string `json:"refs"`
}

// Validate checks names and object IDs.
func (r *RefsSnapshot) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: refs snapshot: %s", ErrInvalidManifest, fmt.Sprintf(format, a...))
	}
	if r.Version != ManifestVersion {
		return bad("unsupported version %d", r.Version)
	}
	if r.Seq <= 0 {
		return bad("seq %d", r.Seq)
	}
	if r.Head != "" {
		if err := git.ValidateRefName(r.Head); err != nil || r.Head == "HEAD" {
			return bad("head %q", r.Head)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(r.Refs)) {
		if err := git.ValidateRefName(name); err != nil || name == "HEAD" {
			return bad("ref %q", name)
		}
		if err := git.ValidateObjectID(r.Refs[name]); err != nil {
			return bad("ref %q: %v", name, err)
		}
	}
	return nil
}
