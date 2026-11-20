package httpapi

import (
	"context"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

var errProposalsPaused = problemResponse{status: http.StatusForbidden, detail: "ErvisioCode proposals are paused"}

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
	_, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	return errProposalsPaused, nil
}

func (s *v1Server) CreateErvisioCodeProposal(ctx context.Context, req v1gen.CreateErvisioCodeProposalRequestObject) (v1gen.CreateErvisioCodeProposalResponseObject, error) {
	_, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	return errProposalsPaused, nil
}

func (s *v1Server) ReviewErvisioCodeProposal(ctx context.Context, req v1gen.ReviewErvisioCodeProposalRequestObject) (v1gen.ReviewErvisioCodeProposalResponseObject, error) {
	_, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	return errProposalsPaused, nil
}
