package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestValidateKey(t *testing.T) {
	good := []string{"a", "a/b/c", "repo-1/objects/ab/cdef", "x_y=z+w@v,u.t", strings.Repeat("a", MaxKeyLen)}
	for _, k := range good {
		if err := ValidateKey(k); err != nil {
			t.Errorf("ValidateKey(%q) = %v", k, err)
		}
	}
	bad := []string{"", "/a", "a/", "a//b", ".", "..", "a/../b", "a/.hidden", ".erepo-tmp-x",
		`a\b`, "a b", "a\x00b", "à", "a:b", "a?b", "a*b", strings.Repeat("a", MaxKeyLen+1)}
	for _, k := range bad {
		if err := ValidateKey(k); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("ValidateKey(%q) = %v, want ErrInvalidKey", k, err)
		}
	}
}

func TestValidatePrefix(t *testing.T) {
	for _, p := range []string{"", "a", "a/", "a/b/"} {
		if err := ValidatePrefix(p); err != nil {
			t.Errorf("ValidatePrefix(%q) = %v", p, err)
		}
	}
	for _, p := range []string{"/", "a//", "../", ".x/"} {
		if err := ValidatePrefix(p); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("ValidatePrefix(%q) = %v", p, err)
		}
	}
}

func FuzzValidateKey(f *testing.F) {
	for _, s := range []string{"a/b", "../x", "a/./b", `a\b`, ".erepo-lock-x", "a/b/", "\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, key string) {
		if ValidateKey(key) != nil {
			return
		}
		if len(key) > MaxKeyLen || strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
			t.Fatalf("accepted %q", key)
		}
		for seg := range strings.SplitSeq(key, "/") {
			if seg == "" || seg[0] == '.' || strings.HasPrefix(seg, ReservedPrefix) {
				t.Fatalf("accepted segment %q in %q", seg, key)
			}
			if strings.ContainsAny(seg, "\\\x00:") {
				t.Fatalf("accepted unsafe byte in %q", key)
			}
		}
	})
}

func TestResolveRange(t *testing.T) {
	cases := []struct {
		rng        *Range
		size       int64
		start, end int64
		err        bool
	}{
		{nil, 10, 0, 10, false},
		{&Range{0, -1}, 10, 0, 10, false},
		{&Range{3, 4}, 10, 3, 7, false},
		{&Range{3, 100}, 10, 3, 10, false},
		{&Range{10, -1}, 10, 10, 10, false},
		{&Range{0, 0}, 10, 0, 0, false},
		{&Range{11, 1}, 10, 0, 0, true},
		{&Range{-1, 1}, 10, 0, 0, true},
	}
	for i, c := range cases {
		s, e, err := ResolveRange(c.rng, c.size)
		if c.err {
			if !errors.Is(err, ErrInvalidRange) {
				t.Errorf("%d: err = %v", i, err)
			}
			continue
		}
		if err != nil || s != c.start || e != c.end {
			t.Errorf("%d: got [%d,%d) %v, want [%d,%d)", i, s, e, err, c.start, c.end)
		}
	}
}

func TestCheckCondition(t *testing.T) {
	cur := &Info{ETag: "e1"}
	for i, c := range []struct {
		cond Condition
		cur  *Info
		ok   bool
	}{
		{Condition{}, nil, true},
		{Condition{}, cur, true},
		{Condition{IfNoneMatch: true}, nil, true},
		{Condition{IfNoneMatch: true}, cur, false},
		{Condition{IfMatch: "e1"}, cur, true},
		{Condition{IfMatch: "e2"}, cur, false},
		{Condition{IfMatch: "e1"}, nil, false},
	} {
		err := CheckCondition(c.cond, c.cur)
		if c.ok != (err == nil) || (err != nil && !errors.Is(err, ErrPreconditionFailed)) {
			t.Errorf("%d: %v", i, err)
		}
	}
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestVerifyReader(t *testing.T) {
	data := []byte("hello world")
	ctx := context.Background()
	read := func(size int64, sha string, src []byte) ([]byte, error) {
		v, err := NewVerifyReader(ctx, bytes.NewReader(src), size, sha)
		if err != nil {
			return nil, err
		}
		return io.ReadAll(v)
	}
	if got, err := read(11, sum(data), data); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("valid body: %q %v", got, err)
	}
	if _, err := read(-1, "", data); err != nil {
		t.Fatalf("no checks: %v", err)
	}
	if _, err := read(12, "", data); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("short body: %v", err)
	}
	if _, err := read(10, "", data); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("long body: %v", err)
	}
	if _, err := read(11, sum([]byte("other")), data); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("bad checksum: %v", err)
	}
	if _, err := read(11, "zz", data); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("malformed checksum: %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	v, _ := NewVerifyReader(cctx, bytes.NewReader(data), 11, "")
	if _, err := io.ReadAll(v); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestIsTransient(t *testing.T) {
	if !IsTransient(MarkTransient(errors.New("x"))) || !IsTransient(io.ErrUnexpectedEOF) {
		t.Fatal("transient not detected")
	}
	if MarkTransient(nil) != nil {
		t.Fatal("MarkTransient(nil) != nil")
	}
	for _, err := range []error{nil, errors.New("x"), ErrNotFound, context.Canceled,
		MarkTransient(fmt.Errorf("wrapped: %w", ErrPreconditionFailed))} {
		if IsTransient(err) {
			t.Errorf("IsTransient(%v) = true", err)
		}
	}
}

func TestKeyedMutex(t *testing.T) {
	var km KeyedMutex
	var wg sync.WaitGroup
	var counter [3]int
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := km.Lock(fmt.Sprint(i % 3))
			counter[i%3]++ // each slot is guarded by its key's lock
			unlock()
		}()
	}
	wg.Wait()
	if counter[0]+counter[1]+counter[2] != 50 {
		t.Fatalf("counter = %v", counter)
	}
	if len(km.locks) != 0 {
		t.Fatalf("locks leaked: %d", len(km.locks))
	}
}
