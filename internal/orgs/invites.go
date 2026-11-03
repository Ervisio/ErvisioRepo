package orgs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

// MaxInviteTeams limits the teams an invitation can add the user to.
const MaxInviteTeams = 20

// Invite is a pending invitation.
type Invite struct {
	ID        uuid.UUID
	Email     string
	Role      string
	Teams     []string
	InvitedBy string // empty when the inviter was deleted
	ExpiresAt time.Time
	CreatedAt time.Time
	// Token is set only at creation, when mail is disabled: the inviter
	// hands the link over themselves.
	Token string
}

// InviteInput is a new invitation.
type InviteInput struct {
	Email string
	Role  string
	Teams []string
}

// teamIDs resolves team names of the organization.
func (s *Service) teamIDs(ctx context.Context, q *dbgen.Queries, org uuid.UUID, names []string) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(names))
	seen := map[uuid.UUID]bool{}
	for _, n := range names {
		t, err := s.team(ctx, q, org, n)
		if errors.Is(err, ErrNotFound) {
			return nil, invalid("no team named %q", n)
		}
		if err != nil {
			return nil, err
		}
		if !seen[t.ID] {
			seen[t.ID] = true
			ids = append(ids, t.ID)
		}
	}
	return ids, nil
}

func teamNames(ctx context.Context, q *dbgen.Queries, org uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := q.ListTeams(ctx, org)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	m := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		m[r.ID] = r.Name
	}
	return m, nil
}

func namesOf(ids []uuid.UUID, names map[uuid.UUID]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if n, ok := names[id]; ok { // deleted teams are dropped
			out = append(out, n)
		}
	}
	return out
}

// CreateInvite invites an email address to join the organization (owners
// and admins; only owners invite owners). With mail enabled the link is
// mailed, otherwise it is returned in Invite.Token.
func (s *Service) CreateInvite(ctx context.Context, who auth.Principal, org string, in InviteInput, c identity.Client) (Invite, error) {
	email, err := identity.NormalizeEmail(in.Email)
	if err != nil {
		return Invite{}, &ValidationError{Msg: err.Error()}
	}
	role, err := authz.ParseOrgRole(in.Role)
	if err != nil {
		return Invite{}, &ValidationError{Msg: err.Error()}
	}
	if len(in.Teams) > MaxInviteTeams {
		return Invite{}, invalid("an invitation can add at most %d teams", MaxInviteTeams)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Invite{}, fmt.Errorf("invite id: %w", err)
	}
	out := Invite{ID: id, Email: email, Role: string(role), Teams: []string{}, InvitedBy: who.Username}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		sc, err := s.load(ctx, q, who, org)
		if err != nil {
			return err
		}
		if !sc.actor.CanManage() || !sc.actor.CanAssignRole(authz.OrgNone, role) {
			return ErrForbidden
		}
		ids, err := s.teamIDs(ctx, q, sc.org.ID, in.Teams)
		if err != nil {
			return err
		}
		names, err := teamNames(ctx, q, sc.org.ID)
		if err != nil {
			return err
		}
		out.Teams = namesOf(ids, names)
		var hash []byte
		if !s.mailOn {
			out.Token, hash = auth.NewToken()
		}
		now := s.now()
		out.CreatedAt, out.ExpiresAt = now, now.Add(s.inviteTTL)
		if _, err := q.DeleteStaleOrgInvite(ctx, dbgen.DeleteStaleOrgInviteParams{OrgID: sc.org.ID, Email: email, Now: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
			return fmt.Errorf("delete stale invite: %w", err)
		}
		if err := q.InsertOrgInvite(ctx, dbgen.InsertOrgInviteParams{
			ID: id, OrgID: sc.org.ID, Email: email, Role: string(role), TeamIds: ids, TokenHash: hash,
			InvitedBy: uuid.NullUUID{UUID: who.UserID, Valid: who.UserID != uuid.Nil},
			ExpiresAt: pgtype.Timestamptz{Time: out.ExpiresAt, Valid: true},
		}); err != nil {
			if isUniqueViolation(err) {
				return ErrInvitePending
			}
			return fmt.Errorf("insert invite: %w", err)
		}
		// The address stays out of the audit data: the invite id points to it.
		return audit.Record(ctx, q, sc.event(audit.ActionOrgInviteCreated, "org_invite", id.String(), c,
			map[string]any{"role": string(role), "teams": out.Teams}))
	})
	if err != nil {
		return Invite{}, err
	}
	s.enqueue(ctx, OrgInviteArgs{InviteID: id})
	return out, nil
}

