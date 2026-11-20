package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/codeshare"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

func (p problemResponse) VisitListErvisioCodeSharesResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitCreateErvisioCodeShareResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitAcceptErvisioCodeShareResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitAcceptErvisioCodeShareByIdResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitRevokeErvisioCodeShareResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetErvisioCodeSharedStateResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (s *v1Server) sharingAccount(ctx context.Context, selected *uuid.UUID) (uuid.UUID, problemResponse, error) {
	if s.shares == nil || s.creds == nil {
		return uuid.Nil, errCredentialsUnavailable, nil
	}
	if selected != nil {
		return s.remoteHubAccount(ctx, *selected)
	}
	if p, ok := caller(ctx); ok && p.Method == auth.MethodSession {
		return p.UserID, problemResponse{}, nil
	}
	return uuid.Nil, errAuthRequired, nil
}

func (s *v1Server) recheckSharingAccount(ctx context.Context, accountID uuid.UUID, selected *uuid.UUID) (problemResponse, error) {
	if selected != nil {
		_, problem, err := s.sharingAccount(ctx, selected)
		return problem, err
	}
	secret := auth.RequestInfoFrom(ctx).SessionToken
	if s.sessions == nil || secret == "" {
		return errAuthRequired, nil
	}
	session, err := s.sessions.Lookup(ctx, secret)
	if errors.Is(err, auth.ErrNoSession) || err == nil && session.UserID != accountID {
		return errAuthRequired, nil
	}
	return problemResponse{}, err
}

func (s *v1Server) ListErvisioCodeShares(ctx context.Context, req v1gen.ListErvisioCodeSharesRequestObject) (v1gen.ListErvisioCodeSharesResponseObject, error) {
	accountID, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	shares, err := s.shares.ListShares(ctx, accountID, shareValue(req.Params.BeforeId))
	if err != nil {
		return shareProblem(err)
	}
	out := v1gen.ListErvisioCodeShares200JSONResponse{Shares: make([]v1gen.ErvisioCodeShare, 0, len(shares))}
	for _, share := range shares {
		out.Shares = append(out.Shares, shareDTO(share))
	}
	if len(shares) == codeshare.PageSize {
		out.NextBeforeId = &shares[len(shares)-1].ID
	}
	return out, nil
}

func (s *v1Server) CreateErvisioCodeShare(ctx context.Context, req v1gen.CreateErvisioCodeShareRequestObject) (v1gen.CreateErvisioCodeShareResponseObject, error) {
	accountID, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	body := req.Body
	if body == nil || (body.RecipientId == nil) == (body.OrgId == nil) ||
		body.RecipientId != nil && (*body.RecipientId == uuid.Nil || *body.RecipientId == accountID) ||
		body.OrgId != nil && *body.OrgId == uuid.Nil ||
		body.DeviceId == uuid.Nil || !remoteSessionID.MatchString(body.SessionId) || !body.Role.Valid() {
		return shareProblem(codeshare.ErrInvalid)
	}
	if body.Role == v1gen.ShareDirect {
		return problemResponse{status: http.StatusUnprocessableEntity, detail: "direct collaboration is not enabled"}, nil
	}
	history, problem, err := s.shareCreateHistory(ctx, accountID, req.Params.AccountId, *body)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	in := codeshare.CreateShareInput{DeviceID: body.DeviceId, SessionID: body.SessionId, Role: codeshare.Role(body.Role), History: history}
	if body.OrgId != nil {
		share, err := s.shares.CreateOrgGrant(ctx, accountID, *body.OrgId, in)
		if err != nil {
			return shareProblem(err)
		}
		return v1gen.CreateErvisioCodeShare201JSONResponse{Share: shareDTO(share)}, nil
	}
	invite, err := s.shares.CreatePersonalInvite(ctx, accountID, *body.RecipientId, in)
	if err != nil {
		return shareProblem(err)
	}
	return v1gen.CreateErvisioCodeShare201JSONResponse{Share: shareDTO(invite.Share), Token: &invite.Token}, nil
}

