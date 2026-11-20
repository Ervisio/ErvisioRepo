package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

const (
	ervisioCodePath   = "/api/v1/user/ervisiocode"
	hubSessionPath    = ervisioCodePath + "/hub-session"
	remoteSessionPath = ervisioCodePath + "/sessions"
	remoteProjectPath = ervisioCodePath + "/projects"
	remoteCatalogPath = ervisioCodePath + "/catalog"
	remoteRelayPath   = ervisioCodePath + "/relay"
	remoteSharePath   = ervisioCodePath + "/shares"
	hubSessionCookie  = "erepo_hub_session"
)

type hubSessionKey struct{}

func sharingRoute(path, method string) bool {
	if path == remoteSharePath {
		return method == http.MethodGet || method == http.MethodPost
	}
	if path == remoteSharePath+"/accept" {
		return method == http.MethodPost
	}
	if path == remoteSharePath+"/recipients" {
		return method == http.MethodGet
	}
	tail, ok := strings.CutPrefix(path, remoteSharePath+"/")
	if !ok {
		return false
	}
	parts := strings.Split(tail, "/")
	if !sharePathID(parts[0]) {
		return false
	}
	switch len(parts) {
	case 1:
		return method == http.MethodDelete
	case 2:
		return parts[1] == "state" && method == http.MethodGet ||
			parts[1] == "accept" && method == http.MethodPost ||
			parts[1] == "proposals" && (method == http.MethodGet || method == http.MethodPost)
	case 4:
		return parts[1] == "proposals" && sharePathID(parts[2]) && parts[3] == "review" && method == http.MethodPost
	default:
		return false
	}
}

func sharePathID(value string) bool {
	_, err := uuid.Parse(value)
	return len(value) == 36 && err == nil
}

func hubSessionAuth(m *auth.Middleware, publicURL string) func(http.Handler) http.Handler {
	origin := hubOrigin(publicURL)
	return func(next http.Handler) http.Handler {
		browserAndBearer := m.LoadV1Session(m.CSRF(next))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, selected := r.URL.Query()["accountId"]
			sharing := sharingRoute(r.URL.Path, r.Method)
			catalog := r.URL.Path == remoteCatalogPath && r.Method == http.MethodGet
			if sharing || catalog {
				if r.URL.EscapedPath() != r.URL.Path || len(r.Header.Values("Authorization")) != 0 {
					writeProblem(w, http.StatusUnauthorized, "a browser or scoped ErvisioCode hub session is required")
					return
				}
				query, err := url.ParseQuery(r.URL.RawQuery)
				accounts := query["accountId"]
				selected = accounts != nil
				if err != nil || selected && (len(accounts) != 1 || !sharePathID(accounts[0])) {
					writeProblem(w, http.StatusBadRequest, "expected one accountId for the ErvisioCode hub session")
					return
				}
				if !validSharingOrigin(r, origin) {
					writeProblem(w, http.StatusForbidden, "a same-origin HTTPS request is required")
					return
				}
			}
			hubRead := r.URL.Path == hubSessionPath && r.Method == http.MethodGet
			hubRemote := selected && len(r.Header.Values("Authorization")) == 0 &&
				(sharing || catalog || (r.URL.Path == remoteSessionPath || r.URL.Path == remoteProjectPath) &&
					(r.Method == http.MethodGet || r.Method == http.MethodPost))
			if !hubRead && !hubRemote {
				browserAndBearer.ServeHTTP(w, r)
				return
			}
			if hubRemote && !sharing && r.Method == http.MethodPost && !validHubOrigin(r, origin) {
				writeProblem(w, http.StatusForbidden, "a same-origin HTTPS hub request is required")
				return
			}
			cookies := r.CookiesNamed(hubSessionCookie)
			if len(cookies) != 1 || !auth.ValidTokenFormat(cookies[0].Value) {
				writeProblem(w, http.StatusUnauthorized, "an ErvisioCode hub session is required")
				return
			}
			// The hub cookie is not an ErvisioRepo principal and never falls back to one.
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), hubSessionKey{}, cookies[0].Value)))
		})
	}
}