// ListInvites lists the pending invitations (owners and admins).
func (s *Service) ListInvites(ctx context.Context, who auth.Principal, org string) ([]Invite, error) {
	sc, err := s.load(ctx, s.q, who, org)
	if err != nil {
		return nil, err
	}
	if !sc.actor.CanManage() {
		return nil, ErrForbidden
	}
	rows, err := s.q.ListOrgInvites(ctx, sc.org.ID)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	names, err := teamNames(ctx, s.q, sc.org.ID)
	if err != nil {
		return nil, err
	}
	out := make([]Invite, 0, len(rows))
	for _, r := range rows {
		out = append(out, Invite{ID: r.ID, Email: r.Email, Role: r.Role, Teams: namesOf(r.TeamIds, names),
			InvitedBy: r.InvitedByName.String, ExpiresAt: r.ExpiresAt.Time, CreatedAt: r.CreatedAt.Time})
	}
	return out, nil
}

// RevokeInvite deletes a pending invitation (owners and admins).
func (s *Service) RevokeInvite(ctx context.Context, who auth.Principal, org string, id uuid.UUID, c identity.Client) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		sc, err := s.load(ctx, q, who, org)
		if err != nil {
			return err
		}
		if !sc.actor.CanManage() {
			return ErrForbidden
		}
		n, err := q.DeleteOrgInvite(ctx, dbgen.DeleteOrgInviteParams{ID: id, OrgID: sc.org.ID})
		if err != nil {
			return fmt.Errorf("delete invite: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, q, sc.event(audit.ActionOrgInviteRevoked, "org_invite", id.String(), c, nil))
	})
}

// Accepted is the result of accepting an invitation.
type Accepted struct {
	Org  string
	Role string
}

// AcceptInvite adds the caller to the organization of the invitation. The
// invited address must belong to the caller (and be verified when the
// instance requires it). A caller who is already a member keeps their role.
func (s *Service) AcceptInvite(ctx context.Context, who auth.Principal, token string, c identity.Client) (Accepted, error) {
	if !auth.ValidTokenFormat(token) {
		return Accepted{}, ErrInvalidToken
	}
	var out Accepted
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		inv, err := q.GetOrgInviteByToken(ctx, auth.HashToken(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return fmt.Errorf("get invite: %w", err)
		}
		if !s.now().Before(inv.ExpiresAt.Time) {
			return ErrInvalidToken
		}
		has, err := q.UserHasEmail(ctx, dbgen.UserHasEmailParams{UserID: who.UserID, Address: inv.Email})
		if err != nil {
			return fmt.Errorf("check email: %w", err)
		}
		if !has.Owned {
			return ErrEmailMismatch
		}
		if s.verifyRequired && !has.Verified {
			return ErrEmailNotVerified
		}
		if err := q.LockOrgMembers(ctx, inv.OrgID); err != nil {
			return fmt.Errorf("lock members: %w", err)
		}
		added, err := q.InsertOrgMember(ctx, dbgen.InsertOrgMemberParams{OrgID: inv.OrgID, UserID: who.UserID, Role: inv.Role})
		if err != nil {
			return fmt.Errorf("insert member: %w", err)
		}
		role, err := s.roleOf(ctx, q, inv.OrgID, who.UserID)
		if err != nil {
			return err
		}
		if len(inv.TeamIds) > 0 {
			if err := q.AddTeamMembers(ctx, dbgen.AddTeamMembersParams{UserID: who.UserID, OrgID: inv.OrgID, TeamIds: inv.TeamIds}); err != nil {
				return fmt.Errorf("add team members: %w", err)
			}
		}
		if _, err := q.MarkOrgInviteAccepted(ctx, dbgen.MarkOrgInviteAcceptedParams{ID: inv.ID,
			AcceptedBy: uuid.NullUUID{UUID: who.UserID, Valid: true}}); err != nil {
			return fmt.Errorf("mark invite accepted: %w", err)
		}
		out = Accepted{Org: inv.OrgName, Role: string(role)}
		sc := scope{org: dbgen.GetOrgDetailsRow{ID: inv.OrgID}, who: who}
		if err := audit.Record(ctx, q, sc.event(audit.ActionOrgInviteAccepted, "org_invite", inv.ID.String(), c,
			map[string]any{"role": string(role), "already_member": added == 0})); err != nil {
			return err
		}
		if added == 0 {
			return nil
		}
		return audit.Record(ctx, q, sc.event(audit.ActionOrgMemberAdded, "user", who.UserID.String(), c,
			map[string]any{"role": inv.Role, "via": "invite"}))
	})
	return out, err
}
