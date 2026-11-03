// Package orgs implements organizations, their members, teams and
// invitations (PLAN §5.3). Authorization rules live in internal/authz; this
// package loads the facts, applies the rules and records audit events.
package orgs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
)

// Errors returned by the service; their messages are safe to show.
var (
	// ErrNotFound hides organizations, teams and members the caller cannot see.
	ErrNotFound         = errors.New("not found")
	ErrForbidden        = errors.New("you do not have permission to do this")
	ErrNameTaken        = errors.New("this name is already taken")
	ErrLastOwner        = errors.New("an organization must keep at least one owner")
	ErrNotMember        = errors.New("the user is not a member of the organization")
	ErrInvitePending    = errors.New("an invitation for this address is already pending")
	ErrInvalidToken     = errors.New("the invitation is invalid or expired")
	ErrEmailMismatch    = errors.New("the invitation was sent to an email address of another account")
	ErrEmailNotVerified = errors.New("verify the invited email address before accepting")
)

// ValidationError is an invalid input; its message is safe to show.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// Limits of free-text fields.
const (
	MaxDisplayName = 100
	MaxDescription = 1000
)

// Service implements the organization flows.
type Service struct {
	pool           *pgxpool.Pool
	q              *dbgen.Queries
	jobs           identity.Enqueuer
	log            *slog.Logger
	mailOn         bool
	verifyRequired bool
	inviteTTL      time.Duration
	now            func() time.Time
}

// Options configures a Service.
type Options struct {
	Pool   *pgxpool.Pool
	Jobs   identity.Enqueuer // nil drops mail jobs (tests)
	Log    *slog.Logger
	Config *config.Config
	Now    func() time.Time // defaults to time.Now
}

// New builds a Service.
func New(o Options) *Service {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		pool: o.Pool, q: dbgen.New(o.Pool), jobs: o.Jobs, log: log,
		mailOn: o.Config.Mail.Enabled, verifyRequired: o.Config.Mail.Enabled && o.Config.Auth.RequireVerifiedEmail,
		inviteTTL: time.Duration(o.Config.Auth.InviteTTL), now: now,
	}
}

// MailEnabled reports whether invitations are sent by mail; otherwise their
// link is returned at creation.
func (s *Service) MailEnabled() bool { return s.mailOn }

// Org is an organization as seen by a caller.
type Org struct {
	ID             uuid.UUID
	Name           string
	DisplayName    string
	Description    string
	BasePermission string
	Require2FA     bool
	Verified       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// Role is the caller's role (empty for an instance admin who is not a member).
	Role authz.OrgRole
}

func orgFrom(r dbgen.GetOrgDetailsRow, role authz.OrgRole) Org {
	return Org{ID: r.ID, Name: r.Name, DisplayName: r.DisplayName, Description: r.Description,
		BasePermission: r.BasePermission, Require2FA: r.Require2fa, Verified: r.VerifiedAt.Valid, CreatedAt: r.CreatedAt.Time,
		UpdatedAt: r.UpdatedAt.Time, Role: role}
}

// Member is a member of an organization or a team.
type Member struct {
	UserID      uuid.UUID
	Username    string
	DisplayName string
	Role        string
	Since       time.Time
}

// scope is an organization resolved for a caller.
type scope struct {
	org   dbgen.GetOrgDetailsRow
	actor authz.Actor
	who   auth.Principal
}

// load resolves the organization name for the caller; organizations the
// caller cannot see are ErrNotFound.
func (s *Service) load(ctx context.Context, q *dbgen.Queries, who auth.Principal, name string) (scope, error) {
	org, err := q.GetOrgDetails(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return scope{}, ErrNotFound
	}
	if err != nil {
		return scope{}, fmt.Errorf("get org: %w", err)
	}
	role, err := s.roleOf(ctx, q, org.ID, who.UserID)
	if err != nil {
		return scope{}, err
	}
	sc := scope{org: org, actor: authz.Actor{Role: role, InstanceAdmin: who.IsAdmin()}, who: who}
	if !sc.actor.CanView() {
		return scope{}, ErrNotFound
	}
	return sc, nil
}

