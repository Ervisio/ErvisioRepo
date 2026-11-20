package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

func (p problemResponse) VisitListErvisioCodeProjectsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitCreateErvisioCodeRemoteSessionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (s *v1Server) ListErvisioCodeProjects(ctx context.Context, req v1gen.ListErvisioCodeProjectsRequestObject) (v1gen.ListErvisioCodeProjectsResponseObject, error) {
	accountID, problem, err := s.remoteAccount(ctx, req.Params.AccountId)
	if err != nil {
		return nil, err
	}
	if problem.status != 0 {
		return problem, nil
	}
	active, err := s.remoteDevices(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return v1gen.ListErvisioCodeProjects200JSONResponse{Projects: s.remote.listProjects(accountID, active)}, nil
}

func (s *v1Server) CreateErvisioCodeRemoteSession(ctx context.Context, req v1gen.CreateErvisioCodeRemoteSessionRequestObject) (v1gen.CreateErvisioCodeRemoteSessionResponseObject, error) {
	accountID, problem, err := s.remoteAccount(ctx, req.Params.AccountId)
	if err != nil {
		return nil, err
	}
	if problem.status != 0 {
		return problem, nil
	}
	body := req.Body
	if body == nil || body.DeviceId == uuid.Nil || body.RelayId == uuid.Nil || body.ProjectId == uuid.Nil {
		return problemResponse{status: http.StatusBadRequest, detail: "invalid Remote create request"}, nil
	}
	reply, err := s.sendAuthorizedRemoteRequest(ctx, remoteHostKey{accountID: accountID, deviceID: body.DeviceId}, body.RelayId,
		v1gen.ErvisioCodeRelayRequest{Op: v1gen.RelayCreate, ProjectId: &body.ProjectId},
		func(ctx context.Context, repository v1gen.ErvisioCodeRemoteRepository) problemResponse {
			return s.authorizeRemoteRepository(ctx, accountID, req.Params.AccountId, repository)
		})
	if err != nil {
		return nil, err
	}
	if reply.problem.status != 0 {
		return reply.problem, nil
	}
	return v1gen.CreateErvisioCodeRemoteSession200JSONResponse{SessionId: reply.sessionID}, nil
}

func (s *v1Server) authorizeRemoteRepository(ctx context.Context, accountID uuid.UUID, selected *uuid.UUID, repository v1gen.ErvisioCodeRemoteRepository) problemResponse {
	owner, name, ok := strings.Cut(repository.Name, "/")
	if !ok {
		return errProjectAccess
	}
	switch repository.Provider {
	case v1gen.ErvisioCodeRemoteRepositoryProviderErvisiorepo:
		if s.identity == nil || s.repos == nil {
			return errProjectAccess
		}
		user, err := s.identity.User(ctx, accountID)
		if errors.Is(err, identity.ErrNotFound) {
			return errAuthRequired
		}
		if err != nil || s.repos.AuthorizeLinkedRepository(ctx, auth.Principal{UserID: accountID, Role: user.Role}, owner, name) != nil {
			return errProjectAccess
		}
	case v1gen.ErvisioCodeRemoteRepositoryProviderGithub:
		if s.github == nil || s.github.AuthorizeAccountRepository(ctx, accountID, owner, name) != nil {
			return errProjectAccess
		}
	default:
		return errProjectAccess
	}
	if _, problem, err := s.remoteAccount(ctx, selected); err != nil || problem.status != 0 {
		return errAuthRequired
	}
	return problemResponse{}
}

func remoteProjects(input *[]v1gen.ErvisioCodeRemoteProjectPublication) (map[uuid.UUID]v1gen.ErvisioCodeRemoteProjectPublication, bool) {
	if input == nil {
		return nil, true
	}
	if len(*input) > remoteMaxProjects {
		return nil, false
	}
	projects := make(map[uuid.UUID]v1gen.ErvisioCodeRemoteProjectPublication, len(*input))
	for _, project := range *input {
		label := strings.TrimSpace(project.Label)
		if project.ProjectId == uuid.Nil || label == "" || len(label) > 128 ||
			strings.ContainsAny(label, `/\:`) || strings.ContainsFunc(label, unicode.IsControl) {
			return nil, false
		}
		if _, exists := projects[project.ProjectId]; exists {
			return nil, false
		}
		project.Label = label
		projects[project.ProjectId] = project
	}
	return projects, true
}
