package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

// Identity is the account service as seen by the auth endpoints (satisfied
// by *identity.Service).
type Identity interface {
	Register(ctx context.Context, in identity.RegisterInput, c identity.Client) (identity.RegisterResult, error)
	Login(ctx context.Context, login, password, oldToken string, c identity.Client) (identity.LoginResult, error)
	Logout(ctx context.Context, p auth.Principal, c identity.Client) error
	User(ctx context.Context, id uuid.UUID) (identity.User, error)
	VerifyEmail(ctx context.Context, token string, c identity.Client) error
	ResendVerification(ctx context.Context, address string, c identity.Client) error
	RequestPasswordReset(ctx context.Context, address string, c identity.Client) error
	ResetPassword(ctx context.Context, token, password string, c identity.Client) error
	CreateInvite(ctx context.Context, actor auth.Principal, email string, c identity.Client) (identity.Invite, error)
	SearchUsers(ctx context.Context, who auth.Principal, query string, repoID uuid.UUID, limit int) ([]identity.UserSummary, error)
}

// problemResponse is an error answer usable by every operation.
type problemResponse struct {
	status     int
	detail     string
	retryAfter int // seconds, sent as Retry-After when > 0
}

func (p problemResponse) write(w http.ResponseWriter) error {
	if p.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(p.retryAfter))
	}
	writeProblem(w, p.status, p.detail)
	return nil
}

func (p problemResponse) VisitRegisterResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitLoginResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitLogoutResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitVerifyEmailResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitResendVerificationResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitRequestPasswordResetResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitResetPasswordResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetCurrentUserResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitCreateInviteResponse(w http.ResponseWriter) error {
	return p.write(w)
}

var errIdentityUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "accounts are not available"}

// identityProblem maps the errors of the identity service to answers. Any
// other error is returned to the strict handler, which logs it and answers
// 500 without details.
func identityProblem(err error) (problemResponse, bool) {
	var rl *identity.RateLimitError
	switch {
	case errors.As(err, &rl):
		return problemResponse{status: http.StatusTooManyRequests, detail: rl.Error(),
			retryAfter: max(1, int(math.Ceil(rl.RetryAfter.Seconds())))}, true
	case errors.Is(err, identity.ErrInvalidName), errors.Is(err, identity.ErrReservedName),
		errors.Is(err, identity.ErrInvalidEmail), errors.Is(err, identity.ErrWeakPassword),
		errors.Is(err, identity.ErrInvalidToken):
		return problemResponse{status: http.StatusBadRequest, detail: err.Error()}, true
	case errors.Is(err, identity.ErrInvalidCredentials):
		return problemResponse{status: http.StatusUnauthorized, detail: err.Error()}, true
	case errors.Is(err, identity.ErrRegistrationClosed), errors.Is(err, identity.ErrInviteRequired),
		errors.Is(err, identity.ErrSuspended), errors.Is(err, identity.ErrEmailNotVerified):
		return problemResponse{status: http.StatusForbidden, detail: err.Error()}, true
	case errors.Is(err, identity.ErrUsernameTaken):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, true
	case errors.Is(err, identity.ErrNotFound):
		return problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}, true
	}
	return problemResponse{}, false
}

func clientOf(ctx context.Context) identity.Client {
	ri := auth.RequestInfoFrom(ctx)
	return identity.Client{IP: ri.IP, UserAgent: ri.UserAgent}
}

func badRequest(detail string) problemResponse {
	return problemResponse{status: http.StatusBadRequest, detail: detail}
}

func userDTO(u identity.User) v1gen.User {
	out := v1gen.User{
		Id: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: v1gen.UserRole(u.Role),
		Locale: v1gen.UserLocale(u.Locale), EmailVerified: u.EmailVerified, CreatedAt: u.CreatedAt,
	}
	if u.Email != "" {
		out.Email = &u.Email
	}
	if u.AvatarURL != "" {
		out.AvatarUrl = &u.AvatarURL
	}
	return out
}

