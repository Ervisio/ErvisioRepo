package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/auth/mfa"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

type mfaTokenKey struct{}

// mfaCookie puts the pending login token (erepo_mfa cookie) in the request
// context for the /auth/mfa handlers.
func mfaCookie(c auth.Cookies) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if t := mfa.FromRequest(r, c.Secure); t != "" {
				r = r.WithContext(context.WithValue(r.Context(), mfaTokenKey{}, t))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func mfaToken(ctx context.Context) string {
	t, _ := ctx.Value(mfaTokenKey{}).(string)
	return t
}

var errMFAUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "two-factor authentication is not available"}

// mfaProblem maps the errors of the MFA service, then the identity ones.
func mfaProblem(err error) (problemResponse, bool) {
	switch {
	case errors.Is(err, mfa.ErrInvalidCode), errors.Is(err, mfa.ErrChallenge), errors.Is(err, mfa.ErrInvalidPasskey):
		return problemResponse{status: http.StatusUnauthorized, detail: err.Error()}, true
	case errors.Is(err, mfa.ErrReauth):
		return problemResponse{status: http.StatusForbidden, detail: err.Error()}, true
	case errors.Is(err, mfa.ErrTOTPEnabled), errors.Is(err, mfa.ErrNoTOTPSetup), errors.Is(err, mfa.ErrNotEnabled),
		errors.Is(err, mfa.ErrFactorRequired), errors.Is(err, mfa.ErrNoFactor):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, true
	case errors.Is(err, mfa.ErrInvalidName):
		return problemResponse{status: http.StatusBadRequest, detail: err.Error()}, true
	case errors.Is(err, mfa.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, true
	case errors.Is(err, mfa.ErrWebAuthnOff):
		return problemResponse{status: http.StatusServiceUnavailable, detail: err.Error()}, true
	}
	return identityProblem(err)
}

func (p problemResponse) VisitGetMfaChallengeResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitVerifyMfaTotpResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitVerifyMfaRecoveryResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitBeginMfaWebAuthnResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitVerifyMfaWebAuthnResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitBeginMfaEnrollTotpResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitConfirmMfaEnrollTotpResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitBeginPasskeyLoginResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitPasskeyLoginResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetSecurityResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitSetupTotpResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitDisableTotpResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitConfirmTotpResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitRegenerateRecoveryCodesResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitBeginPasskeyRegistrationResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRegisterPasskeyResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeletePasskeyResponse(w http.ResponseWriter) error { return p.write(w) }

// pendingResponse answers 202 to a login that needs its second factor: it
// sets only the pending cookie, no session.
type pendingResponse struct {
	pending mfa.Pending
	cookies auth.Cookies
}

func (p pendingResponse) VisitLoginResponse(w http.ResponseWriter) error {
	mfa.SetCookie(w, p.cookies.Secure, p.pending.Token, p.pending.ExpiresAt)
	return writeNoStoreJSON(w, http.StatusAccepted, challengeDTO(p.pending))
}

// sessionResponse completes a login: session and CSRF cookies, and the
// pending cookie cleared.
type sessionResponse struct {
	body    any
	session auth.NewSession
	cookies auth.Cookies
}

func (l sessionResponse) write(w http.ResponseWriter) error {
	l.cookies.SetSession(w, l.session.Token, l.session.ExpiresAt)
	l.cookies.SetCSRF(w, l.session.CSRFToken)
	mfa.ClearCookie(w, l.cookies.Secure)
	return writeNoStoreJSON(w, http.StatusOK, l.body)
}

func (l sessionResponse) VisitVerifyMfaTotpResponse(w http.ResponseWriter) error { return l.write(w) }
func (l sessionResponse) VisitVerifyMfaRecoveryResponse(w http.ResponseWriter) error {
	return l.write(w)
}
func (l sessionResponse) VisitVerifyMfaWebAuthnResponse(w http.ResponseWriter) error {
	return l.write(w)
}
func (l sessionResponse) VisitPasskeyLoginResponse(w http.ResponseWriter) error { return l.write(w) }
func (l sessionResponse) VisitConfirmMfaEnrollTotpResponse(w http.ResponseWriter) error {
	return l.write(w)
}

func writeNoStoreJSON(w http.ResponseWriter, status int, body any) error {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(body)
}

func (s *v1Server) newSession(res identity.LoginResult, body any) sessionResponse {
	if body == nil {
		body = userDTO(res.User)
	}
	return sessionResponse{body: body, session: res.Session, cookies: s.cookies}
}

func challengeDTO(p mfa.Pending) v1gen.MfaChallenge {
	out := v1gen.MfaChallenge{EnrollmentRequired: p.Enroll, ExpiresAt: p.ExpiresAt, Methods: []v1gen.MfaChallengeMethods{}}
	for _, m := range p.Methods {
		out.Methods = append(out.Methods, v1gen.MfaChallengeMethods(m))
	}
	return out
}

// credentialBytes re-encodes a WebAuthn response object for the parser.
func credentialBytes(c map[string]any) ([]byte, bool) {
	if len(c) == 0 {
		return nil, false
	}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > mfa.MaxCredentialJSON {
		return nil, false
	}
	return raw, true
}

func rawObject(raw json.RawMessage) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func reauthOf(b *v1gen.ReauthRequest) mfa.Reauth {
	var r mfa.Reauth
	if b == nil {
		return r
	}
	if b.Password != nil {
		r.Password = *b.Password
	}
	if b.Code != nil {
		r.Code = *b.Code
	}
	return r
}

func (s *v1Server) GetMfaChallenge(ctx context.Context, _ v1gen.GetMfaChallengeRequestObject) (v1gen.GetMfaChallengeResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	p, err := s.mfa.PendingInfo(ctx, mfaToken(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.GetMfaChallenge200JSONResponse(challengeDTO(p)), nil
}

func (s *v1Server) VerifyMfaTotp(ctx context.Context, req v1gen.VerifyMfaTotpRequestObject) (v1gen.VerifyMfaTotpResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	ri := auth.RequestInfoFrom(ctx)
	res, err := s.mfa.VerifyTOTP(ctx, mfaToken(ctx), req.Body.Code, ri.SessionToken, clientOf(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return s.newSession(res, nil), nil
}

func (s *v1Server) VerifyMfaRecovery(ctx context.Context, req v1gen.VerifyMfaRecoveryRequestObject) (v1gen.VerifyMfaRecoveryResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	ri := auth.RequestInfoFrom(ctx)
	res, err := s.mfa.VerifyRecovery(ctx, mfaToken(ctx), req.Body.Code, ri.SessionToken, clientOf(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return s.newSession(res, nil), nil
}

func (s *v1Server) BeginMfaWebAuthn(ctx context.Context, _ v1gen.BeginMfaWebAuthnRequestObject) (v1gen.BeginMfaWebAuthnResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	raw, err := s.mfa.BeginWebAuthnLogin(ctx, mfaToken(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	opts, err := rawObject(raw)
	if err != nil {
		return nil, err
	}
	return v1gen.BeginMfaWebAuthn200JSONResponse{Options: opts}, nil
}

func (s *v1Server) VerifyMfaWebAuthn(ctx context.Context, req v1gen.VerifyMfaWebAuthnRequestObject) (v1gen.VerifyMfaWebAuthnResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	cred, ok := credentialBytes(req.Body.Credential)
	if !ok {
		return badRequest("invalid credential"), nil
	}
	ri := auth.RequestInfoFrom(ctx)
	res, err := s.mfa.FinishWebAuthnLogin(ctx, mfaToken(ctx), cred, ri.SessionToken, clientOf(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return s.newSession(res, nil), nil
}

func (s *v1Server) BeginMfaEnrollTotp(ctx context.Context, _ v1gen.BeginMfaEnrollTotpRequestObject) (v1gen.BeginMfaEnrollTotpResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	secret, uri, err := s.mfa.BeginEnrollTOTP(ctx, mfaToken(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.BeginMfaEnrollTotp200JSONResponse{Secret: secret, Uri: uri}, nil
}

func (s *v1Server) ConfirmMfaEnrollTotp(ctx context.Context, req v1gen.ConfirmMfaEnrollTotpRequestObject) (v1gen.ConfirmMfaEnrollTotpResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	ri := auth.RequestInfoFrom(ctx)
	res, codes, err := s.mfa.ConfirmEnrollTOTP(ctx, mfaToken(ctx), req.Body.Code, ri.SessionToken, clientOf(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	if codes == nil {
		codes = []string{}
	}
	return s.newSession(res, v1gen.MfaEnrollResult{User: userDTO(res.User), RecoveryCodes: codes}), nil
}

func (s *v1Server) BeginPasskeyLogin(ctx context.Context, _ v1gen.BeginPasskeyLoginRequestObject) (v1gen.BeginPasskeyLoginResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	token, raw, err := s.mfa.BeginPasskeyLogin(ctx, clientOf(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	opts, err := rawObject(raw)
	if err != nil {
		return nil, err
	}
	return v1gen.BeginPasskeyLogin200JSONResponse{Challenge: token, Options: opts}, nil
}

func (s *v1Server) PasskeyLogin(ctx context.Context, req v1gen.PasskeyLoginRequestObject) (v1gen.PasskeyLoginResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	cred, ok := credentialBytes(req.Body.Credential)
	if !ok {
		return badRequest("invalid credential"), nil
	}
	ri := auth.RequestInfoFrom(ctx)
	res, err := s.mfa.FinishPasskeyLogin(ctx, req.Body.Challenge, cred, ri.SessionToken, clientOf(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return s.newSession(res, nil), nil
}

func (s *v1Server) GetSecurity(ctx context.Context, _ v1gen.GetSecurityRequestObject) (v1gen.GetSecurityResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}, nil
	}
	st, err := s.mfa.Status(ctx, p)
	if err != nil {
		return nil, err
	}
	out := v1gen.GetSecurity200JSONResponse{
		Totp: st.TOTP, TotpPending: st.TOTPPending, RecoveryCodesLeft: st.RecoveryUnused,
		RecoveryCodesTotal: st.RecoveryTotal, Required: st.Required, WebauthnAvailable: st.WebAuthn,
		Passkeys: make([]v1gen.Passkey, 0, len(st.Passkeys)),
	}
	for _, k := range st.Passkeys {
		out.Passkeys = append(out.Passkeys, passkeyDTO(k))
	}
	return out, nil
}

func passkeyDTO(k mfa.Passkey) v1gen.Passkey {
	return v1gen.Passkey{Id: k.ID, Name: k.Name, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt}
}

func (s *v1Server) SetupTotp(ctx context.Context, req v1gen.SetupTotpRequestObject) (v1gen.SetupTotpResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}, nil
	}
	secret, uri, err := s.mfa.SetupTOTP(ctx, p, reauthOf(req.Body), clientOf(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.SetupTotp200JSONResponse{Secret: secret, Uri: uri}, nil
}

func (s *v1Server) ConfirmTotp(ctx context.Context, req v1gen.ConfirmTotpRequestObject) (v1gen.ConfirmTotpResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	codes, err := s.mfa.ConfirmTOTP(ctx, p, req.Body.Code, clientOf(ctx))
	if errors.Is(err, mfa.ErrInvalidCode) {
		// The session is valid: a wrong code is a bad request, not a 401.
		return badRequest(err.Error()), nil
	}
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	if codes == nil {
		codes = []string{}
	}
	return v1gen.ConfirmTotp200JSONResponse{Codes: codes}, nil
}

func (s *v1Server) DisableTotp(ctx context.Context, req v1gen.DisableTotpRequestObject) (v1gen.DisableTotpResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}, nil
	}
	err := s.mfa.DisableTOTP(ctx, p, reauthOf(req.Body), clientOf(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.DisableTotp204Response{}, nil
}

func (s *v1Server) RegenerateRecoveryCodes(ctx context.Context, req v1gen.RegenerateRecoveryCodesRequestObject) (v1gen.RegenerateRecoveryCodesResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}, nil
	}
	codes, err := s.mfa.RegenerateRecoveryCodes(ctx, p, reauthOf(req.Body), clientOf(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.RegenerateRecoveryCodes200JSONResponse{Codes: codes}, nil
}

func (s *v1Server) BeginPasskeyRegistration(ctx context.Context, req v1gen.BeginPasskeyRegistrationRequestObject) (v1gen.BeginPasskeyRegistrationResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}, nil
	}
	token, raw, err := s.mfa.BeginPasskeyRegistration(ctx, p, reauthOf(req.Body), clientOf(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	opts, err := rawObject(raw)
	if err != nil {
		return nil, err
	}
	return v1gen.BeginPasskeyRegistration200JSONResponse{Challenge: token, Options: opts}, nil
}

func (s *v1Server) RegisterPasskey(ctx context.Context, req v1gen.RegisterPasskeyRequestObject) (v1gen.RegisterPasskeyResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	cred, ok := credentialBytes(req.Body.Credential)
	if !ok {
		return badRequest("invalid credential"), nil
	}
	k, err := s.mfa.FinishPasskeyRegistration(ctx, p, req.Body.Challenge, req.Body.Name, cred, clientOf(ctx))
	if errors.Is(err, mfa.ErrInvalidPasskey) || errors.Is(err, mfa.ErrChallenge) {
		return badRequest(err.Error()), nil
	}
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.RegisterPasskey201JSONResponse(passkeyDTO(k)), nil
}

func (s *v1Server) DeletePasskey(ctx context.Context, req v1gen.DeletePasskeyRequestObject) (v1gen.DeletePasskeyResponseObject, error) {
	if s.mfa == nil {
		return errMFAUnavailable, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return problemResponse{status: http.StatusUnauthorized, detail: "authentication required"}, nil
	}
	err := s.mfa.DeletePasskey(ctx, p, req.Id, reauthOf(req.Body), clientOf(ctx))
	if pr, ok := mfaProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.DeletePasskey204Response{}, nil
}
