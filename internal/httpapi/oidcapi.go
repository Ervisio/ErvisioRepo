package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/sso"
)

var errSSOUnavailable = problemResponse{status: http.StatusNotFound, detail: "unknown identity provider"}

// ssoProblem maps the errors of the sso package.
func ssoProblem(err error) (problemResponse, bool) {
	var se *sso.Error
	switch {
	case err == nil:
		return problemResponse{}, false
	case errors.Is(err, sso.ErrNotFound), errors.Is(err, sso.ErrUnknownProvider):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, true
	case errors.Is(err, sso.ErrLastMethod):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, true
	case errors.Is(err, sso.ErrProvider):
		return problemResponse{status: http.StatusBadGateway, detail: err.Error()}, true
	case errors.Is(err, sso.ErrLinkSession):
		return problemResponse{status: http.StatusUnauthorized, detail: err.Error()}, true
	case errors.As(err, &se):
		return badRequest(se.Error()), true
	}
	return problemResponse{}, false
}

func (p problemResponse) VisitListOidcProvidersResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListOidcLinksResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitBeginOidcLinkResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitUnlinkOidcResponse(w http.ResponseWriter) error    { return p.write(w) }

func (s *v1Server) ListOidcProviders(context.Context, v1gen.ListOidcProvidersRequestObject) (v1gen.ListOidcProvidersResponseObject, error) {
	list := s.sso.Providers()
	out := make(v1gen.ListOidcProviders200JSONResponse, 0, len(list))
	for _, p := range list {
		out = append(out, v1gen.OidcProvider{Name: p.Name, Label: p.Label})
	}
	return out, nil
}

func (s *v1Server) ListOidcLinks(ctx context.Context, _ v1gen.ListOidcLinksRequestObject) (v1gen.ListOidcLinksResponseObject, error) {
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	out := v1gen.ListOidcLinks200JSONResponse{}
	if s.sso == nil {
		return out, nil
	}
	links, err := s.sso.Links(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		dto := v1gen.OidcLink{Id: l.ID, Provider: l.Provider, Label: l.Label, CreatedAt: l.CreatedAt, LastLoginAt: l.LastLoginAt}
		if l.Email != "" {
			e := l.Email
			dto.Email = &e
		}
		out = append(out, dto)
	}
	return out, nil
}

// linkResponse returns the provider URL and binds the flow to the browser.
type linkResponse struct {
	url, state string
	m          *sso.Manager
}

func (l linkResponse) VisitBeginOidcLinkResponse(w http.ResponseWriter) error {
	l.m.SetStateCookie(w, l.state)
	w.Header().Set("Cache-Control", "no-store")
	return v1gen.BeginOidcLink200JSONResponse{Url: l.url}.VisitBeginOidcLinkResponse(w)
}

func (s *v1Server) BeginOidcLink(ctx context.Context, req v1gen.BeginOidcLinkRequestObject) (v1gen.BeginOidcLinkResponseObject, error) {
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	if !s.sso.Has(req.Provider) {
		return errSSOUnavailable, nil
	}
	returnTo := "/settings"
	if req.Body != nil && req.Body.ReturnTo != nil {
		returnTo = *req.Body.ReturnTo
	}
	redirect, state, err := s.sso.Begin(ctx, req.Provider, sso.ModeLink, &p, returnTo)
	if pr, ok := ssoProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return linkResponse{url: redirect, state: state, m: s.sso}, nil
}

func (s *v1Server) UnlinkOidc(ctx context.Context, req v1gen.UnlinkOidcRequestObject) (v1gen.UnlinkOidcResponseObject, error) {
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	if s.sso == nil {
		return problemResponse{status: http.StatusNotFound, detail: sso.ErrNotFound.Error()}, nil
	}
	err := s.sso.Unlink(ctx, p.UserID, req.Id, clientOf(ctx).IP)
	if pr, ok := ssoProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.UnlinkOidc204Response{}, nil
}

// mountOIDC mounts the browser endpoints of the sign-in flow, outside the
// REST API: GET /auth/oidc/{provider}/login redirects to the provider and
// GET /auth/oidc/{provider}/callback completes the flow, then redirects to
// the stored return path, or to the SPA with an oidc_error code.
func mountOIDC(r chi.Router, d Deps) {
	if d.SSO == nil {
		return
	}
	m, cookies, log := d.SSO, d.Auth.Cookies, d.Log
	r.Route("/auth/oidc/{provider}", func(r chi.Router) {
		r.Use(d.Auth.LoadSession)
		r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
			name := chi.URLParam(r, "provider")
			redirect, state, err := m.Begin(r.Context(), name, sso.ModeLogin, nil, r.URL.Query().Get("return_to"))
			if err != nil {
				oidcFailure(w, r, log, "/login", err)
				return
			}
			m.SetStateCookie(w, state)
			noStoreRedirect(w, r, redirect)
		})
		r.Get("/callback", func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			q := r.URL.Query()
			ri := auth.RequestInfoFrom(ctx)
			cb := sso.Callback{Provider: chi.URLParam(r, "provider"), State: q.Get("state"), Code: q.Get("code"),
				Cookie: m.StateFromRequest(r), ProviderError: q.Get("error"), OldToken: ri.SessionToken, Client: clientOf(ctx)}
			fallback := "/login"
			if p, ok := auth.PrincipalFrom(ctx); ok && p.Method == auth.MethodSession {
				cb.Current = &p
				fallback = "/settings"
			}
			m.ClearStateCookie(w)
			res, err := m.Complete(ctx, cb)
			if err != nil {
				oidcFailure(w, r, log, fallback, err)
				return
			}
			if res.Login != nil {
				cookies.SetSession(w, res.Login.Session.Token, res.Login.Session.ExpiresAt)
				cookies.SetCSRF(w, res.Login.Session.CSRFToken)
			}
			noStoreRedirect(w, r, res.ReturnTo)
		})
	})
}

// oidcFailure redirects to path with the error code for the SPA. Errors
// that are not sso.Error are logged and reported as server_error.
func oidcFailure(w http.ResponseWriter, r *http.Request, log *slog.Logger, path string, err error) {
	code := "server_error"
	var se *sso.Error
	if errors.As(err, &se) {
		code = se.Code
	} else {
		log.ErrorContext(r.Context(), "oidc flow failed", "error", err)
	}
	noStoreRedirect(w, r, path+"?"+url.Values{"oidc_error": {code}}.Encode())
}

// noStoreRedirect redirects without caching. Targets are either the
// provider authorization URL built by the sso package or a local path that
// sso.SafeReturnTo already sanitized, so this is not an open redirect.
func noStoreRedirect(w http.ResponseWriter, r *http.Request, to string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, to, http.StatusFound) //nolint:gosec // target is sanitized, see above

}
