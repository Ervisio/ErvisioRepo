package identity

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Errors of the instance admin operations.
var (
	ErrSelfChange  = errors.New("administrators cannot demote or suspend themselves")
	ErrLastAdmin   = errors.New("the instance must keep at least one active administrator")
	ErrInvalidRole = errors.New("invalid role")
)

// Instance roles.
const (
	RoleAdmin      = "admin"
	RoleUser       = "user"
	RoleRestricted = "restricted"
)

// AdminUser is an account as listed by the instance admin.
type AdminUser struct {
	ID           uuid.UUID
	Username     string
	DisplayName  string
	Email        string
	Role         string
	SuspendedAt  *time.Time
	Has2FA       bool
	CreatedAt    time.Time
	LastActivity *time.Time
}

// AdminUserFilter selects the users of AdminListUsers.
type AdminUserFilter struct {
	Search    string
	Role      string // "" for any
	Suspended *bool  // nil for any
	Offset    int
	Limit     int
}

// AdminUserUpdate changes an account; nil fields stay as they are.
type AdminUserUpdate struct {
	Role      *string
	Suspended *bool
}

// InviteStatus values.
const (
	InvitePending = "pending"
	InviteUsed    = "used"
	InviteExpired = "expired"
)

// AdminInvite is an invitation without its token.
type AdminInvite struct {
	ID        uuid.UUID
	Email     string
	Status    string
	CreatedBy string
	UsedBy    string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// likePattern escapes s for a LIKE substring match.
// clamp32 bounds a paging value to the int32 range of the queries.
func clamp32(n int) int32 {
	return int32(max(0, min(n, math.MaxInt32))) //nolint:gosec // bounded above
}

func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(strings.ToLower(strings.TrimSpace(s)))
}

// AdminListUsers returns a page of users and the total matching the filter.
func (s *Service) AdminListUsers(ctx context.Context, f AdminUserFilter) ([]AdminUser, int64, error) {
	if f.Role != "" && f.Role != RoleAdmin && f.Role != RoleUser && f.Role != RoleRestricted {
		return nil, 0, ErrInvalidRole
	}
	p := dbgen.AdminListUsersParams{Search: likePattern(f.Search), Role: f.Role, Off: clamp32(f.Offset), Lim: clamp32(f.Limit)}
	if f.Suspended != nil {
		p.Suspended = pgtype.Bool{Bool: *f.Suspended, Valid: true}
	}
	rows, err := s.q.AdminListUsers(ctx, p)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	out := make([]AdminUser, 0, len(rows))
	var total int64
	for _, r := range rows {
		total = r.Total
		out = append(out, AdminUser{ID: r.ID, Username: r.Username, DisplayName: r.DisplayName, Email: r.Email,
			Role: r.Role, SuspendedAt: timePtr(r.SuspendedAt), Has2FA: r.Has2fa, CreatedAt: r.CreatedAt.Time,
			LastActivity: timePtr(r.LastActivity)})
	}
	return out, total, nil
}

// AdminGetUser returns one account by username.
func (s *Service) AdminGetUser(ctx context.Context, username string) (AdminUser, error) {
	r, err := s.q.AdminGetUser(ctx, username)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminUser{}, ErrNotFound
	}
	if err != nil {
		return AdminUser{}, fmt.Errorf("get user: %w", err)
	}
	return AdminUser{ID: r.ID, Username: r.Username, DisplayName: r.DisplayName, Email: r.Email,
		Role: r.Role, SuspendedAt: timePtr(r.SuspendedAt), Has2FA: r.Has2fa, CreatedAt: r.CreatedAt.Time,
		LastActivity: timePtr(r.LastActivity)}, nil
}

