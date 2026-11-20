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
	ErrNotFound      = errors.New("ErvisioCode share or proposal not available")
	ErrInvalidInvite = errors.New("ErvisioCode invitation not available")

	opaqueID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

const (
	InviteTTL = 7 * 24 * time.Hour
	PageSize  = 50
)

type Role string

const (
	Reader   Role = "reader"
	Proposer Role = "proposer"
	Direct   Role = "direct" // No shared role authorizes execution in this slice.
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
	ID              uuid.UUID
	Target          Target
	RecipientID     uuid.UUID // nil for an organization grant
	OrgID           uuid.UUID // nil for a personal share
	Role            Role
	History         History
	CreatedAt       time.Time
	InviteExpiresAt time.Time
	AcceptedAt      time.Time
	RevokedAt       time.Time
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
	switch in.Role {
	case Reader, Proposer, Direct:
	default:
		return Share{}, ErrInvalid
	}
	if in.Role == Direct && !in.History.FullHistory {
		return Share{}, ErrInvalid
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
	return shareFromRow(row), queryError("create share", err)
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
	return shareFromRow(row), queryError("accept invite", err)
}

func (s *Service) RevokeShare(ctx context.Context, accountID, shareID uuid.UUID) error {
	n, err := s.q.RevokeErvisioCodeShare(ctx, dbgen.RevokeErvisioCodeShareParams{ActorID: accountID, ID: shareID})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return queryError("revoke share", err)
}

// GetActiveShare rechecks membership and device validity without requiring the
// host to be online. The host must still enforce the stored history boundary.
func (s *Service) GetActiveShare(ctx context.Context, accountID, shareID uuid.UUID) (Share, error) {
	row, err := s.q.GetActiveErvisioCodeShare(ctx, dbgen.GetActiveErvisioCodeShareParams{ActorID: accountID, ID: shareID})
	return shareFromRow(row), queryError("get active share", err)
}

// ListShares includes owned shares, active received shares and valid incoming invites.
// Use the last ID as beforeID for the next page.
func (s *Service) ListShares(ctx context.Context, accountID, beforeID uuid.UUID) ([]Share, error) {
	rows, err := s.q.ListErvisioCodeShares(ctx, dbgen.ListErvisioCodeSharesParams{
		ActorID: accountID, BeforeID: nullableID(beforeID), PageSize: PageSize,
	})
	if err != nil {
		return nil, queryError("list shares", err)
	}
	out := make([]Share, 0, len(rows))
	for _, row := range rows {
		out = append(out, shareFromRow(row))
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

func shareFromRow(r dbgen.ErvisiocodeShare) Share {
	return Share{
		ID: r.ID, Target: Target{OwnerID: r.OwnerID, DeviceID: r.DeviceID, SessionID: r.SessionID},
		RecipientID: r.RecipientID.UUID, OrgID: r.OrgID.UUID, Role: Role(r.Role),
		History: History{FullHistory: r.FullHistory, AfterMessageID: r.AfterMessageID.String,
			FromMessageID: r.FromMessageID.String, HostCursor: r.HostCursor.String},
		CreatedAt: r.CreatedAt.Time, InviteExpiresAt: r.InviteExpiresAt.Time,
		AcceptedAt: r.AcceptedAt.Time, RevokedAt: r.RevokedAt.Time,
	}
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
