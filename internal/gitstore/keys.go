// Package gitstore keeps git repositories on the storage tier (PLAN §4.3):
// the remote pack journal (manifest, base pack, segments, ref snapshots) and
// the local hot copies handed out by Tier.Acquire.
//
// Remote layout, relative to the "git" storage class (the full blob key gets
// the "git/" prefix):
//
//	<repo>/manifest.json              mutable, CAS-written on the class primary
//	<repo>/base/<gen>.pack|.idx       base pack of generation <gen>
//	<repo>/seg/<seq>-<sha>.pack|.idx  objects of a single push
//	<repo>/refs/<seq>.json            full ref snapshot after push <seq>
//
// <repo> is the repository UUID, <gen> and <seq> are decimal integers without
// leading zeros and <sha> is the git pack checksum (40 or 64 lowercase hex).
// Packs and snapshots are immutable and go through tier.Class (storage_objects
// rows, cache, replication); only the manifest bypasses the class.
package gitstore

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// Policy is the storage policy (data type) holding git data.
const Policy = "git"

// Pack file extensions.
const (
	ExtPack = "pack"
	ExtIdx  = "idx"
)

// KeyKind tells what a key points to.
type KeyKind int

const (
	KindManifest KeyKind = iota + 1
	KindBase
	KindSegment
	KindRefs
)

func (k KeyKind) String() string {
	switch k {
	case KindManifest:
		return "manifest"
	case KindBase:
		return "base"
	case KindSegment:
		return "segment"
	case KindRefs:
		return "refs"
	}
	return "unknown"
}

// Key is a parsed journal key.
type Key struct {
	Repo uuid.UUID
	Kind KeyKind
	Gen  int64  // KindBase
	Seq  int64  // KindSegment, KindRefs
	SHA  string // KindSegment
	Ext  string // KindBase, KindSegment: ExtPack or ExtIdx
}

// ErrInvalidKey is returned by ParseKey and by the key builders.
var ErrInvalidKey = errors.New("gitstore: invalid key")

// RepoPrefix is the class-relative prefix of every key of a repository.
func RepoPrefix(repo uuid.UUID) string { return repo.String() + "/" }

// ManifestKey is the class-relative key of the manifest.
func ManifestKey(repo uuid.UUID) string { return RepoPrefix(repo) + "manifest.json" }

// BaseKey is the class-relative key of a base pack or index.
func BaseKey(repo uuid.UUID, gen int64, ext string) string {
	return RepoPrefix(repo) + "base/" + strconv.FormatInt(gen, 10) + "." + ext
}

// SegmentKey is the class-relative key of a segment pack or index.
func SegmentKey(repo uuid.UUID, seq int64, sha, ext string) string {
	return RepoPrefix(repo) + "seg/" + strconv.FormatInt(seq, 10) + "-" + sha + "." + ext
}

// RefsKey is the class-relative key of a ref snapshot.
func RefsKey(repo uuid.UUID, seq int64) string {
	return RepoPrefix(repo) + "refs/" + strconv.FormatInt(seq, 10) + ".json"
}

// String rebuilds the key.
func (k Key) String() string {
	switch k.Kind {
	case KindManifest:
		return ManifestKey(k.Repo)
	case KindBase:
		return BaseKey(k.Repo, k.Gen, k.Ext)
	case KindSegment:
		return SegmentKey(k.Repo, k.Seq, k.SHA, k.Ext)
	case KindRefs:
		return RefsKey(k.Repo, k.Seq)
	}
	return ""
}

// ParseKey parses a class-relative key (without the "git/" prefix). Only the
// canonical form is accepted: ParseKey(k).String() == k.
func ParseKey(key string) (Key, error) {
	bad := func(why string) (Key, error) {
		return Key{}, fmt.Errorf("%w: %q: %s", ErrInvalidKey, key, why)
	}
	repoPart, rest, ok := strings.Cut(key, "/")
	if !ok {
		return bad("missing repository")
	}
	repo, err := uuid.Parse(repoPart)
	if err != nil || repo.String() != repoPart {
		return bad("repository is not a canonical UUID")
	}
	k := Key{Repo: repo}
	dir, file, ok := strings.Cut(rest, "/")
	switch {
	case !ok && rest == "manifest.json":
		k.Kind = KindManifest
		return k, nil
	case !ok || strings.Contains(file, "/"):
		return bad("unknown layout")
	}
	switch dir {
	case "base":
		stem, ext, ok := cutExt(file)
		if !ok {
			return bad("base must end in .pack or .idx")
		}
		if k.Gen, ok = parseNum(stem); !ok {
			return bad("bad generation")
		}
		k.Kind, k.Ext = KindBase, ext
	case "seg":
		stem, ext, ok := cutExt(file)
		if !ok {
			return bad("segment must end in .pack or .idx")
		}
		num, sha, ok := strings.Cut(stem, "-")
		if !ok {
			return bad("segment must be <seq>-<sha>")
		}
		if k.Seq, ok = parseNum(num); !ok {
			return bad("bad sequence")
		}
		if git.ValidateObjectID(sha) != nil {
			return bad("bad pack checksum")
		}
		k.Kind, k.SHA, k.Ext = KindSegment, sha, ext
	case "refs":
		num, ok := strings.CutSuffix(file, ".json")
		if !ok {
			return bad("ref snapshot must end in .json")
		}
		if k.Seq, ok = parseNum(num); !ok {
			return bad("bad sequence")
		}
		k.Kind = KindRefs
	default:
		return bad("unknown directory")
	}
	return k, nil
}

func cutExt(file string) (stem, ext string, ok bool) {
	stem, ext, ok = strings.Cut(file, ".")
	return stem, ext, ok && (ext == ExtPack || ext == ExtIdx)
}

// parseNum accepts canonical non-negative decimals ("0", "17", not "017").
func parseNum(s string) (int64, bool) {
	if s == "" || len(s) > 18 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}
