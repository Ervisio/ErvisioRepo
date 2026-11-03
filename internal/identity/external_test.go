package identity

import (
	"strings"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	for in, want := range map[string]string{
		"alice":                 "alice",
		"Alice.Smith":           "Alice-Smith",
		"alice@example.com":     "alice",
		"--a__b--":              "a-b",
		"":                      "member",
		"日本":                    "member",
		"admin":                 "member",
		strings.Repeat("x", 60): strings.Repeat("x", 39),
	} {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzSanitizeName(f *testing.F) {
	for _, s := range []string{"alice", "a.b@c", "-", "日本語", "x--y"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if err := ValidateName(SanitizeName(s)); err != nil {
			t.Fatalf("SanitizeName(%q) = %q: %v", s, SanitizeName(s), err)
		}
	})
}
