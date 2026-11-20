package codeshare

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

var (
	ErrInvalid       = errors.New("invalid ErvisioCode sharing input")
	ErrNotFound      = errors.New("ErvisioCode share not available")
	ErrInvalidInvite = errors.New("ErvisioCode invitation not available")
	ErrRoleDisabled  = errors.New("only reader grants are enabled")

	opaqueID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

const (
	InviteTTL = 7 * 24 * time.Hour
	PageSize  = 50
)

type Role string

const (
	Reader Role = "reader"
)

type Target struct {
	OwnerID   uuid.UUID
	DeviceID  uuid.UUID
	SessionID string
}

type History struct {
	FullHistory    bool
	AfterMessageID string // exclusive
	FromMessageID  string // inclusive
	HostCursor     string // must come from the bound host, not directly from client input
}

type Share struct {
	ID               uuid.UUID
	Target           Target
	SessionTitle     string
	OwnerUsername    string
	OwnerDisplayName string
	OwnerAvatarURL   string
	RecipientID      uuid.UUID // nil for an organization grant
	OrgID            uuid.UUID // nil for a personal share
	Role             Role
	History          History
	CreatedAt        time.Time
	InviteExpiresAt  time.Time
	AcceptedAt       time.Time
	RevokedAt        time.Time
}

type SharePage struct {
	Shares             []Share
	PendingInvitations int64
}

type SharedSession struct {
	Target  Target
	History History
}

type CreateShareInput struct {
	DeviceID  uuid.UUID
	SessionID string
	Role      Role
	History   History
}

type Invitation struct {
	Share Share
	Token string // returned only when created; never persisted in plaintext
}

type Service struct {
	q *dbgen.Queries
}

func New(db dbgen.DBTX) *Service {
	return &Service{q: dbgen.New(db)}
}

func (s *Service) CreatePersonalInvite(ctx context.Context, accountID, recipientID uuid.UUID, in CreateShareInput) (Invitation, error) {
	if recipientID == uuid.Nil || recipientID == accountID {
		return Invitation{}, ErrInvalid
	}
	secret, hash := auth.NewToken()
	share, err := s.createShare(ctx, accountID, recipientID, uuid.Nil, in, hash)
	if err != nil {
		return Invitation{}, err
	}
	return Invitation{Share: share, Token: secret}, nil
}

// CreateOrgGrant requires a current org owner or admin. The grant applies to
// current org members only; it does not grant membership.
func (s *Service) CreateOrgGrant(ctx context.Context, accountID, orgID uuid.UUID, in CreateShareInput) (Share, error) {
	if orgID == uuid.Nil {
		return Share{}, ErrInvalid
	}
	return s.createShare(ctx, accountID, uuid.Nil, orgID, in, nil)
}

func (s *Service) createShare(ctx context.Context, accountID, recipientID, orgID uuid.UUID, in CreateShareInput, hash []byte) (Share, error) {
	if accountID == uuid.Nil || in.DeviceID == uuid.Nil || !opaqueID.MatchString(in.SessionID) || !in.History.valid() {
		return Share{}, ErrInvalid
	}
	if in.Role != Reader {
		return Share{}, ErrRoleDisabled
	}
	var expiresAt pgtype.Timestamptz
	if recipientID != uuid.Nil {
		expiresAt = pgtype.Timestamptz{Time: time.Now().UTC().Add(InviteTTL), Valid: true}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Share{}, fmt.Errorf("generate share ID: %w", err)
	}
	row, err := s.q.CreateErvisioCodeShare(ctx, dbgen.CreateErvisioCodeShareParams{
		ID: id, OwnerID: accountID, DeviceID: in.DeviceID, SessionID: in.SessionID,
		RecipientID: nullableID(recipientID), OrgID: nullableID(orgID),
		Role: string(in.Role), FullHistory: in.History.FullHistory, InviteTokenHash: hash, InviteExpiresAt: expiresAt,
		AfterMessageID: nullableText(in.History.AfterMessageID), FromMessageID: nullableText(in.History.FromMessageID),
		HostCursor: nullableText(in.History.HostCursor),
	})
	return shareFromRow(dbgen.ListErvisioCodeSharesRow(row)), queryError("create share", err)
}

func (s *Service) AcceptPersonalInvite(ctx context.Context, accountID uuid.UUID, token string) (Share, error) {
	if accountID == uuid.Nil || !auth.ValidTokenFormat(token) {
		return Share{}, ErrInvalidInvite
	}
	return s.acceptPersonalInvite(ctx, accountID, uuid.Nil, auth.HashToken(token))
}

func (s *Service) AcceptPersonalInviteByID(ctx context.Context, accountID, shareID uuid.UUID) (Share, error) {
	if accountID == uuid.Nil || shareID == uuid.Nil {
		return Share{}, ErrInvalidInvite
	}
	return s.acceptPersonalInvite(ctx, accountID, shareID, nil)
}

func (s *Service) acceptPersonalInvite(ctx context.Context, accountID, shareID uuid.UUID, tokenHash []byte) (Share, error) {
	row, err := s.q.AcceptErvisioCodeShare(ctx, dbgen.AcceptErvisioCodeShareParams{
		RecipientID: nullableID(accountID), ShareID: nullableID(shareID), TokenHash: tokenHash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Share{}, ErrInvalidInvite
	}
	return shareFromRow(dbgen.ListErvisioCodeSharesRow(row)), queryError("accept invite", err)
}

func (s *Service) RejectPersonalInvite(ctx context.Context, accountID, shareID uuid.UUID) error {
	n, err := s.q.RejectErvisioCodeShare(ctx, dbgen.RejectErvisioCodeShareParams{RecipientID: accountID, ID: shareID})
	if err == nil && n == 0 {
		return ErrInvalidInvite
	}
	return queryError("reject invite", err)
}

func (s *Service) RevokeShare(ctx context.Context, accountID, shareID uuid.UUID) error {
	n, err := s.q.RevokeErvisioCodeShare(ctx, dbgen.RevokeErvisioCodeShareParams{ActorID: accountID, ID: shareID})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return queryError("revoke share", err)
}

// GetActiveSession rechecks membership and device validity without requiring the
// host to be online. The host must still enforce the stored history boundary.
func (s *Service) GetActiveSession(ctx context.Context, accountID, shareID uuid.UUID) (SharedSession, error) {
	row, err := s.q.GetActiveErvisioCodeShare(ctx, dbgen.GetActiveErvisioCodeShareParams{ActorID: accountID, ID: shareID})
	return SharedSession{
		Target:  Target{OwnerID: row.OwnerID, DeviceID: row.DeviceID, SessionID: row.SessionID},
		History: shareHistory(row.FullHistory, row.AfterMessageID, row.FromMessageID, row.HostCursor),
	}, queryError("get active share", err)
}

// ListShares includes owned shares, active received shares and valid incoming invites.
// Use the last ID as beforeID for the next page.
func (s *Service) ListShares(ctx context.Context, accountID, beforeID uuid.UUID) (SharePage, error) {
	rows, err := s.q.ListErvisioCodeShares(ctx, dbgen.ListErvisioCodeSharesParams{
		ActorID: accountID, BeforeID: nullableID(beforeID), PageSize: PageSize,
	})
	if err != nil {
		return SharePage{}, queryError("list shares", err)
	}
	count, err := s.q.CountErvisioCodePendingInvitations(ctx, accountID)
	if err != nil {
		return SharePage{}, queryError("count pending invitations", err)
	}
	out := SharePage{Shares: make([]Share, 0, len(rows)), PendingInvitations: count}
	for _, row := range rows {
		out.Shares = append(out.Shares, shareFromRow(row))
	}
	return out, nil
}

func (h History) valid() bool {
	choices := 0
	if h.FullHistory {
		choices++
	}
	for _, id := range []string{h.AfterMessageID, h.FromMessageID, h.HostCursor} {
		if id != "" {
			if !opaqueID.MatchString(id) {
				return false
			}
			choices++
		}
	}
	return choices == 1
}

func shareFromRow(r dbgen.ListErvisioCodeSharesRow) Share {
	return Share{
		ID: r.ID, Target: Target{OwnerID: r.OwnerID, DeviceID: r.DeviceID, SessionID: r.SessionID},
		SessionTitle: r.SessionTitle, OwnerUsername: r.OwnerUsername, OwnerDisplayName: r.OwnerDisplayName, OwnerAvatarURL: r.OwnerAvatarUrl,
		RecipientID: r.RecipientID.UUID, OrgID: r.OrgID.UUID, Role: Role(r.Role),
		History:   shareHistory(r.FullHistory, r.AfterMessageID, r.FromMessageID, r.HostCursor),
		CreatedAt: r.CreatedAt.Time, InviteExpiresAt: r.InviteExpiresAt.Time,
		AcceptedAt: r.AcceptedAt.Time, RevokedAt: r.RevokedAt.Time,
	}
}

func shareHistory(full bool, after, from, cursor pgtype.Text) History {
	return History{FullHistory: full, AfterMessageID: after.String, FromMessageID: from.String, HostCursor: cursor.String}
}

func nullableID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}

func nullableText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

func queryError(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