func (s *v1Server) shareCreateHistory(ctx context.Context, accountID uuid.UUID, selected *uuid.UUID, body v1gen.ErvisioCodeShareCreate) (codeshare.History, problemResponse, error) {
	choices := 0
	for _, set := range []bool{body.FullHistory != nil, body.AfterMessageId != nil, body.FromMessageId != nil, body.NewOnly != nil} {
		if set {
			choices++
		}
	}
	if choices != 1 || body.FullHistory != nil && !bool(*body.FullHistory) || body.NewOnly != nil && !bool(*body.NewOnly) ||
		body.AfterMessageId != nil && !remoteSessionID.MatchString(*body.AfterMessageId) ||
		body.FromMessageId != nil && !remoteSessionID.MatchString(*body.FromMessageId) {
		return codeshare.History{}, problemResponse{status: http.StatusBadRequest, detail: "choose exactly one explicit share history boundary or full history"}, nil
	}
	history := codeshare.History{FullHistory: body.FullHistory != nil,
		AfterMessageID: shareValue(body.AfterMessageId), FromMessageID: shareValue(body.FromMessageId)}
	if body.NewOnly == nil {
		return history, problemResponse{}, nil
	}
	reply, err := s.sendSharedSessionRequest(ctx, remoteHostKey{accountID: accountID, deviceID: body.DeviceId},
		v1gen.ErvisioCodeRelayRequest{Op: v1gen.RelayHistoryBoundary, SessionId: &body.SessionId})
	if err != nil || reply.problem.status != 0 {
		return codeshare.History{}, reply.problem, err
	}
	problem, err := s.recheckSharingAccount(ctx, accountID, selected)
	history.HostCursor = reply.hostCursor
	return history, problem, err
}

func (s *v1Server) AcceptErvisioCodeShare(ctx context.Context, req v1gen.AcceptErvisioCodeShareRequestObject) (v1gen.AcceptErvisioCodeShareResponseObject, error) {
	accountID, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	if req.Body == nil {
		return shareProblem(codeshare.ErrInvalid)
	}
	share, err := s.shares.AcceptPersonalInvite(ctx, accountID, req.Body.Token)
	if err != nil {
		return shareProblem(err)
	}
	return v1gen.AcceptErvisioCodeShare200JSONResponse(shareDTO(share)), nil
}

func (s *v1Server) AcceptErvisioCodeShareById(ctx context.Context, req v1gen.AcceptErvisioCodeShareByIdRequestObject) (v1gen.AcceptErvisioCodeShareByIdResponseObject, error) {
	accountID, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	if req.Body == nil {
		return shareProblem(codeshare.ErrInvalid)
	}
	share, err := s.shares.AcceptPersonalInviteByID(ctx, accountID, req.ShareId)
	if err != nil {
		return shareProblem(err)
	}
	return v1gen.AcceptErvisioCodeShareById200JSONResponse(shareDTO(share)), nil
}

func (s *v1Server) RevokeErvisioCodeShare(ctx context.Context, req v1gen.RevokeErvisioCodeShareRequestObject) (v1gen.RevokeErvisioCodeShareResponseObject, error) {
	accountID, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	if err := s.shares.RevokeShare(ctx, accountID, req.ShareId); err != nil {
		return shareProblem(err)
	}
	return v1gen.RevokeErvisioCodeShare204Response{}, nil
}

