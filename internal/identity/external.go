package identity

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// ErrExternalEmailTaken refuses to create an account for an external
// identity whose email address already belongs to an account: the owner of
// that account must sign in and link the identity, so that a matching email
// alone never grants access to an existing account.
var ErrExternalEmailTaken = errors.New("an account with this email address already exists; sign in and link the provider from the settings")

// ExternalAccount describes an account created at the first login of an
// external identity (OIDC auto-registration).
type ExternalAccount struct {
	// Username is the suggested name (a claim of the provider); it is
	// sanitized and suffixed on collision.
	Username    string
	DisplayName string
	// Email is the address claimed by the provider. It is stored, as
	// verified, only when EmailVerified is set.
	Email         string
	EmailVerified bool
	Locale        string
}

// RegisterExternal creates an account without password for an external
// identity. link runs in the same transaction, to attach the identity to
// the new account. The first account of the instance becomes its admin.
func (s *Service) RegisterExternal(ctx context.Context, in ExternalAccount, link func(q *dbgen.Queries, userID uuid.UUID) error, c Client) (uuid.UUID, error) {
	addr := ""
	if in.Email != "" {
		a, err := NormalizeEmail(in.Email)
		if err == nil {
			addr = a
		} else if in.EmailVerified {
			return uuid.Nil, err
		}
	}
	base := SanitizeName(in.Username)
	userID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("user id: %w", err)
	}
	var first bool
	var name string
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.AcquireRegistrationLock(ctx); err != nil {
			return fmt.Errorf("registration lock: %w", err)
		}
		if addr != "" {
			if taken, err := q.EmailTaken(ctx, addr); err != nil {
				return fmt.Errorf("email taken: %w", err)
			} else if taken {
				return ErrExternalEmailTaken
			}
		}
		n, err := q.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		first = n == 0
		if name, err = freeName(ctx, q, base); err != nil {
			return err
		}
		role := auth.RoleUser
		if first {
			role = auth.RoleAdmin
		}
		if err := q.InsertNamespace(ctx, dbgen.InsertNamespaceParams{ID: userID, Kind: "user", Name: name}); err != nil {
			return fmt.Errorf("insert namespace: %w", err)
		}
		display := strings.TrimSpace(in.DisplayName)
		if len(display) > 100 {
			display = ""
		}
		if err := q.InsertUser(ctx, dbgen.InsertUserParams{ID: userID, DisplayName: display, Role: role, Locale: normLocale(in.Locale)}); err != nil {
			return fmt.Errorf("insert user: %w", err)
		}
		if addr != "" && in.EmailVerified {
			emailID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("email id: %w", err)
			}
			if err := q.InsertEmail(ctx, dbgen.InsertEmailParams{ID: emailID, UserID: userID, Address: addr, IsPrimary: true,
				VerifiedAt: pgtype.Timestamptz{Time: s.now(), Valid: true}}); err != nil {
				if isUniqueViolation(err) {
					return ErrExternalEmailTaken
				}
				return fmt.Errorf("insert email: %w", err)
			}
		}
		if err := link(q, userID); err != nil {
			return err
		}
		if err := audit.Record(ctx, q, audit.Event{Actor: userID, Action: audit.ActionRegister, TargetType: "user",
			TargetID: userID.String(), IP: c.IP, Data: map[string]any{"username": name, "method": "external"}}); err != nil {
			return err
		}
		if first {
			return audit.Record(ctx, q, audit.Event{Actor: userID, Action: audit.ActionFirstAdmin, TargetType: "user",
				TargetID: userID.String(), IP: c.IP})
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	if first {
		s.log.InfoContext(ctx, "first user registered as instance admin", "username", name)
	}
	return userID, nil
}

// freeName returns base, or base-2, base-3... if taken.
func freeName(ctx context.Context, q *dbgen.Queries, base string) (string, error) {
	for i := 1; i <= 100; i++ {
		name := base
		if i > 1 {
			sfx := "-" + strconv.Itoa(i)
			name = strings.TrimRight(base[:min(len(base), MaxNameLength-len(sfx))], "-") + sfx
		}
		if ValidateName(name) != nil {
			continue
		}
		taken, err := q.NamespaceTaken(ctx, name)
		if err != nil {
			return "", fmt.Errorf("namespace taken: %w", err)
		}
		if !taken {
			return name, nil
		}
	}
	return "", ErrUsernameTaken
}

// SanitizeName turns a name suggested by an external provider into a valid
// user name: other characters become hyphens, runs of hyphens collapse, and
// the result is cut to MaxNameLength. An unusable or reserved suggestion
// yields "member".
func SanitizeName(s string) string {
	if at := strings.IndexByte(s, '@'); at >= 0 {
		s = s[:at]
	}
	var b strings.Builder
	hyphen := true
	for i := 0; i < len(s) && b.Len() < MaxNameLength; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteByte(c)
			hyphen = false
		case !hyphen:
			b.WriteByte('-')
			hyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" || ValidateName(out) != nil {
		return "member"
	}
	return out
}
