package httpapi

import (
	"context"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/codeshare"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

func (p problemResponse) VisitListErvisioCodeProposalsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitCreateErvisioCodeProposalResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitReviewErvisioCodeProposalResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (s *v1Server) ListErvisioCodeProposals(ctx context.Context, req v1gen.ListErvisioCodeProposalsRequestObject) (v1gen.ListErvisioCodeProposalsResponseObject, error) {
	accountID, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	if _, err := s.shares.GetActiveShare(ctx, accountID, req.ShareId); err != nil {
		return shareProblem(err)
	}
	proposals, err := s.shares.ListProposals(ctx, accountID, req.ShareId, shareValue(req.Params.BeforeId))
	if err != nil {
		return shareProblem(err)
	}
	out := v1gen.ListErvisioCodeProposals200JSONResponse{Proposals: make([]v1gen.ErvisioCodeProposal, 0, len(proposals))}
	for _, proposal := range proposals {
		out.Proposals = append(out.Proposals, proposalDTO(proposal))
	}
	if len(proposals) == codeshare.PageSize {
		out.NextBeforeId = &proposals[len(proposals)-1].ID
	}
	return out, nil
}

func (s *v1Server) CreateErvisioCodeProposal(ctx context.Context, req v1gen.CreateErvisioCodeProposalRequestObject) (v1gen.CreateErvisioCodeProposalResponseObject, error) {
	accountID, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	if req.Body == nil {
		return shareProblem(codeshare.ErrInvalid)
	}
	proposal, err := s.shares.CreateProposal(ctx, accountID, req.ShareId, req.Body.Body)
	if err != nil {
		return shareProblem(err)
	}
	return v1gen.CreateErvisioCodeProposal201JSONResponse(proposalDTO(proposal)), nil
}

func (s *v1Server) ReviewErvisioCodeProposal(ctx context.Context, req v1gen.ReviewErvisioCodeProposalRequestObject) (v1gen.ReviewErvisioCodeProposalResponseObject, error) {
	accountID, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	if req.Body == nil {
		return shareProblem(codeshare.ErrInvalid)
	}
	proposal, err := s.shares.ReviewProposal(ctx, accountID, req.ShareId, req.ProposalId, codeshare.ProposalStatus(req.Body.Status))
	if err != nil {
		return shareProblem(err)
	}
	return v1gen.ReviewErvisioCodeProposal200JSONResponse(proposalDTO(proposal)), nil
}

func proposalDTO(proposal codeshare.Proposal) v1gen.ErvisioCodeProposal {
	return v1gen.ErvisioCodeProposal{
		Id: proposal.ID, ShareId: proposal.ShareID, AuthorId: proposal.AuthorID, Body: proposal.Body,
		Status: v1gen.ErvisioCodeProposalStatus(proposal.Status), CreatedAt: proposal.CreatedAt,
		ExpiresAt: proposal.ExpiresAt, ReviewedAt: shareOptional(proposal.ReviewedAt),
	}
}