func (s *v1Server) GetErvisioCodeSharedState(ctx context.Context, req v1gen.GetErvisioCodeSharedStateRequestObject) (v1gen.GetErvisioCodeSharedStateResponseObject, error) {
	accountID, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	share, err := s.shares.GetActiveShare(ctx, accountID, req.ShareId)
	if err != nil {
		return shareProblem(err)
	}
	history, err := relayShareHistory(share.History)
	if err != nil {
		return nil, err
	}
	reply, err := s.sendSharedSessionRequest(ctx, remoteHostKey{accountID: share.Target.OwnerID, deviceID: share.Target.DeviceID},
		v1gen.ErvisioCodeRelayRequest{Op: v1gen.RelayState, SessionId: &share.Target.SessionID, History: history})
	if err != nil {
		return nil, err
	}
	if problem, err := s.recheckSharingAccount(ctx, accountID, req.Params.AccountId); err != nil || problem.status != 0 {
		return problem, err
	}
	current, err := s.shares.GetActiveShare(ctx, accountID, share.ID)
	if err != nil {
		return shareProblem(err)
	}
	if current.Target != share.Target || current.History != share.History {
		return shareProblem(codeshare.ErrNotFound)
	}
	if reply.problem.status != 0 {
		return reply.problem, nil
	}
	return v1gen.GetErvisioCodeSharedState200JSONResponse(reply.state), nil
}

func (s *v1Server) sendSharedSessionRequest(ctx context.Context, key remoteHostKey, request v1gen.ErvisioCodeRelayRequest) (remoteReply, error) {
	relayID, problem := s.remote.sessionRelay(key, request)
	if problem.status != 0 {
		return remoteReply{problem: problem}, nil
	}
	return s.sendRemoteRequest(ctx, key, relayID, request)
}

func relayShareHistory(history codeshare.History) (*v1gen.ErvisioCodeRelayHistory, error) {
	if history.FullHistory {
		return nil, nil
	}
	out := new(v1gen.ErvisioCodeRelayHistory)
	switch {
	case history.AfterMessageID != "":
		return out, out.FromErvisioCodeHistoryAfter(v1gen.ErvisioCodeHistoryAfter{AfterMessageId: history.AfterMessageID})
	case history.FromMessageID != "":
		return out, out.FromErvisioCodeHistoryFrom(v1gen.ErvisioCodeHistoryFrom{FromMessageId: history.FromMessageID})
	case history.HostCursor != "":
		return out, out.FromErvisioCodeHistoryCursor(v1gen.ErvisioCodeHistoryCursor{HostCursor: history.HostCursor})
	default:
		return nil, errors.New("restricted ErvisioCode share has no history boundary")
	}
}

func shareProblem(err error) (problemResponse, error) {
	switch {
	case errors.Is(err, codeshare.ErrInvalid):
		return problemResponse{status: http.StatusBadRequest, detail: err.Error()}, nil
	case errors.Is(err, codeshare.ErrNotFound), errors.Is(err, codeshare.ErrInvalidInvite):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, nil
	default:
		return problemResponse{}, err
	}
}

func shareDTO(share codeshare.Share) v1gen.ErvisioCodeShare {
	return v1gen.ErvisioCodeShare{
		Id: share.ID, OwnerId: share.Target.OwnerID, DeviceId: share.Target.DeviceID, SessionId: share.Target.SessionID,
		Role: v1gen.ErvisioCodeShareRole(share.Role), CreatedAt: share.CreatedAt, FullHistory: share.History.FullHistory,
		RecipientId: shareOptional(share.RecipientID), OrgId: shareOptional(share.OrgID),
		AcceptedAt: shareOptional(share.AcceptedAt), InviteExpiresAt: shareOptional(share.InviteExpiresAt), RevokedAt: shareOptional(share.RevokedAt),
		AfterMessageId: shareOptional(share.History.AfterMessageID), FromMessageId: shareOptional(share.History.FromMessageID),
		NewOnly: shareOptional(share.History.HostCursor != ""),
	}
}

func shareOptional[T comparable](value T) *T {
	var zero T
	if value == zero {
		return nil
	}
	return &value
}

func shareValue[T any](value *T) T {
	if value != nil {
		return *value
	}
	var zero T
	return zero
}
