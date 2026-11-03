package git

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalid is wrapped by every validation error.
var ErrInvalid = errors.New("invalid")

func invalid(kind, why string) error { return fmt.Errorf("%w %s: %s", ErrInvalid, kind, why) }

// MaxRefName bounds a full ref name.
const MaxRefName = 255

// ValidateRefName checks a full ref name ("refs/heads/main", or "HEAD") with
// the rules of git check-ref-format, plus: printable ASCII only, no leading
// '-' in any component and at least two components below refs/.
func ValidateRefName(name string) error {
	if name == "HEAD" {
		return nil
	}
	if !strings.HasPrefix(name, "refs/") {
		return invalid("ref", "must start with refs/")
	}
	if err := checkRefFormat(name); err != nil {
		return err
	}
	if strings.Count(name, "/") < 2 {
		return invalid("ref", "too few components")
	}
	return nil
}

// ValidateBranchName checks a short branch name ("main", "feature/x").
func ValidateBranchName(name string) error {
	if name == "HEAD" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "refs/") {
		return invalid("branch", "reserved name")
	}
	if err := checkRefFormat(name); err != nil {
		return err
	}
	return checkRefFormat("refs/heads/" + name)
}

// ValidateTagName checks a short tag name.
func ValidateTagName(name string) error {
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, "refs/") {
		return invalid("tag", "reserved name")
	}
	if err := checkRefFormat(name); err != nil {
		return err
	}
	return checkRefFormat("refs/tags/" + name)
}

func checkRefFormat(name string) error {
	if name == "" {
		return invalid("ref", "empty")
	}
	if len(name) > MaxRefName {
		return invalid("ref", "too long")
	}
	if name == "@" {
		return invalid("ref", "is @")
	}
	if strings.HasSuffix(name, "/") || strings.HasPrefix(name, "/") {
		return invalid("ref", "leading or trailing slash")
	}
	if strings.HasSuffix(name, ".") {
		return invalid("ref", "ends with '.'")
	}
	if strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "//") {
		return invalid("ref", "forbidden sequence")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c <= ' ' || c >= 0x7f {
			return invalid("ref", "control, space or non-ASCII character")
		}
		switch c {
		case '~', '^', ':', '?', '*', '[', '\\':
			return invalid("ref", fmt.Sprintf("forbidden character %q", c))
		}
	}
	for _, comp := range strings.Split(name, "/") {
		if strings.HasPrefix(comp, ".") || strings.HasPrefix(comp, "-") {
			return invalid("ref", "component starts with '.' or '-'")
		}
		if strings.HasSuffix(comp, ".lock") {
			return invalid("ref", "component ends with .lock")
		}
	}
	return nil
}

// MaxTreePath bounds a path inside a tree.
const MaxTreePath = 4096

// ValidateTreePath checks a slash-separated path inside a git tree as given
// by a client ("src/main.go"). The empty path is the root. Rejected: a
// leading '/', a trailing '/', empty, "." or ".." components, NUL, newline
// and other control characters, backslashes, a leading '-' and any ".git"
// component (case-insensitive).
func ValidateTreePath(p string) error {
	if p == "" {
		return nil
	}
	if len(p) > MaxTreePath {
		return invalid("path", "too long")
	}
	if strings.HasPrefix(p, "-") {
		return invalid("path", "starts with '-'")
	}
	for i := 0; i < len(p); i++ {
		if c := p[i]; c < ' ' || c == 0x7f || c == '\\' {
			return invalid("path", "control character or backslash")
		}
	}
	for _, comp := range strings.Split(p, "/") {
		switch {
		case comp == "":
			return invalid("path", "empty component")
		case comp == "." || comp == "..":
			return invalid("path", "dot component")
		case strings.EqualFold(comp, ".git"):
			return invalid("path", ".git component")
		}
	}
	return nil
}

// MaxRepoName is the longest repository name.
const MaxRepoName = 100

// ValidateRepoName checks a repository name: 1-100 characters among
// [A-Za-z0-9._-], not "." or "..", not starting with '.' or '-' and not
// ending with ".git" (the suffix belongs to the clone URL).
func ValidateRepoName(name string) error {
	if name == "" || len(name) > MaxRepoName {
		return invalid("repository name", "must be 1-100 characters")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return invalid("repository name", "only letters, digits, '.', '_' and '-' are allowed")
		}
	}
	if name[0] == '.' || name[0] == '-' {
		return invalid("repository name", "must not start with '.' or '-'")
	}
	if strings.HasSuffix(strings.ToLower(name), ".git") {
		return invalid("repository name", "must not end with .git")
	}
	return nil
}

// ValidateObjectID checks a full SHA-1 (40) or SHA-256 (64) lowercase hex
// object id.
func ValidateObjectID(id string) error {
	if len(id) != 40 && len(id) != 64 {
		return invalid("object id", "must be 40 or 64 hex characters")
	}
	for i := 0; i < len(id); i++ {
		if c := id[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return invalid("object id", "must be lowercase hex")
		}
	}
	return nil
}

// ValidateRevision checks a revision a client may pass to read commands: a
// full object id, a full ref name or a branch/tag short name. Revision
// syntax (~, ^, @{}, :) is never accepted.
func ValidateRevision(rev string) error {
	if ValidateObjectID(rev) == nil || rev == "HEAD" {
		return nil
	}
	if strings.HasPrefix(rev, "refs/") {
		return ValidateRefName(rev)
	}
	return ValidateBranchName(rev)
}
