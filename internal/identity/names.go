// Package identity manages users, their email addresses and organizations,
// and implements the account flows (registration, login, email
// verification, password reset, invitations) on top of internal/auth.
package identity

import (
	"errors"
	"net/mail"
	"strings"
)

// MaxNameLength is the longest user or organization name.
const MaxNameLength = 39

// MaxEmailLength is the longest accepted address (RFC 5321 path limit).
const MaxEmailLength = 254

// Validation errors.
var (
	ErrInvalidName  = errors.New("names use letters, digits and single hyphens between them, at most 39 characters")
	ErrReservedName = errors.New("this name is reserved")
	ErrInvalidEmail = errors.New("invalid email address")
)

// reserved are names that cannot be taken by users or organizations, because
// they collide with routes of the web UI or the API, or impersonate the
// instance. Compared case-insensitively.
var reserved = map[string]struct{}{}

func init() {
	for _, n := range strings.Fields(`
		about abuse account accounts admin administrator admins api app apps assets auth blog
		docs explore favicon git github healthz help home info issues join login logout
		mail marketplace me metrics new noreply no-reply notifications null org orgs
		organization organizations owner packages postmaster privacy pulls raw readyz register
		registry repo repos root security settings signin signout signup site static status
		staff support system team teams terms undefined user users verify-email reset-password
		webmaster www ervisio ervisio-repo erepo`) {
		reserved[n] = struct{}{}
	}
}

// IsReserved reports whether name is reserved.
func IsReserved(name string) bool {
	_, ok := reserved[strings.ToLower(name)]
	return ok
}

// ValidateName checks a user or organization name: 1 to 39 ASCII letters or
// digits, with single hyphens between them, not reserved. Users and
// organizations share one namespace (enforced by the namespaces table).
func ValidateName(name string) error {
	if len(name) == 0 || len(name) > MaxNameLength {
		return ErrInvalidName
	}
	prevHyphen := true // forbids a leading hyphen
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			prevHyphen = false
		case c == '-' && !prevHyphen:
			prevHyphen = true
		default:
			return ErrInvalidName
		}
	}
	if prevHyphen {
		return ErrInvalidName // trailing hyphen
	}
	if IsReserved(name) {
		return ErrReservedName
	}
	return nil
}

// NormalizeEmail validates a bare address ("user@example.com", no display
// name) and returns it trimmed. Case is preserved; uniqueness is
// case-insensitive.
func NormalizeEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) < 3 || len(s) > MaxEmailLength {
		return "", ErrInvalidEmail
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s {
		return "", ErrInvalidEmail
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" || domain == "" || strings.ContainsAny(s, " \t\r\n<>\"") || !strings.Contains(domain, ".") && domain != "localhost" {
		return "", ErrInvalidEmail
	}
	return s, nil
}
