package identity

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Audit actions of the account self-service endpoints.
const (
	actionProfileUpdated  = "user.profile_updated"
	actionPasswordChanged = "user.password_changed"
	actionSessionRevoked  = "user.session_revoked"
	actionEmailAdded      = "user.email_added"
	actionEmailPrimary    = "user.email_primary"
	actionEmailRemoved    = "user.email_removed"
)

const (
	maxDisplayName = 100
	maxAvatarURL   = 2048
	maxEmails      = 10
)

// Errors of the account self-service operations.
var (
	ErrInvalidProfile = errors.New("invalid profile")
	ErrEmailTaken     = errors.New("this email address is already in use")
	ErrEmailLimit     = errors.New("too many email addresses")
	ErrPrimaryEmail   = errors.New("the primary email address cannot be removed")
	ErrUnverifiedMail = errors.New("verify the email address before making it primary")
)

// ProfileInput is the editable part of an account.
type ProfileInput struct {
	DisplayName string
	AvatarURL   string
	Locale      string
}

// SessionInfo is an active session of an account.
type SessionInfo struct {
	ID         uuid.UUID
	IP         string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// EmailAddress is an address of an account.
type EmailAddress struct {
	ID        uuid.UUID
	Address   string
	Primary   bool
	Verified  bool
	CreatedAt time.Time
}

// UpdateProfile changes the display name, avatar and language.
func (s *Service) UpdateProfile(ctx context.Context, userID uuid.UUID, in ProfileInput, c Client) (User, error) {
	name := strings.TrimSpace(in.DisplayName)
	if utf8.RuneCountInString(name) > maxDisplayName || strings.ContainsFunc(name, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return User{}, fmt.Errorf("%w: display name", ErrInvalidProfile)
	}
	avatar := strings.TrimSpace(in.AvatarURL)
	if avatar != "" {
		u, err := url.Parse(avatar)
		if err != nil || len(avatar) > maxAvatarURL || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return User{}, fmt.Errorf("%w: avatar must be an absolute https URL", ErrInvalidProfile)
		}
	}
	if in.Locale != "en" && in.Locale != "it" {
		return User{}, fmt.Errorf("%w: locale", ErrInvalidProfile)
	}
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.UpdateUserProfile(ctx, dbgen.UpdateUserProfileParams{ID: userID, DisplayName: name, AvatarUrl: avatar, Locale: in.Locale}); err != nil {
			return fmt.Errorf("update profile: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: actionProfileUpdated, TargetType: "user",
			TargetID: userID.String(), IP: c.IP})
	})
	if err != nil {
		return User{}, err
	}
	return s.User(ctx, userID)
}

// ChangePassword sets a new password. Accounts that have one must present
// it; accounts created through an external identity may set a first one.
// Every session except keep is revoked.
func (s *Service) ChangePassword(ctx context.Context, userID, keep uuid.UUID, current, next string, c Client) error {
	if err := s.checkPassword(next); err != nil {
		return err
	}
	has, err := s.q.UserHasPassword(ctx, userID)
	if err != nil {
		return fmt.Errorf("check password: %w", err)
	}
	if has {
		if err := s.VerifyPassword(ctx, userID, current); err != nil {
			return err
		}
	}
	hash, err := s.hasher.Hash(ctx, next)
	if err != nil {
		return err
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.UpsertPassword(ctx, dbgen.UpsertPasswordParams{UserID: userID, Hash: hash}); err != nil {
			return fmt.Errorf("store password: %w", err)
		}
		if _, err := q.DeleteUserSessionsExcept(ctx, dbgen.DeleteUserSessionsExceptParams{UserID: userID, Keep: keep}); err != nil {
			return fmt.Errorf("revoke sessions: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: actionPasswordChanged, TargetType: "user",
			TargetID: userID.String(), IP: c.IP})
	})
	if err != nil {
		return err
	}
	s.limiter.Reset(auth.AccountKey(userID.String()))
	return nil
}

// ListSessions returns the active sessions, most recently used first.
func (s *Service) ListSessions(ctx context.Context, userID uuid.UUID) ([]SessionInfo, error) {
	rows, err := s.q.ListUserSessions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	out := make([]SessionInfo, 0, len(rows))
	now := s.now()
	for _, r := range rows {
		if r.ExpiresAt.Valid && !r.ExpiresAt.Time.After(now) {
			continue
		}
		out = append(out, SessionInfo{ID: r.ID, IP: r.Ip, UserAgent: r.UserAgent, CreatedAt: r.CreatedAt.Time,
			LastSeenAt: r.LastSeenAt.Time, ExpiresAt: r.ExpiresAt.Time})
	}
	return out, nil
}

// RevokeSession ends one session of the account.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID, c Client) error {
	n, err := s.q.DeleteUserSessionByID(ctx, dbgen.DeleteUserSessionByIDParams{ID: sessionID, UserID: userID})
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	s.record(ctx, audit.Event{Actor: userID, Action: actionSessionRevoked, TargetType: "session",
		TargetID: sessionID.String(), IP: c.IP})
	return nil
}

