package httpapi

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/codeshare"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

func (p problemResponse) VisitActOnErvisioCodeCollaborationResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitCheckErvisioCodeCollaborationResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (s *v1Server) ActOnErvisioCodeCollaboration(ctx context.Context, req v1gen.ActOnErvisioCodeCollaborationRequestObject) (v1gen.ActOnErvisioCodeCollaborationResponseObject, error) {
	accountID, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	body := req.Body
	if body == nil {
		return shareProblem(codeshare.ErrInvalid)
	}
	var op v1gen.ErvisioCodeRelayOp
	switch {
	case body.Op == v1gen.CollaborationPrompt && validRemotePrompt(body.Text):
		op = v1gen.RelayCollaborationPrompt
	case body.Op == v1gen.CollaborationInterrupt && body.Text == nil:
		op = v1gen.RelayCollaborationStop
	default:
		return shareProblem(codeshare.ErrInvalid)
	}
	share, err := s.shares.GetActiveSession(ctx, accountID, req.ShareId)
	if err != nil {
		return shareProblem(err)
	}
	reply, err := s.sendCollaborationRequest(ctx, accountID, req.Params.AccountId, req.ShareId, share, op, body.Text)
	if err != nil || reply.problem.status != 0 {
		return reply.problem, err
	}
	return v1gen.ActOnErvisioCodeCollaboration204Response{}, nil
}

func (s *v1Server) sendCollaborationRequest(ctx context.Context, accountID uuid.UUID, selected *uuid.UUID, shareID uuid.UUID, share codeshare.SharedSession, op v1gen.ErvisioCodeRelayOp, text *string) (remoteReply, error) {
	if share.Role != codeshare.Direct || !share.History.FullHistory {
		problem, err := shareProblem(codeshare.ErrNotFound)
		return remoteReply{problem: problem}, err
	}
	authorize := func(ctx context.Context) (problemResponse, error) {
		return s.recheckSharedSession(ctx, accountID, selected, shareID, share)
	}
	if problem, err := authorize(ctx); err != nil || problem.status != 0 {
		return remoteReply{problem: problem}, err
	}
	key := remoteHostKey{accountID: share.Target.OwnerID, deviceID: share.Target.DeviceID}
	request := v1gen.ErvisioCodeRelayRequest{Op: op, SessionId: &share.Target.SessionID, GrantId: &shareID, ContributorId: &accountID, Text: text}
	relayID, problem := s.remote.sessionRelay(key, request)
	if problem.status != 0 {
		return remoteReply{problem: problem}, nil
	}
	reply, err := s.sendGatedRemoteRequest(ctx, key, relayID, request, nil, authorize)
	if err != nil {
		return remoteReply{}, err
	}
	if problem, err := authorize(ctx); err != nil || problem.status != 0 {
		return remoteReply{problem: problem}, err
	}
	if activeRelay, problem := s.remote.sessionRelay(key, request); problem.status != 0 {
		return remoteReply{problem: problem}, nil
	} else if activeRelay != relayID {
		return remoteReply{problem: errRemoteOffline}, nil
	}
	return reply, nil
}

func (s *v1Server) CheckErvisioCodeCollaboration(ctx context.Context, req v1gen.CheckErvisioCodeCollaborationRequestObject) (v1gen.CheckErvisioCodeCollaborationResponseObject, error) {
	p, ok := caller(ctx)
	if !ok || p.Method != auth.MethodToken || !p.ErvisioCodeDevice {
		return errAuthRequired, nil
	}
	if s.shares == nil {
		return errCredentialsUnavailable, nil
	}
	body := req.Body
	if body == nil || body.RelayId == uuid.Nil || req.ShareId == uuid.Nil || body.ContributorId == uuid.Nil || !remoteSessionID.MatchString(body.SessionId) {
		return shareProblem(codeshare.ErrInvalid)
	}
	share, err := s.shares.GetActiveSession(ctx, body.ContributorId, req.ShareId)
	if err != nil {
		return shareProblem(err)
	}
	if share.Role != codeshare.Direct || !share.History.FullHistory ||
		share.Target != (codeshare.Target{OwnerID: p.UserID, DeviceID: p.TokenID, SessionID: body.SessionId}) {
		return shareProblem(codeshare.ErrNotFound)
	}
	key := remoteHostKey{accountID: p.UserID, deviceID: p.TokenID}
	request := v1gen.ErvisioCodeRelayRequest{Op: v1gen.RelayCollaborationState, SessionId: &body.SessionId}
	if relayID, problem := s.remote.sessionRelay(key, request); problem.status != 0 {
		return problem, nil
	} else if relayID != body.RelayId {
		return errRemoteOffline, nil
	}
	return v1gen.CheckErvisioCodeCollaboration204Response{}, nil
}
