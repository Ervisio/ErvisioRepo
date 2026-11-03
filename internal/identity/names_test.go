package identity

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	good := []string{"a", "alice", "Alice-Bob", "a1-b2-c3", "0", strings.Repeat("x", 39)}
	for _, n := range good {
		if err := ValidateName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	bad := []string{"", "-a", "a-", "a--b", "a_b", "a.b", "à", "a b", strings.Repeat("x", 40), "a/b", "..", "a\x00"}
	for _, n := range bad {
		if err := ValidateName(n); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"admin", "API", "Settings", "login", "verify-email"} {
		if err := ValidateName(n); !errors.Is(err, ErrReservedName) {
			t.Errorf("%q: %v", n, err)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	for in, want := range map[string]string{
		"alice@example.com":     "alice@example.com",
		"  Bob.X+tag@Ex.org \n": "Bob.X+tag@Ex.org",
		"root@localhost":        "root@localhost",
	} {
		got, err := NormalizeEmail(in)
		if err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "a", "a@", "@b.c", "Alice <a@b.c>", "a@b", "a b@c.d", "a@b.c\r\nBcc: x@y.z",
		strings.Repeat("a", 250) + "@b.cd", "<a@b.c>"} {
		if _, err := NormalizeEmail(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

// The database check constraint uses this expression; keep them in sync.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9](-?[A-Za-z0-9])*$`)

func FuzzValidateName(f *testing.F) {
	for _, s := range []string{"alice", "a-b", "-", "a--b", "admin", "x" + strings.Repeat("-y", 20)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		err := ValidateName(s)
		matches := nameRE.MatchString(s) && len(s) <= MaxNameLength
		if (err == nil || errors.Is(err, ErrReservedName)) != matches {
			t.Fatalf("%q: ValidateName %v, regexp %v", s, err, matches)
		}
		if err == nil && IsReserved(s) {
			t.Fatalf("%q reserved but accepted", s)
		}
	})
}

func FuzzNormalizeEmail(f *testing.F) {
	for _, s := range []string{"a@b.c", "A <a@b.c>", "a@b.c\nX: y", "\"q\"@b.c"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, err := NormalizeEmail(s)
		if err != nil {
			return
		}
		if strings.ContainsAny(got, "\r\n<> ") || len(got) > MaxEmailLength || strings.Count(got, "@") < 1 {
			t.Fatalf("%q normalized to unsafe %q", s, got)
		}
		again, err := NormalizeEmail(got)
		if err != nil || again != got {
			t.Fatalf("not idempotent: %q -> %q (%v)", got, again, err)
		}
	})
}
