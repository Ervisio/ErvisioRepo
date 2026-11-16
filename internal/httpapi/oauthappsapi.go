package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

// OAuth apps: developer settings, authorized applications and the consent
// page of the web flow (APPS-PLAN A1).

func (p problemResponse) VisitListMyOAuthAppsResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitCreateMyOAuthAppResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListOrgOAuthAppsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitCreateOrgOAuthAppResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetOAuthAppResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitUpdateOAuthAppResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitDeleteOAuthAppResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitCreateOAuthAppSecretResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteOAuthAppSecretResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListOAuthAuthorizationsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRevokeOAuthAuthorizationResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetOAuthConsentResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitDecideOAuthConsentResponse(w http.ResponseWriter) error {
	return p.write(w)
}

// oauthProblem maps the errors of the OAuth app operations.
func oauthProblem(err error) (problemResponse, bool) {
	var ie *credentials.AppInputError
	var ae *credentials.AuthorizeError
	switch {
	case err == nil:
		return problemResponse{}, false
	case errors.As(err, &ie):
		return problemResponse{status: http.StatusUnprocessableEntity, detail: ie.Msg}, true
	case errors.As(err, &ae):
		if ae.NotFound {
			return problemResponse{status: http.StatusNotFound, detail: ae.Msg}, true
		}
		return badRequest(ae.Msg), true
	case errors.Is(err, credentials.ErrForbidden):
		return problemResponse{status: http.StatusForbidden, detail: "you are not allowed to register or manage this application"}, true
	case errors.Is(err, credentials.ErrTooMany):
		return problemResponse{status: http.StatusConflict, detail: "the limit for this kind of credential is reached"}, true
	}
	return credentialsProblem(err)
}

func oauthOwnerDTO(a credentials.OAuthApp) v1gen.OAuthAppOwner {
	return v1gen.OAuthAppOwner{Kind: v1gen.OAuthAppOwnerKind(a.OwnerKind), Name: a.OwnerName}
}

