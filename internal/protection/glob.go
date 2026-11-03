package protection

import (
	"fmt"
	"strings"
	"unicode"
)

// MaxPatternLen bounds a rule pattern.
const MaxPatternLen = 255

// ValidatePattern checks a branch or tag rule pattern: a glob on the short
// ref name where "*" matches within one path segment, "**" across
// segments and "?" one character.
func ValidatePattern(p string) error {
	if p == "" || len(p) > MaxPatternLen {
		return fmt.Errorf("%w: pattern must be 1 to %d characters", ErrInvalid, MaxPatternLen)
	}
	if strings.HasPrefix(p, "refs/") {
		return fmt.Errorf("%w: pattern is matched against the short name, without refs/heads/ or refs/tags/", ErrInvalid)
	}
	for _, r := range p {
		if unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune("~^:\\[", r) {
			return fmt.Errorf("%w: pattern contains %q", ErrInvalid, r)
		}
	}
	if strings.Contains(p, "***") {
		return fmt.Errorf("%w: pattern contains \"***\"", ErrInvalid)
	}
	return nil
}

// Match reports whether the short ref name matches the pattern.
func Match(pattern, name string) bool {
	return match(pattern, name)
}

func match(p, s string) bool {
	for p != "" {
		switch {
		case strings.HasPrefix(p, "**"):
			rest := strings.TrimPrefix(p, "**")
			for i := 0; i <= len(s); i++ {
				if match(rest, s[i:]) {
					return true
				}
			}
			return false
		case p[0] == '*':
			rest := p[1:]
			for i := 0; i <= len(s); i++ {
				if match(rest, s[i:]) {
					return true
				}
				if i < len(s) && s[i] == '/' {
					return false
				}
			}
			return false
		case p[0] == '?':
			if s == "" || s[0] == '/' {
				return false
			}
			p, s = p[1:], s[1:]
		default:
			if s == "" || p[0] != s[0] {
				return false
			}
			p, s = p[1:], s[1:]
		}
	}
	return s == ""
}

// specificity orders patterns: an exact name first, then fewer wildcards,
// then more literal characters.
type specificity struct {
	exact     bool
	wildcards int
	literal   int
}

func specificityOf(p string) specificity {
	w := strings.Count(p, "**")
	q := strings.ReplaceAll(p, "**", "")
	w += strings.Count(q, "*") + strings.Count(q, "?")
	lit := len(q) - strings.Count(q, "*") - strings.Count(q, "?")
	return specificity{exact: w == 0, wildcards: w, literal: lit}
}

// moreSpecific reports whether pattern a wins over b.
func moreSpecific(a, b string) bool {
	sa, sb := specificityOf(a), specificityOf(b)
	switch {
	case sa.exact != sb.exact:
		return sa.exact
	case sa.wildcards != sb.wildcards:
		return sa.wildcards < sb.wildcards
	case sa.literal != sb.literal:
		return sa.literal > sb.literal
	}
	return a < b
}

// Select returns the index of the most specific pattern matching name, or
// -1 when none does.
func Select(patterns []string, name string) int {
	best := -1
	for i, p := range patterns {
		if !Match(p, name) {
			continue
		}
		if best < 0 || moreSpecific(p, patterns[best]) {
			best = i
		}
	}
	return best
}
