package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

// Account is the self-service part of the account service (satisfied by
// *identity.Service); the router reaches it through the Identity value.
type Account interface {
	UpdateProfile(ctx context.Context, userID uuid.UUID, in identity.ProfileInput, c identity.Client) (identity.User, error)
	ChangePassword(ctx context.Context, userID, keep uuid.UUID, current, next string, c identity.Client) error
	ListSessions(ctx context.Context, userID uuid.UUID) ([]identity.SessionInfo, error)
	RevokeSession(ctx context.Context, userID, sessionID uuid.UUID, c identity.Client) error
	RevokeOtherSessions(ctx context.Context, userID, keep uuid.UUID, c identity.Client) error
	ListEmails(ctx context.Context, userID uuid.UUID) ([]identity.EmailAddress, error)
	AddEmail(ctx context.Context, userID uuid.UUID, address string, c identity.Client) (identity.EmailAddress, error)
	SetPrimaryEmail(ctx context.Context, userID, emailID uuid.UUID, c identity.Client) error
	DeleteEmail(ctx context.Context, userID, emailID uuid.UUID, c identity.Client) error
	ResendEmailVerification(ctx context.Context, userID, emailID uuid.UUID, c identity.Client) error
}

func (p problemResponse) VisitUpdateProfileResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitChangePasswordResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListSessionsResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitRevokeOtherSessionsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRevokeSessionResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitListEmailsResponse(w http.ResponseWriter) error      { return p.write(w) }
func (p problemResponse) VisitAddEmailResponse(w http.ResponseWriter) error        { return p.write(w) }
func (p problemResponse) VisitDeleteEmailResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitSetPrimaryEmailResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitResendEmailVerificationResponse(w http.ResponseWriter) error {
	return p.write(w)
}

// account returns the self-service service and the signed-in principal, or
// the answer to send instead.
func (s *v1Server) account(ctx context.Context) (Account, auth.Principal, *problemResponse) {
	acct, ok := s.identity.(Account)
	if !ok || s.identity == nil {
		return nil, auth.Principal{}, &errIdentityUnavailable
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return nil, auth.Principal{}, &problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}
	}
	return acct, p, nil
}

// accountProblem maps account errors to answers.
func accountProblem(err error) (problemResponse, bool) {
	switch {
	case errors.Is(err, identity.ErrInvalidProfile):
		return badRequest(err.Error()), true
	case errors.Is(err, identity.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: "not found"}, true
	case errors.Is(err, identity.ErrEmailTaken), errors.Is(err, identity.ErrPrimaryEmail),
		errors.Is(err, identity.ErrUnverifiedMail):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, true
	case errors.Is(err, identity.ErrEmailLimit):
		return badRequest(err.Error()), true
	}
	return identityProblem(err)
}

func (s *v1Server) UpdateProfile(ctx context.Context, req v1gen.UpdateProfileRequestObject) (v1gen.UpdateProfileResponseObject, error) {
	acct, p, pr := s.account(ctx)
	if pr != nil {
		return *pr, nil
	}
	if req.Body == nil {
		return badRequest("request body required"), nil
	}
	avatar := ""
	if req.Body.AvatarUrl != nil {
		avatar = *req.Body.AvatarUrl
	}
	u, err := acct.UpdateProfile(ctx, p.UserID, identity.ProfileInput{DisplayName: req.Body.DisplayName,
		AvatarURL: avatar, Locale: string(req.Body.Locale)}, clientOf(ctx))
	if pr, ok := accountProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.UpdateProfile200JSONResponse(userDTO(u)), nil
}

func (s *v1Server) ChangePassword(ctx context.Context, req v1gen.ChangePasswordRequestObject) (v1gen.ChangePasswordResponseObject, error) {
	acct, p, pr := s.account(ctx)
	if pr != nil {
		return *pr, nil
	}
	if req.Body == nil {
		return badRequest("request body required"), nil
	}
	current := ""
	if req.Body.CurrentPassword != nil {
		current = *req.Body.CurrentPassword
	}
	err := acct.ChangePassword(ctx, p.UserID, p.SessionID, current, req.Body.NewPassword, clientOf(ctx))
	if pr, ok := accountProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.ChangePassword204Response{}, nil
}