func oauthAppDTO(a credentials.OAuthApp) v1gen.OAuthApp {
	out := v1gen.OAuthApp{ClientId: a.ClientID, Name: a.Name, Description: a.Description, HomepageUrl: a.HomepageURL,
		LogoUrl: optString(a.LogoURL), CallbackUrls: nonNil(a.CallbackURLs), DeviceFlow: a.DeviceFlow, Owner: oauthOwnerDTO(a),
		Users: a.Users, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
	if a.Secrets != nil {
		secrets := make([]v1gen.OAuthAppSecret, 0, len(a.Secrets))
		for _, s := range a.Secrets {
			secrets = append(secrets, oauthSecretDTO(s))
		}
		out.Secrets = &secrets
	}
	return out
}

func oauthSecretDTO(s credentials.OAuthAppSecret) v1gen.OAuthAppSecret {
	return v1gen.OAuthAppSecret{Id: s.ID, Hint: s.Hint, CreatedBy: optString(s.CreatedBy), CreatedAt: s.CreatedAt,
		LastUsedAt: s.LastUsedAt}
}

func oauthSummaryDTO(a credentials.OAuthApp) v1gen.OAuthAppSummary {
	return v1gen.OAuthAppSummary{ClientId: a.ClientID, Name: a.Name, Description: a.Description, HomepageUrl: a.HomepageURL,
		LogoUrl: optString(a.LogoURL), Owner: oauthOwnerDTO(a)}
}

func oauthAppsDTO(apps []credentials.OAuthApp) []v1gen.OAuthApp {
	out := make([]v1gen.OAuthApp, 0, len(apps))
	for _, a := range apps {
		out = append(out, oauthAppDTO(a))
	}
	return out
}

func oauthInput(name, description, homepage, logo *string, callbacks *[]string, deviceFlow *bool) credentials.OAuthAppInput {
	in := credentials.OAuthAppInput{Name: name, Description: description, HomepageURL: homepage, LogoURL: logo,
		DeviceFlow: deviceFlow}
	if callbacks != nil {
		in.CallbackURLs = nonNil(*callbacks)
	}
	return in
}

func (s *v1Server) ListMyOAuthApps(ctx context.Context, _ v1gen.ListMyOAuthAppsRequestObject) (v1gen.ListMyOAuthAppsResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	apps, err := s.creds.ListUserOAuthApps(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return v1gen.ListMyOAuthApps200JSONResponse(oauthAppsDTO(apps)), nil
}

func (s *v1Server) CreateMyOAuthApp(ctx context.Context, req v1gen.CreateMyOAuthAppRequestObject) (v1gen.CreateMyOAuthAppResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	app, err := s.creds.CreateOAuthApp(ctx, p, "", oauthInput(&b.Name, b.Description, &b.HomepageUrl, b.LogoUrl,
		&b.CallbackUrls, b.DeviceFlow), clientOf(ctx).IP)
	if pr, ok := oauthProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.CreateMyOAuthApp201JSONResponse(oauthAppDTO(app)), nil
}

func (s *v1Server) ListOrgOAuthApps(ctx context.Context, req v1gen.ListOrgOAuthAppsRequestObject) (v1gen.ListOrgOAuthAppsResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	apps, err := s.creds.ListOrgOAuthApps(ctx, p, req.Org)
	if pr, ok := oauthProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.ListOrgOAuthApps200JSONResponse(oauthAppsDTO(apps)), nil
}

func (s *v1Server) CreateOrgOAuthApp(ctx context.Context, req v1gen.CreateOrgOAuthAppRequestObject) (v1gen.CreateOrgOAuthAppResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	app, err := s.creds.CreateOAuthApp(ctx, p, req.Org, oauthInput(&b.Name, b.Description, &b.HomepageUrl, b.LogoUrl,
		&b.CallbackUrls, b.DeviceFlow), clientOf(ctx).IP)
	if pr, ok := oauthProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.CreateOrgOAuthApp201JSONResponse(oauthAppDTO(app)), nil
}

func (s *v1Server) GetOAuthApp(ctx context.Context, req v1gen.GetOAuthAppRequestObject) (v1gen.GetOAuthAppResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	app, err := s.creds.GetOAuthApp(ctx, p, req.ClientId)
	if pr, ok := oauthProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.GetOAuthApp200JSONResponse(oauthAppDTO(app)), nil
}

func (s *v1Server) UpdateOAuthApp(ctx context.Context, req v1gen.UpdateOAuthAppRequestObject) (v1gen.UpdateOAuthAppResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	app, err := s.creds.UpdateOAuthApp(ctx, p, req.ClientId, oauthInput(b.Name, b.Description, b.HomepageUrl, b.LogoUrl,
		b.CallbackUrls, b.DeviceFlow), clientOf(ctx).IP)
	if pr, ok := oauthProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.UpdateOAuthApp200JSONResponse(oauthAppDTO(app)), nil
}

func (s *v1Server) DeleteOAuthApp(ctx context.Context, req v1gen.DeleteOAuthAppRequestObject) (v1gen.DeleteOAuthAppResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	err := s.creds.DeleteOAuthApp(ctx, p, req.ClientId, clientOf(ctx).IP)
	if pr, ok := oauthProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.DeleteOAuthApp204Response{}, nil
}

func (s *v1Server) CreateOAuthAppSecret(ctx context.Context, req v1gen.CreateOAuthAppSecretRequestObject) (v1gen.CreateOAuthAppSecretResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	sec, secret, err := s.creds.CreateOAuthAppSecret(ctx, p, req.ClientId, clientOf(ctx).IP)
	if pr, ok := oauthProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.CreateOAuthAppSecret201JSONResponse{Secret: oauthSecretDTO(sec), ClientSecret: secret}, nil
}

func (s *v1Server) DeleteOAuthAppSecret(ctx context.Context, req v1gen.DeleteOAuthAppSecretRequestObject) (v1gen.DeleteOAuthAppSecretResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	err := s.creds.DeleteOAuthAppSecret(ctx, p, req.ClientId, req.Id, clientOf(ctx).IP)
	if pr, ok := oauthProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.DeleteOAuthAppSecret204Response{}, nil
}

func (s *v1Server) ListOAuthAuthorizations(ctx context.Context, _ v1gen.ListOAuthAuthorizationsRequestObject) (v1gen.ListOAuthAuthorizationsResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	list, err := s.creds.ListAuthorizations(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]v1gen.OAuthAuthorization, 0, len(list))
	for _, a := range list {
		out = append(out, v1gen.OAuthAuthorization{Id: a.ID, App: oauthSummaryDTO(a.App), Scopes: nonNil(a.Scopes),
			TokenScopes: nonNil(a.TokenScopes), Tokens: a.Tokens, CreatedAt: a.CreatedAt, LastUsedAt: a.LastUsedAt})
	}
	return v1gen.ListOAuthAuthorizations200JSONResponse(out), nil
}

func (s *v1Server) RevokeOAuthAuthorization(ctx context.Context, req v1gen.RevokeOAuthAuthorizationRequestObject) (v1gen.RevokeOAuthAuthorizationResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	err := s.creds.RevokeAuthorization(ctx, p.UserID, req.Id, clientOf(ctx).IP)
	if pr, ok := oauthProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.RevokeOAuthAuthorization204Response{}, nil
}

func (s *v1Server) GetOAuthConsent(ctx context.Context, req v1gen.GetOAuthConsentRequestObject) (v1gen.GetOAuthConsentResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	pr := req.Params
	c, err := s.creds.CheckAuthorize(ctx, p.UserID, credentials.AuthorizeRequest{ClientID: pr.ClientId,
		RedirectURI: deref(pr.RedirectUri), Scope: deref(pr.Scope), CodeChallenge: deref(pr.CodeChallenge),
		CodeChallengeMethod: deref(pr.CodeChallengeMethod)})
	if prob, ok := oauthProblem(err); ok {
		return prob, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.GetOAuthConsent200JSONResponse{App: oauthSummaryDTO(c.App), RedirectUri: c.RedirectURI,
		RequestedScopes: nonNil(c.RequestedScopes), Scopes: nonNil(c.Scopes), Granted: c.Granted}, nil
}

func (s *v1Server) DecideOAuthConsent(ctx context.Context, req v1gen.DecideOAuthConsentRequestObject) (v1gen.DecideOAuthConsentResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	u, err := s.creds.Authorize(ctx, p.UserID, credentials.AuthorizeRequest{ClientID: b.ClientId,
		RedirectURI: deref(b.RedirectUri), Scope: deref(b.Scope), State: deref(b.State), CodeChallenge: deref(b.CodeChallenge),
		CodeChallengeMethod: deref(b.CodeChallengeMethod)}, b.Approve, clientOf(ctx).IP)
	if prob, ok := oauthProblem(err); ok {
		return prob, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.DecideOAuthConsent200JSONResponse{Url: u}, nil
}
