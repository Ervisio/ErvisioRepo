package gitstore

import (
	"errors"
	"strings"
	"testing"
)

var hex64 = strings.Repeat("ab", 32)

func seg(seq int64, sha string) Segment {
	return Segment{Seq: seq, SHA: sha, Pack: SegmentKey(testRepo, seq, sha, ExtPack), Idx: SegmentKey(testRepo, seq, sha, ExtIdx),
		SHA256: hex64, IdxSHA256: hex64, Size: 100}
}

func validManifest() *Manifest {
	m := NewManifest(testRepo)
	m.Generation = 2
	m.Base = &Base{Gen: 2, SHA: sha1, Pack: BaseKey(testRepo, 2, ExtPack), Idx: BaseKey(testRepo, 2, ExtIdx),
		SHA256: hex64, IdxSHA256: hex64, Size: 10}
	m.Segments = []Segment{seg(3, strings.Repeat("1", 40)), seg(5, strings.Repeat("2", 40))}
	m.RefsSeq = 6
	return m
}

func TestManifestValidate(t *testing.T) {
	if err := NewManifest(testRepo).Validate(); err != nil {
		t.Fatalf("empty manifest: %v", err)
	}
	m := validManifest()
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(m.Packs()) != 3 || m.Packs()[0].Pack != m.Base.Pack || m.Seq() != 6 {
		t.Fatalf("packs %+v", m.Packs())
	}
	c := m.Clone()
	c.Base.Gen, c.Segments[0].Seq = 9, 9
	if m.Base.Gen != 2 || m.Segments[0].Seq != 3 {
		t.Fatal("clone shares memory")
	}
	for name, mut := range map[string]func(*Manifest){
		"version":        func(m *Manifest) { m.Version = 2 },
		"generation":     func(m *Manifest) { m.Generation = 1 },
		"base keys":      func(m *Manifest) { m.Base.Pack = BaseKey(testRepo, 1, ExtPack) },
		"base sha256":    func(m *Manifest) { m.Base.SHA256 = "AB" },
		"order":          func(m *Manifest) { m.Segments[1].Seq = 3 },
		"beyond refs":    func(m *Manifest) { m.RefsSeq = 4 },
		"segment key":    func(m *Manifest) { m.Segments[0].Idx = m.Segments[0].Pack },
		"segment size":   func(m *Manifest) { m.Segments[0].Size = 0 },
		"segment sha":    func(m *Manifest) { m.Segments[0].SHA = "zz" },
		"duplicate pack": func(m *Manifest) { m.Segments[1] = seg(5, m.Segments[0].SHA) },
		"zero seq":       func(m *Manifest) { m.Segments[0] = seg(0, m.Segments[0].SHA) },
	} {
		m := validManifest()
		mut(m)
		if err := m.Validate(); !errors.Is(err, ErrInvalidManifest) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRefsSnapshotValidate(t *testing.T) {
	ok := &RefsSnapshot{Version: 1, Seq: 1, Head: "refs/heads/main", Refs: map[string]string{"refs/heads/main": sha1}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*RefsSnapshot{
		"seq":      {Version: 1, Seq: 0},
		"version":  {Version: 0, Seq: 1},
		"head":     {Version: 1, Seq: 1, Head: "HEAD"},
		"ref name": {Version: 1, Seq: 1, Refs: map[string]string{"refs/heads/a..b": sha1}},
		"oid":      {Version: 1, Seq: 1, Refs: map[string]string{"refs/heads/a": "123"}},
	} {
		if err := s.Validate(); !errors.Is(err, ErrInvalidManifest) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
