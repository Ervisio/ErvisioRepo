package codeshare

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

const MaxProposalBytes = 16384

type ProposalStatus string

const (
	Pending  ProposalStatus = "pending"
	Approved ProposalStatus = "approved"
	Rejected ProposalStatus = "rejected"
)

type Proposal struct {
	ID         uuid.UUID
	ShareID    uuid.UUID
	AuthorID   uuid.UUID
	Body       string
	Status     ProposalStatus
	CreatedAt  time.Time
	ExpiresAt  time.Time
	ReviewedAt time.Time
}

func (s *Service) CreateProposal(ctx context.Context, accountID, shareID uuid.UUID, body string) (Proposal, error) {
	if accountID == uuid.Nil || shareID == uuid.Nil || len(body) > MaxProposalBytes ||
		!utf8.ValidString(body) || strings.TrimSpace(body) == "" || strings.ContainsRune(body, 0) {
		return Proposal{}, ErrInvalid
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Proposal{}, fmt.Errorf("generate proposal ID: %w", err)
	}
	row, err := s.q.CreateErvisioCodeProposal(ctx, dbgen.CreateErvisioCodeProposalParams{
		ID: id, ShareID: shareID, AuthorID: accountID, Body: body,
	})
	return proposalFromRow(row), queryError("create proposal", err)
}

// ListProposals shows the owner all proposals; recipients only see their own.
func (s *Service) ListProposals(ctx context.Context, accountID, shareID, beforeID uuid.UUID) ([]Proposal, error) {
	rows, err := s.q.ListErvisioCodeProposals(ctx, dbgen.ListErvisioCodeProposalsParams{
		ActorID: accountID, ShareID: shareID, BeforeID: nullableID(beforeID), PageSize: PageSize,
	})
	if err != nil {
		return nil, queryError("list proposals", err)
	}
	out := make([]Proposal, 0, len(rows))
	for _, row := range rows {
		out = append(out, proposalFromRow(row))
	}
	return out, nil
}

// ReviewProposal records an owner's decision; approval never dispatches or
// queues a prompt, including when the host comes back online.
func (s *Service) ReviewProposal(ctx context.Context, ownerID, shareID, proposalID uuid.UUID, status ProposalStatus) (Proposal, error) {
	if status != Approved && status != Rejected {
		return Proposal{}, ErrInvalid
	}
	row, err := s.q.ReviewErvisioCodeProposal(ctx, dbgen.ReviewErvisioCodeProposalParams{
		OwnerID: ownerID, ShareID: shareID, ID: proposalID, Status: string(status),
	})
	return proposalFromRow(row), queryError("review proposal", err)
}

func proposalFromRow(r dbgen.ErvisiocodeProposal) Proposal {
	return Proposal{
		ID: r.ID, ShareID: r.ShareID, AuthorID: r.AuthorID, Body: r.Body,
		Status: ProposalStatus(r.Status), CreatedAt: r.CreatedAt.Time, ExpiresAt: r.ExpiresAt.Time, ReviewedAt: r.ReviewedAt.Time,
	}
}
