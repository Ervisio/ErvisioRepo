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
	}); err != nil {
		return fmt.Errorf("insert audit event %s: %w", e.Action, err)
	}
	return nil
}