func (s *Service) roleOf(ctx context.Context, q *dbgen.Queries, org, user uuid.UUID) (authz.OrgRole, error) {
	if user == uuid.Nil {
		return authz.OrgNone, nil
	}
	r, err := q.GetOrgMemberRole(ctx, dbgen.GetOrgMemberRoleParams{OrgID: org, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		return authz.OrgNone, nil
	}
	if err != nil {
		return authz.OrgNone, fmt.Errorf("get member role: %w", err)
	}
	return authz.OrgRole(r), nil
}

// event builds an audit event scoped to the organization.
func (sc scope) event(action, targetType, targetID string, c identity.Client, data map[string]any) audit.Event {
	return audit.Event{Actor: sc.who.UserID, Action: action, TargetType: targetType, TargetID: targetID,
		IP: c.IP, Data: data, OrgID: sc.org.ID}
}

// CreateOrgInput is a new organization.
type CreateOrgInput struct {
	Name        string
	DisplayName string
	Description string
}

func checkText(field, v string, limit int) error {
	if utf8.RuneCountInString(v) > limit {
		return invalid("%s is longer than %d characters", field, limit)
	}
	if strings.ContainsAny(v, "\x00") {
		return invalid("%s contains invalid characters", field)
	}
	return nil
}

// CreateOrg creates an organization owned by the caller. Restricted users
// cannot create organizations.
func (s *Service) CreateOrg(ctx context.Context, who auth.Principal, in CreateOrgInput, c identity.Client) (Org, error) {
	if who.Role == auth.RoleRestricted {
		return Org{}, ErrForbidden
	}
	if err := identity.ValidateName(in.Name); err != nil {
		return Org{}, &ValidationError{Msg: err.Error()}
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if err := checkText("display name", in.DisplayName, MaxDisplayName); err != nil {
		return Org{}, err
	}
	if err := checkText("description", in.Description, MaxDescription); err != nil {
		return Org{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Org{}, fmt.Errorf("org id: %w", err)
	}
	var row dbgen.GetOrgDetailsRow
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.InsertNamespace(ctx, dbgen.InsertNamespaceParams{ID: id, Kind: "org", Name: in.Name}); err != nil {
			if isUniqueViolation(err) {
				return ErrNameTaken
			}
			return fmt.Errorf("insert namespace: %w", err)
		}
		if err := q.InsertOrg(ctx, dbgen.InsertOrgParams{ID: id, DisplayName: in.DisplayName, Description: in.Description}); err != nil {
			return fmt.Errorf("insert org: %w", err)
		}
		if _, err := q.InsertOrgMember(ctx, dbgen.InsertOrgMemberParams{OrgID: id, UserID: who.UserID, Role: string(authz.OrgOwner)}); err != nil {
			return fmt.Errorf("insert owner: %w", err)
		}
		if row, err = q.GetOrgDetails(ctx, in.Name); err != nil {
			return fmt.Errorf("get org: %w", err)
		}
		sc := scope{org: row, who: who}
		return audit.Record(ctx, q, sc.event(audit.ActionOrgCreated, "org", id.String(), c, map[string]any{"name": in.Name}))
	})
	if err != nil {
		return Org{}, err
	}
	return orgFrom(row, authz.OrgOwner), nil
}

// GetOrg returns an organization the caller can see.
func (s *Service) GetOrg(ctx context.Context, who auth.Principal, name string) (Org, error) {
	sc, err := s.load(ctx, s.q, who, name)
	if err != nil {
		return Org{}, err
	}
	return orgFrom(sc.org, sc.actor.Role), nil
}

// ListMyOrgs returns the organizations the caller is a member of.
func (s *Service) ListMyOrgs(ctx context.Context, who auth.Principal) ([]Org, error) {
	rows, err := s.q.ListUserOrgs(ctx, who.UserID)
	if err != nil {
		return nil, fmt.Errorf("list orgs: %w", err)
	}
	out := make([]Org, 0, len(rows))
	for _, r := range rows {
		out = append(out, orgFrom(dbgen.GetOrgDetailsRow{ID: r.ID, Name: r.Name, DisplayName: r.DisplayName,
			Description: r.Description, BasePermission: r.BasePermission, Require2fa: r.Require2fa, VerifiedAt: r.VerifiedAt,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}, authz.OrgRole(r.Role)))
	}
	return out, nil
}

// UpdateOrgInput changes the settings of an organization; nil fields stay.
type UpdateOrgInput struct {
	DisplayName    *string
	Description    *string
	BasePermission *string
	// Require2FA can only be changed by owners (and instance admins).
	Require2FA *bool
}

// UpdateOrg changes the settings (owners and admins).
func (s *Service) UpdateOrg(ctx context.Context, who auth.Principal, name string, in UpdateOrgInput, c identity.Client) (Org, error) {
	var out Org
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		sc, err := s.load(ctx, q, who, name)
		if err != nil {
			return err
		}
		if !sc.actor.CanManage() {
			return ErrForbidden
		}
		o := sc.org
		changed := []string{}
		if in.DisplayName != nil {
			v := strings.TrimSpace(*in.DisplayName)
			if err := checkText("display name", v, MaxDisplayName); err != nil {
				return err
			}
			o.DisplayName, changed = v, append(changed, "display_name")
		}
		if in.Description != nil {
			if err := checkText("description", *in.Description, MaxDescription); err != nil {
				return err
			}
			o.Description, changed = *in.Description, append(changed, "description")
		}
		if in.BasePermission != nil {
			p, err := authz.ParsePermission(*in.BasePermission)
			if err != nil {
				return &ValidationError{Msg: err.Error()}
			}
			o.BasePermission, changed = p.String(), append(changed, "base_permission")
		}
		if in.Require2FA != nil {
			if !sc.actor.CanDelete() {
				return ErrForbidden
			}
			o.Require2fa, changed = *in.Require2FA, append(changed, "require_2fa")
		}
		if err := q.UpdateOrg(ctx, dbgen.UpdateOrgParams{ID: o.ID, DisplayName: o.DisplayName,
			Description: o.Description, BasePermission: o.BasePermission, Require2fa: o.Require2fa}); err != nil {
			return fmt.Errorf("update org: %w", err)
		}
		if o, err = q.GetOrgDetails(ctx, name); err != nil {
			return fmt.Errorf("get org: %w", err)
		}
		out = orgFrom(o, sc.actor.Role)
		return audit.Record(ctx, q, sc.event(audit.ActionOrgUpdated, "org", o.ID.String(), c, map[string]any{"fields": changed}))
	})
	return out, err
}