// RevokeOtherSessions ends every session except keep.
func (s *Service) RevokeOtherSessions(ctx context.Context, userID, keep uuid.UUID, c Client) error {
	if _, err := s.q.DeleteUserSessionsExcept(ctx, dbgen.DeleteUserSessionsExceptParams{UserID: userID, Keep: keep}); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	s.record(ctx, audit.Event{Actor: userID, Action: actionSessionRevoked, TargetType: "user",
		TargetID: userID.String(), IP: c.IP})
	return nil
}

func emailDTO(id uuid.UUID, address string, primary bool, verified bool, created time.Time) EmailAddress {
	return EmailAddress{ID: id, Address: address, Primary: primary, Verified: verified, CreatedAt: created}
}

// ListEmails returns the addresses of the account, the primary one first.
func (s *Service) ListEmails(ctx context.Context, userID uuid.UUID) ([]EmailAddress, error) {
	rows, err := s.q.ListUserEmails(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list emails: %w", err)
	}
	out := make([]EmailAddress, 0, len(rows))
	for _, r := range rows {
		out = append(out, emailDTO(r.ID, r.Address, r.IsPrimary, r.VerifiedAt.Valid, r.CreatedAt.Time))
	}
	return out, nil
}

// AddEmail attaches an unverified address to the account and mails the
// verification link.
func (s *Service) AddEmail(ctx context.Context, userID uuid.UUID, address string, c Client) (EmailAddress, error) {
	addr, err := NormalizeEmail(address)
	if err != nil {
		return EmailAddress{}, err
	}
	if err := s.allowAction(c); err != nil {
		return EmailAddress{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return EmailAddress{}, fmt.Errorf("email id: %w", err)
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.CountUserEmails(ctx, userID)
		if err != nil {
			return fmt.Errorf("count emails: %w", err)
		}
		if n >= maxEmails {
			return ErrEmailLimit
		}
		if taken, err := q.EmailTaken(ctx, addr); err != nil {
			return fmt.Errorf("email taken: %w", err)
		} else if taken {
			return ErrEmailTaken
		}
		if err := q.InsertEmail(ctx, dbgen.InsertEmailParams{ID: id, UserID: userID, Address: addr}); err != nil {
			if isUniqueViolation(err) {
				return ErrEmailTaken
			}
			return fmt.Errorf("insert email: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: actionEmailAdded, TargetType: "email",
			TargetID: id.String(), IP: c.IP})
	})
	if err != nil {
		return EmailAddress{}, err
	}
	s.enqueue(ctx, VerifyEmailArgs{EmailID: id})
	return emailDTO(id, addr, false, false, s.now()), nil
}

// SetPrimaryEmail makes an address the primary one. With mail enabled it
// must be verified first.
func (s *Service) SetPrimaryEmail(ctx context.Context, userID, emailID uuid.UUID, c Client) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		e, err := q.GetUserEmail(ctx, dbgen.GetUserEmailParams{ID: emailID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get email: %w", err)
		}
		if e.IsPrimary {
			return nil
		}
		if s.mailOn && !e.VerifiedAt.Valid {
			return ErrUnverifiedMail
		}
		if err := q.ClearPrimaryEmail(ctx, userID); err != nil {
			return fmt.Errorf("clear primary: %w", err)
		}
		if _, err := q.SetPrimaryEmail(ctx, dbgen.SetPrimaryEmailParams{ID: emailID, UserID: userID}); err != nil {
			return fmt.Errorf("set primary: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: actionEmailPrimary, TargetType: "email",
			TargetID: emailID.String(), IP: c.IP})
	})
}

// DeleteEmail removes a non-primary address.
func (s *Service) DeleteEmail(ctx context.Context, userID, emailID uuid.UUID, c Client) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		e, err := q.GetUserEmail(ctx, dbgen.GetUserEmailParams{ID: emailID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get email: %w", err)
		}
		if e.IsPrimary {
			return ErrPrimaryEmail
		}
		if _, err := q.DeleteUserEmail(ctx, dbgen.DeleteUserEmailParams{ID: emailID, UserID: userID}); err != nil {
			return fmt.Errorf("delete email: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: actionEmailRemoved, TargetType: "email",
			TargetID: emailID.String(), IP: c.IP})
	})
}

// ResendEmailVerification mails a new link for an unverified address.
func (s *Service) ResendEmailVerification(ctx context.Context, userID, emailID uuid.UUID, c Client) error {
	e, err := s.q.GetUserEmail(ctx, dbgen.GetUserEmailParams{ID: emailID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get email: %w", err)
	}
	if err := s.allowAction(c); err != nil {
		return err
	}
	if !e.VerifiedAt.Valid {
		s.enqueue(ctx, VerifyEmailArgs{EmailID: emailID})
	}
	return nil
}
