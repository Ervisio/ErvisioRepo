package git

import (
	"strings"
	"testing"
)

func TestValidateRefName(t *testing.T) {
	good := []string{"HEAD", "refs/heads/main", "refs/heads/feature/x-1", "refs/tags/v1.0.0", "refs/pull/1/head"}
	bad := []string{"", "main", "refs/heads", "refs/heads/", "refs/heads/a..b", "refs/heads/.x", "refs/heads/x.lock",
		"refs/heads/a b", "refs/heads/a~1", "refs/heads/a^", "refs/heads/a:b", "refs/heads/a@{1}", "refs/heads/-x",
		"refs/heads/a\\b", "refs/heads/a//b", "refs/heads/x.", "refs/heads/\x00", "refs/heads/è", "refs/heads/a*"}
	for _, r := range good {
		if err := ValidateRefName(r); err != nil {
			t.Errorf("%q: %v", r, err)
		}
	}
	for _, r := range bad {
		if ValidateRefName(r) == nil {
			t.Errorf("%q accepted", r)
		}
	}
}

func TestValidateBranchName(t *testing.T) {
	for _, b := range []string{"main", "feature/x", "v1.2"} {
		if err := ValidateBranchName(b); err != nil {
			t.Errorf("%q: %v", b, err)
		}
	}
	for _, b := range []string{"", "HEAD", "-f", "refs/heads/x", "@", "a..b", "--upload-pack=x"} {
		if ValidateBranchName(b) == nil {
			t.Errorf("%q accepted", b)
		}
	}
	if ValidateTagName("v1") != nil || ValidateTagName("-v") == nil {
		t.Error("tag validation")
	}
}

func TestValidateTreePath(t *testing.T) {
	for _, p := range []string{"", "a", "src/main.go", "a b/c.txt", ".github/workflows/ci.yml", "dir/.gitignore"} {
		if err := ValidateTreePath(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	for _, p := range []string{"/a", "a/", "a//b", "../a", "a/../b", "./a", ".git/config", "x/.GIT/y", "a\x00b",
		"a\nb", "a\\b", "-a", strings.Repeat("a", MaxTreePath+1)} {
		if ValidateTreePath(p) == nil {
			t.Errorf("%q accepted", p)
		}
	}
}

func TestValidateRepoName(t *testing.T) {
	for _, n := range []string{"a", "my-repo", "repo.js", "Under_score"} {
		if err := ValidateRepoName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"", ".", "..", ".hidden", "-x", "a/b", "a b", "x.git", "X.GIT", strings.Repeat("a", 101), "è"} {
		if ValidateRepoName(n) == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestValidateObjectID(t *testing.T) {
	if ValidateObjectID(strings.Repeat("a", 40)) != nil || ValidateObjectID(strings.Repeat("0", 64)) != nil {
		t.Error("valid id rejected")
	}
	for _, id := range []string{"", "abc", strings.Repeat("A", 40), strings.Repeat("g", 40), strings.Repeat("a", 41)} {
		if ValidateObjectID(id) == nil {
			t.Errorf("%q accepted", id)
		}
	}
	if ValidateRevision("main~1") == nil || ValidateRevision("HEAD") != nil || ValidateRevision("refs/tags/v1") != nil {
		t.Error("revision validation")
	}
}

func FuzzValidateRefName(f *testing.F) {
	for _, s := range []string{"refs/heads/main", "refs/heads/a..b", "HEAD", "refs/x/.lock", "refs/heads/-"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if ValidateRefName(s) != nil || s == "HEAD" {
			return
		}
		if strings.Contains(s, "..") || strings.ContainsAny(s, " ~^:?*[\\\x00\n") || strings.Contains(s, "/-") ||
			strings.Contains(s, "@{") || strings.HasSuffix(s, ".lock") || strings.Contains(s, "/.") {
			t.Fatalf("accepted %q", s)
		}
	})
}

func FuzzValidateBranchName(f *testing.F) {
	for _, s := range []string{"main", "-x", "a/b", "HEAD"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if ValidateBranchName(s) == nil {
			if strings.HasPrefix(s, "-") || ValidateRefName("refs/heads/"+s) != nil {
				t.Fatalf("accepted %q", s)
			}
		}
	})
}

func FuzzValidateTreePath(f *testing.F) {
	for _, s := range []string{"a/b", "../x", ".git", "a/./b", "/abs"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if ValidateTreePath(s) != nil || s == "" {
			return
		}
		for _, c := range strings.Split(s, "/") {
			if c == "" || c == "." || c == ".." || strings.EqualFold(c, ".git") {
				t.Fatalf("accepted %q", s)
			}
		}
		if strings.ContainsAny(s, "\x00\n\r\\") || strings.HasPrefix(s, "-") {
			t.Fatalf("accepted %q", s)
		}
	})
}

func FuzzValidateRepoName(f *testing.F) {
	for _, s := range []string{"repo", "a.git", ".x", "a/b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if ValidateRepoName(s) != nil {
			return
		}
		if strings.ContainsAny(s, "/\\\x00 ") || s == "." || s == ".." || strings.HasPrefix(s, "-") ||
			strings.HasSuffix(strings.ToLower(s), ".git") || len(s) > MaxRepoName {
			t.Fatalf("accepted %q", s)
		}
	})
}

func FuzzValidateObjectID(f *testing.F) {
	f.Add(strings.Repeat("a", 40))
	f.Fuzz(func(t *testing.T, s string) {
		if ValidateObjectID(s) == nil && strings.Trim(s, "0123456789abcdef") != "" {
			t.Fatalf("accepted %q", s)
		}
	})
}