func (s *v1Server) Register(ctx context.Context, req v1gen.RegisterRequestObject) (v1gen.RegisterResponseObject, error) {
	if s.identity == nil {
		return errIdentityUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	in := identity.RegisterInput{Username: req.Body.Username, Email: req.Body.Email, Password: req.Body.Password}
	if req.Body.InviteToken != nil {
		in.InviteToken = *req.Body.InviteToken
	}
	if req.Body.Locale != nil {
		in.Locale = string(*req.Body.Locale)
	}
	res, err := s.identity.Register(ctx, in, clientOf(ctx))
	if p, ok := identityProblem(err); ok {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.Register202JSONResponse{VerificationRequired: res.VerificationRequired}, nil
}

// loginResponse sets the new session and CSRF cookies.
type loginResponse struct {
	user    v1gen.User
	session auth.NewSession
	cookies auth.Cookies
}

func (l loginResponse) VisitLoginResponse(w http.ResponseWriter) error {
	l.cookies.SetSession(w, l.session.Token, l.session.ExpiresAt)
	l.cookies.SetCSRF(w, l.session.CSRFToken)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(l.user)
}

func (s *v1Server) Login(ctx context.Context, req v1gen.LoginRequestObject) (v1gen.LoginResponseObject, error) {
	if s.identity == nil {
		return errIdentityUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	ri := auth.RequestInfoFrom(ctx)
	if s.mfa != nil {
		out, err := s.mfa.Login(ctx, req.Body.Login, req.Body.Password, ri.SessionToken, clientOf(ctx))
		if p, ok := mfaProblem(err); ok {
			return p, nil
		}
		if err != nil {
			return nil, err
		}
		if out.Pending != nil {
			return pendingResponse{pending: *out.Pending, cookies: s.cookies}, nil
		}
		return loginResponse{user: userDTO(out.Session.User), session: out.Session.Session, cookies: s.cookies}, nil
	}
	res, err := s.identity.Login(ctx, req.Body.Login, req.Body.Password, ri.SessionToken, clientOf(ctx))
	if p, ok := identityProblem(err); ok {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	return loginResponse{user: userDTO(res.User), session: res.Session, cookies: s.cookies}, nil
}

// logoutResponse clears the session cookie and issues a fresh CSRF token.
type logoutResponse struct{ cookies auth.Cookies }

func (l logoutResponse) VisitLogoutResponse(w http.ResponseWriter) error {
	l.cookies.ClearSession(w)
	csrf, _ := auth.NewToken()
	l.cookies.SetCSRF(w, csrf)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *v1Server) Logout(ctx context.Context, _ v1gen.LogoutRequestObject) (v1gen.LogoutResponseObject, error) {
	if s.identity == nil {
		return errIdentityUnavailable, nil
	}
	if p, ok := auth.PrincipalFrom(ctx); ok {
		if err := s.identity.Logout(ctx, p, clientOf(ctx)); err != nil {
			return nil, err
		}
	}
	return logoutResponse{cookies: s.cookies}, nil
}

func (s *v1Server) GetCurrentUser(ctx context.Context, _ v1gen.GetCurrentUserRequestObject) (v1gen.GetCurrentUserResponseObject, error) {
	if s.identity == nil {
		return errIdentityUnavailable, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}, nil
	}
	u, err := s.identity.User(ctx, p.UserID)
	if pr, ok := identityProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.GetCurrentUser200JSONResponse(userDTO(u)), nil
}

func (s *v1Server) VerifyEmail(ctx context.Context, req v1gen.VerifyEmailRequestObject) (v1gen.VerifyEmailResponseObject, error) {
	if s.identity == nil {
		return errIdentityUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	err := s.identity.VerifyEmail(ctx, req.Body.Token, clientOf(ctx))
	if p, ok := identityProblem(err); ok {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.VerifyEmail204Response{}, nil
}

func (s *v1Server) ResendVerification(ctx context.Context, req v1gen.ResendVerificationRequestObject) (v1gen.ResendVerificationResponseObject, error) {
	if s.identity == nil {
		return errIdentityUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	err := s.identity.ResendVerification(ctx, req.Body.Email, clientOf(ctx))
	if p, ok := identityProblem(err); ok {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.ResendVerification202Response{}, nil
}

func (s *v1Server) RequestPasswordReset(ctx context.Context, req v1gen.RequestPasswordResetRequestObject) (v1gen.RequestPasswordResetResponseObject, error) {
	if s.identity == nil {
		return errIdentityUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	err := s.identity.RequestPasswordReset(ctx, req.Body.Email, clientOf(ctx))
	if p, ok := identityProblem(err); ok {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.RequestPasswordReset202Response{}, nil
}

func (s *v1Server) ResetPassword(ctx context.Context, req v1gen.ResetPasswordRequestObject) (v1gen.ResetPasswordResponseObject, error) {
	if s.identity == nil {
		return errIdentityUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	err := s.identity.ResetPassword(ctx, req.Body.Token, req.Body.Password, clientOf(ctx))
	if p, ok := identityProblem(err); ok {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.ResetPassword204Response{}, nil
}

func (s *v1Server) CreateInvite(ctx context.Context, req v1gen.CreateInviteRequestObject) (v1gen.CreateInviteResponseObject, error) {
	if s.identity == nil {
		return errIdentityUnavailable, nil
	}
	p, _ := auth.PrincipalFrom(ctx)
	email := ""
	if req.Body != nil && req.Body.Email != nil {
		email = *req.Body.Email
	}
	inv, err := s.identity.CreateInvite(ctx, p, email, clientOf(ctx))
	if pr, ok := identityProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	out := v1gen.CreateInvite201JSONResponse{Id: inv.ID, Token: inv.Token, ExpiresAt: inv.ExpiresAt}
	if inv.Email != "" {
		out.Email = &inv.Email
	}
	return out, nil
}
