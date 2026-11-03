package protection

import (
	"bufio"
	"context"
	"fmt"
	"net/mail"
	"regexp"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// CodeOwnersPaths are the locations of the CODEOWNERS file, in the order
// GitHub looks for it; the first one present is used.
var CodeOwnersPaths = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

// maxCodeOwnersSize bounds the CODEOWNERS file (GitHub: 3 MB).
const maxCodeOwnersSize = 3 << 20

// CodeOwners is a parsed CODEOWNERS file.
type CodeOwners struct {
	// Path is the file the rules were read from ("" when there is none).
	Path  string
	Rules []OwnerRule
	// Errors lists the lines that were ignored, as "line N: reason".
	Errors []string
}

// OwnerRule is one line of a CODEOWNERS file.
type OwnerRule struct {
	Line    int
	Pattern string
	// Owners are "@user", "@org/team" or an email address; empty means
	// the matching paths have no owner.
	Owners []string
	re     *regexp.Regexp
}

// ParseCodeOwners parses a CODEOWNERS file with the GitHub syntax: one
// gitignore-style pattern per line followed by owners, "#" comments, and
// the last matching line wins. Invalid lines are skipped and reported.
func ParseCodeOwners(path string, data []byte) *CodeOwners {
	c := &CodeOwners{Path: path}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64<<10), maxCodeOwnersSize)
	n := 0
	for sc.Scan() {
		n++
		line := stripComment(sc.Text())
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		pattern := strings.ReplaceAll(f[0], `\#`, "#")
		re, err := ownerPattern(pattern)
		if err != nil {
			c.Errors = append(c.Errors, fmt.Sprintf("line %d: %v", n, err))
			continue
		}
		r := OwnerRule{Line: n, Pattern: pattern, re: re}
		bad := false
		for _, o := range f[1:] {
			if !validOwner(o) {
				c.Errors = append(c.Errors, fmt.Sprintf("line %d: invalid owner %q", n, o))
				bad = true
				break
			}
			r.Owners = append(r.Owners, o)
		}
		if !bad {
			c.Rules = append(c.Rules, r)
		}
	}
	if err := sc.Err(); err != nil {
		c.Errors = append(c.Errors, fmt.Sprintf("line %d: %v", n+1, err))
	}
	return c
}

// stripComment removes a "#" comment; "\#" is a literal hash.
func stripComment(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && (i == 0 || s[i-1] != '\\') {
			return s[:i]
		}
	}
	return s
}

func validOwner(o string) bool {
	if name, ok := strings.CutPrefix(o, "@"); ok {
		user, team, isTeam := strings.Cut(name, "/")
		if !validName(user) {
			return false
		}
		return !isTeam || validName(team)
	}
	a, err := mail.ParseAddress(o)
	return err == nil && a.Address == o
}

func validName(s string) bool {
	if s == "" || len(s) > 39 {
		return false
	}
	for _, r := range s {
		alnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !alnum && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

// ownerPattern compiles a CODEOWNERS pattern to a regular expression on
// the slash-separated path of a file.
func ownerPattern(p string) (*regexp.Regexp, error) {
	switch {
	case strings.HasPrefix(p, "!"):
		return nil, fmt.Errorf("negated pattern %q is not supported", p)
	case strings.ContainsAny(p, "[]"):
		return nil, fmt.Errorf("character ranges in %q are not supported", p)
	}
	dirOnly := strings.HasSuffix(p, "/")
	body := strings.Trim(p, "/")
	anchored := strings.HasPrefix(p, "/") || strings.Contains(body, "/")
	if body == "" {
		return nil, fmt.Errorf("empty pattern %q", p)
	}
	var b strings.Builder
	if anchored {
		b.WriteString("^")
	} else {
		b.WriteString("^(?:.*/)?")
	}
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case strings.HasPrefix(body[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(body[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	if dirOnly {
		b.WriteString("/.*$")
	} else {
		b.WriteString("(?:/.*)?$")
	}
	return regexp.Compile(b.String())
}

// Match returns the rule that applies to path (the last matching one), or
// nil when no rule matches.
func (c *CodeOwners) Match(path string) *OwnerRule {
	if c == nil {
		return nil
	}
	path = strings.TrimPrefix(path, "/")
	for i := len(c.Rules) - 1; i >= 0; i-- {
		if c.Rules[i].re.MatchString(path) {
			return &c.Rules[i]
		}
	}
	return nil
}

// Owners returns the owners of path; nil when it has none.
func (c *CodeOwners) Owners(path string) []string {
	if r := c.Match(path); r != nil {
		return r.Owners
	}
	return nil
}

// OwnersOf maps each changed path to its owners. Paths without owners are
// left out.
func (c *CodeOwners) OwnersOf(paths []string) map[string][]string {
	out := make(map[string][]string)
	for _, p := range paths {
		if o := c.Owners(p); len(o) > 0 {
			out[p] = o
		}
	}
	return out
}

// LoadCodeOwners reads the CODEOWNERS file of commit rev (the head of the
// target branch). A repository without one yields an empty, non-nil set.
// env holds extra git variables (a push quarantine).
func LoadCodeOwners(ctx context.Context, repoPath string, env []string, rev string) (*CodeOwners, error) {
	for _, p := range CodeOwnersPaths {
		data, err := git.ReadBlobAt(ctx, repoPath, env, rev, p, maxCodeOwnersSize)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		if data != nil {
			return ParseCodeOwners(p, data), nil
		}
	}
	return &CodeOwners{}, nil
}