// AdminUpdateUser changes the role and/or suspension of an account. A
// suspension revokes the sessions and tokens of the account. An admin
// cannot demote or suspend itself, and the last active admin stays.
func (s *Service) AdminUpdateUser(ctx context.Context, actor uuid.UUID, username string, in AdminUserUpdate, c Client) (AdminUser, error) {
	if in.Role != nil && *in.Role != RoleAdmin && *in.Role != RoleUser && *in.Role != RoleRestricted {
		return AdminUser{}, ErrInvalidRole
	}
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.LockAdminChanges(ctx); err != nil {
			return fmt.Errorf("lock: %w", err)
		}
		u, err := q.AdminGetUser(ctx, username)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get user: %w", err)
		}
		demote := in.Role != nil && u.Role == RoleAdmin && *in.Role != RoleAdmin
		suspend := in.Suspended != nil && *in.Suspended && !u.SuspendedAt.Valid
		if (demote || suspend) && u.ID == actor {
			return ErrSelfChange
		}
		if (demote || suspend) && u.Role == RoleAdmin && !u.SuspendedAt.Valid {
			n, err := q.CountActiveAdmins(ctx)
			if err != nil {
				return fmt.Errorf("count admins: %w", err)
			}
			if n <= 1 {
				return ErrLastAdmin
			}
		}
		target := u.ID.String()
		if in.Role != nil && *in.Role != u.Role {
			if err := q.SetUserRole(ctx, dbgen.SetUserRoleParams{ID: u.ID, Role: *in.Role}); err != nil {
				return fmt.Errorf("set role: %w", err)
			}
			err := audit.Record(ctx, q, audit.Event{Actor: actor, Action: audit.ActionAdminUserRole, TargetType: "user",
				TargetID: target, IP: c.IP, Data: map[string]any{"from": u.Role, "to": *in.Role}})
			if err != nil {
				return err
			}
		}
		if in.Suspended != nil && *in.Suspended != u.SuspendedAt.Valid {
			if err := q.SetUserSuspended(ctx, dbgen.SetUserSuspendedParams{ID: u.ID, Suspended: *in.Suspended}); err != nil {
				return fmt.Errorf("set suspended: %w", err)
			}
			action := audit.ActionAdminUserUnsuspended
			if *in.Suspended {
				action = audit.ActionAdminUserSuspended
				if err := revokeAccess(ctx, q, u.ID); err != nil {
					return err
				}
			}
			if err := audit.Record(ctx, q, audit.Event{Actor: actor, Action: action, TargetType: "user",
				TargetID: target, IP: c.IP}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return AdminUser{}, err
	}
	return s.AdminGetUser(ctx, username)
}

// revokeAccess ends every session and deletes every token of an account.
func revokeAccess(ctx context.Context, q *dbgen.Queries, id uuid.UUID) error {
	if _, err := q.DeleteUserSessions(ctx, id); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	if _, err := q.DeleteUserTokens(ctx, id); err != nil {
		return fmt.Errorf("revoke tokens: %w", err)
	}
	if err := q.DeleteUserLFSTokens(ctx, id); err != nil {
		return fmt.Errorf("revoke lfs tokens: %w", err)
	}
	if err := q.DeleteUserOneTimeTokens(ctx, id); err != nil {
		return fmt.Errorf("revoke one-time tokens: %w", err)
	}
	if err := q.DeleteUserMFAChallenges(ctx, id); err != nil {
		return fmt.Errorf("revoke mfa challenges: %w", err)
	}
	return nil
}

// AdminReset2FA removes the TOTP, recovery codes and passkeys of an account.
func (s *Service) AdminReset2FA(ctx context.Context, actor uuid.UUID, username string, c Client) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		u, err := q.GetUserByUsername(ctx, username)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get user: %w", err)
		}
		if _, err := q.DeleteTOTP(ctx, u.ID); err != nil {
			return fmt.Errorf("delete totp: %w", err)
		}
		if err := q.DeleteRecoveryCodes(ctx, u.ID); err != nil {
			return fmt.Errorf("delete recovery codes: %w", err)
		}
		if _, err := q.DeleteUserWebAuthnCredentials(ctx, u.ID); err != nil {
			return fmt.Errorf("delete passkeys: %w", err)
		}
		if err := q.DeleteUserMFAChallenges(ctx, u.ID); err != nil {
			return fmt.Errorf("delete mfa challenges: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: actor, Action: audit.ActionAdminUser2FAReset, TargetType: "user",
			TargetID: u.ID.String(), IP: c.IP})
	})
}

// AdminListInvites returns a page of invitations and their total.
func (s *Service) AdminListInvites(ctx context.Context, offset, limit int) ([]AdminInvite, int64, error) {
	rows, err := s.q.AdminListInvites(ctx, dbgen.AdminListInvitesParams{Off: clamp32(offset), Lim: clamp32(limit)})
	if err != nil {
		return nil, 0, fmt.Errorf("list invites: %w", err)
	}
	now := s.now()
	out := make([]AdminInvite, 0, len(rows))
	var total int64
	for _, r := range rows {
		total = r.Total
		st := InvitePending
		switch {
		case r.UsedAt.Valid:
			st = InviteUsed
		case !r.ExpiresAt.Time.After(now):
			st = InviteExpired
		}
		out = append(out, AdminInvite{ID: r.ID, Email: r.Email.String, Status: st, CreatedBy: r.CreatedBy,
			UsedBy: r.UsedBy, CreatedAt: r.CreatedAt.Time, ExpiresAt: r.ExpiresAt.Time, UsedAt: timePtr(r.UsedAt)})
	}
	return out, total, nil
}

// AdminRevokeInvite deletes an invitation that was not used.
func (s *Service) AdminRevokeInvite(ctx context.Context, actor, id uuid.UUID, c Client) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		if _, err := q.DeleteUnusedInvite(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("revoke invite: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: actor, Action: audit.ActionInviteRevoked, TargetType: "invite",
			TargetID: id.String(), IP: c.IP})
	})
}
