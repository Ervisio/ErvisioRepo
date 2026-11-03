package gitstore

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var testRepo = uuid.MustParse("0192f3a1-7b2c-7d4e-8f00-123456789abc")

const sha1 = "0123456789abcdef0123456789abcdef01234567"

func TestKeys(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want Key
	}{
		{ManifestKey(testRepo), Key{Repo: testRepo, Kind: KindManifest}},
		{BaseKey(testRepo, 3, ExtPack), Key{Repo: testRepo, Kind: KindBase, Gen: 3, Ext: ExtPack}},
		{BaseKey(testRepo, 0, ExtIdx), Key{Repo: testRepo, Kind: KindBase, Gen: 0, Ext: ExtIdx}},
		{SegmentKey(testRepo, 42, sha1, ExtPack), Key{Repo: testRepo, Kind: KindSegment, Seq: 42, SHA: sha1, Ext: ExtPack}},
		{RefsKey(testRepo, 7), Key{Repo: testRepo, Kind: KindRefs, Seq: 7}},
	} {
		got, err := ParseKey(tc.key)
		if err != nil || got != tc.want {
			t.Errorf("ParseKey(%q) = %+v, %v", tc.key, got, err)
		}
		if got.String() != tc.key {
			t.Errorf("round trip %q -> %q", tc.key, got.String())
		}
	}
	if want := "0192f3a1-7b2c-7d4e-8f00-123456789abc/seg/42-" + sha1 + ".idx"; SegmentKey(testRepo, 42, sha1, ExtIdx) != want {
		t.Errorf("segment key %q", SegmentKey(testRepo, 42, sha1, ExtIdx))
	}
	r := testRepo.String()
	for _, bad := range []string{
		"", r, r + "/", "x/manifest.json", strings.ToUpper(r) + "/manifest.json",
		r + "/manifest.json/x", r + "/base/1.tar", r + "/base/01.pack", r + "/base/-1.pack",
		r + "/base/1.pack.idx", r + "/seg/1.pack", r + "/seg/1-xyz.pack", r + "/seg/01-" + sha1 + ".pack",
		r + "/refs/1.pack", r + "/refs/a.json", r + "/other/1.json", r + "/refs/1/2.json",
		r + "/base/99999999999999999999.pack",
	} {
		if _, err := ParseKey(bad); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("ParseKey(%q) accepted", bad)
		}
	}
}

func FuzzParseKey(f *testing.F) {
	for _, s := range []string{ManifestKey(testRepo), BaseKey(testRepo, 1, ExtPack),
		SegmentKey(testRepo, 2, sha1, ExtIdx), RefsKey(testRepo, 3), "../x", testRepo.String() + "/seg/1-a.pack"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		k, err := ParseKey(s)
		if err != nil {
			return
		}
		if k.String() != s {
			t.Fatalf("non-canonical key accepted: %q -> %q", s, k.String())
		}
		if strings.Contains(s, "..") || strings.HasPrefix(s, "/") || strings.Contains(s, "//") {
			t.Fatalf("unsafe key accepted: %q", s)
		}
	})
}