// DeleteOrg deletes an organization with its members, teams and invitations
// (owners only).
func (s *Service) DeleteOrg(ctx context.Context, who auth.Principal, name string, c identity.Client) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		sc, err := s.load(ctx, q, who, name)
		if err != nil {
			return err
		}
		if !sc.actor.CanDelete() {
			return ErrForbidden
		}
		if _, err := q.DeleteOrgNamespace(ctx, sc.org.ID); err != nil {
			return fmt.Errorf("delete org: %w", err)
		}
		return audit.Record(ctx, q, sc.event(audit.ActionOrgDeleted, "org", sc.org.ID.String(), c, map[string]any{"name": sc.org.Name}))
	})
}

// ListMembers lists the members of an organization.
func (s *Service) ListMembers(ctx context.Context, who auth.Principal, name string) ([]Member, error) {
	sc, err := s.load(ctx, s.q, who, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListOrgMembers(ctx, sc.org.ID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	out := make([]Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, Member{UserID: r.UserID, Username: r.Username, DisplayName: r.DisplayName, Role: r.Role, Since: r.CreatedAt.Time})
	}
	return out, nil
}

// member resolves a username to a member of the organization.
func (s *Service) member(ctx context.Context, q *dbgen.Queries, org uuid.UUID, username string) (uuid.UUID, authz.OrgRole, error) {
	u, err := q.GetUserByUsername(ctx, username)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, authz.OrgNone, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, authz.OrgNone, fmt.Errorf("get user: %w", err)
	}
	role, err := s.roleOf(ctx, q, org, u.ID)
	if err != nil {
		return uuid.Nil, authz.OrgNone, err
	}
	return u.ID, role, nil
}

