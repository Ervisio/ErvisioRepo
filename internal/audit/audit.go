// Package audit appends security events to the audit_log table (PLAN §8.3).
// Events never carry secrets, tokens or passwords.
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Actions recorded by the authentication flows.
const (
	ActionRegister             = "auth.register"
	ActionRegisterDuplicate    = "auth.register_duplicate_email"
	ActionFirstAdmin           = "auth.first_admin"
	ActionLogin                = "auth.login"
	ActionLoginFailed          = "auth.login_failed"
	ActionLogout               = "auth.logout"
	ActionEmailVerified        = "auth.email_verified"
	ActionPasswordResetRequest = "auth.password_reset_requested"
	ActionPasswordReset        = "auth.password_reset"
	ActionPasswordRehash       = "auth.password_rehashed"
	ActionInviteCreated        = "auth.invite_created"
	ActionInviteRevoked        = "auth.invite_revoked"
)

// Actions recorded by the instance admin.
const (
	ActionAdminUserRole        = "admin.user_role_changed"
	ActionAdminUserSuspended   = "admin.user_suspended"
	ActionAdminUserUnsuspended = "admin.user_unsuspended"
	ActionAdminUser2FAReset    = "admin.user_2fa_reset"
)

// Actions recorded by organization management; their events carry OrgID.
const (
	ActionOrgCreated        = "org.created"
	ActionOrgUpdated        = "org.updated"
	ActionOrgDeleted        = "org.deleted"
	ActionOrgMemberAdded    = "org.member_added"
	ActionOrgMemberRole     = "org.member_role_changed"
	ActionOrgMemberRemoved  = "org.member_removed"
	ActionOrgTeamCreated    = "org.team_created"
	ActionOrgTeamUpdated    = "org.team_updated"
	ActionOrgTeamDeleted    = "org.team_deleted"
	ActionOrgTeamMemberSet  = "org.team_member_set"
	ActionOrgTeamMemberDrop = "org.team_member_removed"
	ActionOrgInviteCreated  = "org.invite_created"
	ActionOrgInviteRevoked  = "org.invite_revoked"
	ActionOrgInviteAccepted = "org.invite_accepted"
	ActionQuotaUpdated      = "quota.updated"
)

// Repository events.
const (
	ActionRepoCreated         = "repo.created"
	ActionRepoUpdated         = "repo.updated"
	ActionRepoRenamed         = "repo.renamed"
	ActionRepoTransferred     = "repo.transferred"
	ActionRepoDeleted         = "repo.deleted"
	ActionRepoCollaboratorSet = "repo.collaborator_set"
	ActionRepoCollaboratorDel = "repo.collaborator_removed"
)

// Inserter is the subset of dbgen.Querier used to record events.
type Inserter interface {
	InsertAuditEvent(ctx context.Context, arg dbgen.InsertAuditEventParams) error
}

// Event is one audit entry. A zero Actor means an anonymous caller.
type Event struct {
	Actor      uuid.UUID
	Action     string
	TargetType string
	TargetID   string
	IP         string
	Data       map[string]any
	// OrgID scopes the event to an organization (its audit log); zero for
	// instance-wide events.
	OrgID uuid.UUID
}

// Record appends e to the audit log.
func Record(ctx context.Context, q Inserter, e Event) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("audit id: %w", err)
	}
	data := []byte("{}")
	if len(e.Data) > 0 {
		if data, err = json.Marshal(e.Data); err != nil {
			return fmt.Errorf("audit data: %w", err)
		}
	}
	if err := q.InsertAuditEvent(ctx, dbgen.InsertAuditEventParams{
		ID: id, ActorID: uuid.NullUUID{UUID: e.Actor, Valid: e.Actor != uuid.Nil},
		Action: e.Action, TargetType: e.TargetType, TargetID: e.TargetID, Ip: e.IP, Data: data,
		OrgID: uuid.NullUUID{UUID: e.OrgID, Valid: e.OrgID != uuid.Nil},
	}); err != nil {
		return fmt.Errorf("insert audit event %s: %w", e.Action, err)
	}
	return nil
}