func validSharingOrigin(r *http.Request, origin string) bool {
	if r.Method != http.MethodGet || len(r.Header.Values("Origin")) != 0 {
		return validHubOrigin(r, origin)
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	default:
		return false
	}
}

func hubOrigin(publicURL string) string {
	u, err := url.Parse(publicURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	host := strings.ToLower(u.Host)
	if u.Port() == "443" {
		host = strings.TrimSuffix(host, ":443")
	}
	return "https://" + host
}

func validHubOrigin(r *http.Request, origin string) bool {
	values := r.Header.Values("Origin")
	if origin == "" || len(values) != 1 || values[0] != origin {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin":
		return true
	default:
		return false
	}
}

func authorizeHubSession(authorize v1gen.MiddlewareFunc) v1gen.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		normal := authorize(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, hub := r.Context().Value(hubSessionKey{}).(string)
			_, permitted := r.Context().Value(v1gen.HubSessionScopes).([]string)
			if hub && permitted {
				next.ServeHTTP(w, r)
				return
			}
			normal.ServeHTTP(w, r)
		})
	}
}

func (p problemResponse) VisitCreateErvisioCodeHubSessionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetErvisioCodeHubSessionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (s *v1Server) CreateErvisioCodeHubSession(ctx context.Context, _ v1gen.CreateErvisioCodeHubSessionRequestObject) (v1gen.CreateErvisioCodeHubSessionResponseObject, error) {
	p, ok := caller(ctx)
	if !ok || p.Method != auth.MethodToken || !p.ErvisioCodeDevice {
		return errAuthRequired, nil
	}
	if hubOrigin(s.meta.PublicURL) == "" {
		return problemResponse{status: http.StatusForbidden, detail: "a public HTTPS URL is required for a hub session"}, nil
	}
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	secret, err := s.creds.CreateErvisioCodeHubSession(ctx, p.UserID, p.TokenID)
	if errors.Is(err, credentials.ErrNotFound) {
		return errAuthRequired, nil
	}
	if err != nil {
		return nil, err
	}
	cookie := http.Cookie{Name: hubSessionCookie, Value: secret, Path: ervisioCodePath + "/",
		MaxAge: int(credentials.ErvisioCodeHubSessionTTL / time.Second),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}
	return v1gen.CreateErvisioCodeHubSession204Response{
		Headers: v1gen.CreateErvisioCodeHubSession204ResponseHeaders{SetCookie: cookie.String()},
	}, nil
}

func (s *v1Server) GetErvisioCodeHubSession(ctx context.Context, req v1gen.GetErvisioCodeHubSessionRequestObject) (v1gen.GetErvisioCodeHubSessionResponseObject, error) {
	secret, ok := ctx.Value(hubSessionKey{}).(string)
	if !ok {
		return errAuthRequired, nil
	}
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	accountID := req.Params.AccountId
	username, err := s.creds.GetErvisioCodeHubSession(ctx, accountID, secret)
	if errors.Is(err, credentials.ErrNotFound) {
		return errAuthRequired, nil
	}
	if err != nil {
		return nil, err
	}
	devices, err := s.creds.ListErvisioCodeDevices(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := v1gen.GetErvisioCodeHubSession200JSONResponse{
		AccountId: accountID, Username: username, Devices: ervisioCodeDevicesDTO(devices),
	}
	if s.identity != nil {
		user, err := s.identity.User(ctx, accountID)
		if errors.Is(err, identity.ErrNotFound) {
			return errAuthRequired, nil
		}
		if err != nil {
			return nil, err
		}
		if user.DisplayName != "" {
			out.DisplayName = &user.DisplayName
		}
		if user.AvatarURL != "" {
			out.AvatarUrl = &user.AvatarURL
		}
	}
	return out, nil
}