// SetMemberRole changes the role of a member. Only owners grant or revoke
// ownership, and the last owner cannot step down. New members join through
// invitations.
func (s *Service) SetMemberRole(ctx context.Context, who auth.Principal, name, username, role string, c identity.Client) (Member, error) {
	newRole, err := authz.ParseOrgRole(role)
	if err != nil {
		return Member{}, &ValidationError{Msg: err.Error()}
	}
	var out Member
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		sc, err := s.load(ctx, q, who, name)
		if err != nil {
			return err
		}
		if err := q.LockOrgMembers(ctx, sc.org.ID); err != nil {
			return fmt.Errorf("lock members: %w", err)
		}
		uid, cur, err := s.member(ctx, q, sc.org.ID, username)
		if err != nil {
			return err
		}
		if cur == authz.OrgNone {
			return ErrNotMember
		}
		if !sc.actor.CanAssignRole(cur, newRole) {
			return ErrForbidden
		}
		if cur == authz.OrgOwner && newRole != authz.OrgOwner {
			if err := lastOwnerCheck(ctx, q, sc.org.ID); err != nil {
				return err
			}
		}
		if cur != newRole {
			if _, err := q.SetOrgMemberRole(ctx, dbgen.SetOrgMemberRoleParams{OrgID: sc.org.ID, UserID: uid, Role: string(newRole)}); err != nil {
				return fmt.Errorf("set role: %w", err)
			}
			if err := audit.Record(ctx, q, sc.event(audit.ActionOrgMemberRole, "user", uid.String(), c,
				map[string]any{"from": string(cur), "to": string(newRole)})); err != nil {
				return err
			}
		}
		members, err := q.ListOrgMembers(ctx, sc.org.ID)
		if err != nil {
			return fmt.Errorf("list members: %w", err)
		}
		for _, m := range members {
			if m.UserID == uid {
				out = Member{UserID: m.UserID, Username: m.Username, DisplayName: m.DisplayName, Role: m.Role, Since: m.CreatedAt.Time}
			}
		}
		return nil
	})
	return out, err
}

func lastOwnerCheck(ctx context.Context, q *dbgen.Queries, org uuid.UUID) error {
	n, err := q.CountOrgOwners(ctx, org)
	if err != nil {
		return fmt.Errorf("count owners: %w", err)
	}
	if n <= 1 {
		return ErrLastOwner
	}
	return nil
}

// RemoveMember removes a member (or lets the caller leave). The last owner
// cannot be removed.
func (s *Service) RemoveMember(ctx context.Context, who auth.Principal, name, username string, c identity.Client) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		sc, err := s.load(ctx, q, who, name)
		if err != nil {
			return err
		}
		if err := q.LockOrgMembers(ctx, sc.org.ID); err != nil {
			return fmt.Errorf("lock members: %w", err)
		}
		uid, cur, err := s.member(ctx, q, sc.org.ID, username)
		if err != nil {
			return err
		}
		if cur == authz.OrgNone {
			return ErrNotMember
		}
		if !sc.actor.CanRemoveMember(cur, uid == who.UserID) {
			return ErrForbidden
		}
		if cur == authz.OrgOwner {
			if err := lastOwnerCheck(ctx, q, sc.org.ID); err != nil {
				return err
			}
		}
		if _, err := q.DeleteOrgMember(ctx, dbgen.DeleteOrgMemberParams{OrgID: sc.org.ID, UserID: uid}); err != nil {
			return fmt.Errorf("delete member: %w", err)
		}
		return audit.Record(ctx, q, sc.event(audit.ActionOrgMemberRemoved, "user", uid.String(), c,
			map[string]any{"role": string(cur), "self": uid == who.UserID}))
	})
}

// AuditLog returns the audit entries of an organization (owners and admins).
func (s *Service) AuditLog(ctx context.Context, who auth.Principal, name string, f audit.Filter) (audit.Page, error) {
	sc, err := s.load(ctx, s.q, who, name)
	if err != nil {
		return audit.Page{}, err
	}
	if !sc.actor.CanManage() {
		return audit.Page{}, ErrForbidden
	}
	f.OrgID = sc.org.ID
	return audit.List(ctx, s.q, f)
}

func (s *Service) tx(ctx context.Context, fn func(*dbgen.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// enqueue inserts a mail job; failures are logged, never shown.
func (s *Service) enqueue(ctx context.Context, args river.JobArgs) {
	if !s.mailOn || s.jobs == nil {
		return
	}
	if err := s.jobs.Enqueue(context.WithoutCancel(ctx), args); err != nil {
		s.log.ErrorContext(ctx, "enqueue mail job", "kind", args.Kind(), "error", err)
	}
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// Quota returns the limits and usage of an organization (owners and admins).
func (s *Service) Quota(ctx context.Context, who auth.Principal, name string) (quota.Status, error) {
	sc, err := s.load(ctx, s.q, who, name)
	if err != nil {
		return quota.Status{}, err
	}
	if !sc.actor.CanManage() {
		return quota.Status{}, ErrForbidden
	}
	return quota.Get(ctx, s.q, sc.org.ID)
}