func (s *v1Server) ListSessions(ctx context.Context, _ v1gen.ListSessionsRequestObject) (v1gen.ListSessionsResponseObject, error) {
	acct, p, pr := s.account(ctx)
	if pr != nil {
		return *pr, nil
	}
	list, err := acct.ListSessions(ctx, p.UserID)
	if pr, ok := accountProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(v1gen.ListSessions200JSONResponse, 0, len(list))
	for _, x := range list {
		out = append(out, v1gen.SessionInfo{Id: x.ID, Current: x.ID == p.SessionID, Ip: x.IP, UserAgent: x.UserAgent,
			CreatedAt: x.CreatedAt, LastSeenAt: x.LastSeenAt, ExpiresAt: x.ExpiresAt})
	}
	return out, nil
}

func (s *v1Server) RevokeOtherSessions(ctx context.Context, _ v1gen.RevokeOtherSessionsRequestObject) (v1gen.RevokeOtherSessionsResponseObject, error) {
	acct, p, pr := s.account(ctx)
	if pr != nil {
		return *pr, nil
	}
	err := acct.RevokeOtherSessions(ctx, p.UserID, p.SessionID, clientOf(ctx))
	if pr, ok := accountProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.RevokeOtherSessions204Response{}, nil
}

func (s *v1Server) RevokeSession(ctx context.Context, req v1gen.RevokeSessionRequestObject) (v1gen.RevokeSessionResponseObject, error) {
	acct, p, pr := s.account(ctx)
	if pr != nil {
		return *pr, nil
	}
	err := acct.RevokeSession(ctx, p.UserID, req.Id, clientOf(ctx))
	if pr, ok := accountProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.RevokeSession204Response{}, nil
}

func emailAddressDTO(e identity.EmailAddress) v1gen.EmailAddress {
	return v1gen.EmailAddress{Id: e.ID, Address: e.Address, Primary: e.Primary, Verified: e.Verified, CreatedAt: e.CreatedAt}
}

func (s *v1Server) ListEmails(ctx context.Context, _ v1gen.ListEmailsRequestObject) (v1gen.ListEmailsResponseObject, error) {
	acct, p, pr := s.account(ctx)
	if pr != nil {
		return *pr, nil
	}
	list, err := acct.ListEmails(ctx, p.UserID)
	if pr, ok := accountProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(v1gen.ListEmails200JSONResponse, 0, len(list))
	for _, e := range list {
		out = append(out, emailAddressDTO(e))
	}
	return out, nil
}

func (s *v1Server) AddEmail(ctx context.Context, req v1gen.AddEmailRequestObject) (v1gen.AddEmailResponseObject, error) {
	acct, p, pr := s.account(ctx)
	if pr != nil {
		return *pr, nil
	}
	if req.Body == nil {
		return badRequest("request body required"), nil
	}
	e, err := acct.AddEmail(ctx, p.UserID, req.Body.Email, clientOf(ctx))
	if pr, ok := accountProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.AddEmail201JSONResponse(emailAddressDTO(e)), nil
}

func (s *v1Server) SetPrimaryEmail(ctx context.Context, req v1gen.SetPrimaryEmailRequestObject) (v1gen.SetPrimaryEmailResponseObject, error) {
	acct, p, pr := s.account(ctx)
	if pr != nil {
		return *pr, nil
	}
	err := acct.SetPrimaryEmail(ctx, p.UserID, req.Id, clientOf(ctx))
	if pr, ok := accountProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.SetPrimaryEmail204Response{}, nil
}

func (s *v1Server) DeleteEmail(ctx context.Context, req v1gen.DeleteEmailRequestObject) (v1gen.DeleteEmailResponseObject, error) {
	acct, p, pr := s.account(ctx)
	if pr != nil {
		return *pr, nil
	}
	err := acct.DeleteEmail(ctx, p.UserID, req.Id, clientOf(ctx))
	if pr, ok := accountProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.DeleteEmail204Response{}, nil
}

func (s *v1Server) ResendEmailVerification(ctx context.Context, req v1gen.ResendEmailVerificationRequestObject) (v1gen.ResendEmailVerificationResponseObject, error) {
	acct, p, pr := s.account(ctx)
	if pr != nil {
		return *pr, nil
	}
	err := acct.ResendEmailVerification(ctx, p.UserID, req.Id, clientOf(ctx))
	if pr, ok := accountProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.ResendEmailVerification202Response{}, nil
}
