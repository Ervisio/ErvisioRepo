package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

// ProblemWriter writes an RFC 9457 error response.
type ProblemWriter func(w http.ResponseWriter, status int, detail string)

// Middleware loads the session of each request and enforces CSRF and
// authorization.
type Middleware struct {
	Sessions       *Sessions
	Tokens         *Tokens // nil refuses every access token
	Cookies        Cookies
	TrustedProxies []netip.Prefix
	Problem        ProblemWriter
	Log            *slog.Logger
}

type requestInfoKey struct{}

// RequestInfo describes the client of a request.
type RequestInfo struct {
	IP        string
	UserAgent string
	// SessionToken is the session cookie as sent, valid or not.
	SessionToken string
	// CSRFToken is the token of the live session, empty without one.
	CSRFToken string
}

// RequestInfoFrom returns the client information stored by LoadSession.
func RequestInfoFrom(ctx context.Context) RequestInfo {
	ri, _ := ctx.Value(requestInfoKey{}).(RequestInfo)
	return ri
}

// WithRequestInfo returns ctx carrying ri (for tests and internal callers).
func WithRequestInfo(ctx context.Context, ri RequestInfo) context.Context {
	return context.WithValue(ctx, requestInfoKey{}, ri)
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// LoadSession authenticates the request from its session cookie, if any, and
// records the client information. It never rejects a request: a missing or
// invalid session leaves it anonymous (and clears a stale cookie). It also
// issues the CSRF cookie to clients that do not have one yet.
func (m *Middleware) LoadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ri := RequestInfo{IP: ClientIP(r, m.TrustedProxies), UserAgent: r.UserAgent()}
		if bearer, ok := bearerToken(r); ok {
			// An access token replaces the cookies: the session cookie of
			// the same client is ignored, and no CSRF cookie is issued.
			p, err := m.lookupToken(ctx, bearer)
			switch {
			case err == nil:
				next.ServeHTTP(w, r.WithContext(WithRequestInfo(WithPrincipal(ctx, p), ri)))
			case errors.Is(err, ErrNoToken):
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				m.Problem(w, http.StatusUnauthorized, "invalid or expired access token")
			default:
				m.Log.ErrorContext(ctx, "load access token", "error", err)
				m.Problem(w, http.StatusServiceUnavailable, "")
			}
			return
		}
		ri.SessionToken = m.Cookies.Session(r)
		if ri.SessionToken != "" && m.Sessions != nil {
			s, err := m.Sessions.Lookup(ctx, ri.SessionToken)
			switch {
			case err == nil:
				ctx = WithPrincipal(ctx, s.Principal())
				ri.CSRFToken = s.CSRFToken
				if m.Cookies.CSRF(r) != s.CSRFToken {
					m.Cookies.SetCSRF(w, s.CSRFToken)
				}
			case errors.Is(err, ErrNoSession):
				m.Cookies.ClearSession(w)
			default:
				m.Log.ErrorContext(ctx, "load session", "error", err)
				m.Problem(w, http.StatusServiceUnavailable, "")
				return
			}
		}
		if ri.CSRFToken == "" && !ValidTokenFormat(m.Cookies.CSRF(r)) {
			t, _ := NewToken()
			m.Cookies.SetCSRF(w, t)
		}
		next.ServeHTTP(w, r.WithContext(WithRequestInfo(ctx, ri)))
	})
}

// bearerToken returns the credentials of an "Authorization: Bearer" header.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	scheme, cred, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(cred), true
}

func (m *Middleware) lookupToken(ctx context.Context, token string) (Principal, error) {
	if m.Tokens == nil {
		return Principal{}, ErrNoToken
	}
	return m.Tokens.Lookup(ctx, token)
}

// CSRF rejects unsafe requests that a browser marks as cross-site, and those
// whose X-CSRF-Token header does not match the CSRF cookie and, with a
// session, the token bound to the session (double submit plus synchronizer
// token). Access token callers are exempt: a browser never attaches the
// token on its own. It must run after LoadSession.
func (m *Middleware) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, _ := PrincipalFrom(r.Context()); safeMethod(r.Method) || p.Method == MethodToken {
			next.ServeHTTP(w, r)
			return
		}
		switch r.Header.Get("Sec-Fetch-Site") {
		case "", "same-origin", "none":
		default:
			m.Problem(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		header := r.Header.Get(CSRFHeader)
		cookie := m.Cookies.CSRF(r)
		ok := ValidTokenFormat(header) && EqualTokens(header, cookie)
		if want := RequestInfoFrom(r.Context()).CSRFToken; want != "" {
			ok = ok && EqualTokens(header, want)
		}
		if !ok {
			m.Problem(w, http.StatusForbidden, "missing or invalid CSRF token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireUser answers 401 to anonymous requests.
func (m *Middleware) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PrincipalFrom(r.Context()); !ok {
			m.Problem(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin answers 401 to anonymous requests and 403 to callers that
// are not instance admins.
func (m *Middleware) RequireAdmin(next http.Handler) http.Handler {
	return m.RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, _ := PrincipalFrom(r.Context()); !p.IsAdmin() {
			m.Problem(w, http.StatusForbidden, "instance admin required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// Scopes enforces the security requirement of an OpenAPI operation for
// session callers only; access tokens are refused on every secured
// operation. See Authorize.
func (m *Middleware) Scopes(scopes func(context.Context) ([]string, bool)) func(http.Handler) http.Handler {
	return m.Authorize(scopes, nil)
}

// ScopeAnonymous in the session scheme of an operation lets callers without
// a session through (the principal is then the zero value): the handler
// decides what they may see, as for public repositories. Signed-in callers
// and access tokens are checked as usual.
const ScopeAnonymous = "anonymous"

// Authorize enforces the security requirement of an OpenAPI operation,
// given the scopes the generated router put in the context for the session
// scheme and for the access token scheme. An operation with neither is
// public. Session (or anonymous) callers need the session scheme: an empty
// list means a signed-in user, "admin" an instance admin. Access token
// callers need the token scheme and every scope it lists; "admin" also
// requires an instance admin. token may be nil (no operation accepts
// tokens).
func (m *Middleware) Authorize(session, token func(context.Context) ([]string, bool)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		user, admin := m.RequireUser(next), m.RequireAdmin(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ss, sessionOK := session(ctx)
			var ts []string
			tokenOK := false
			if token != nil {
				ts, tokenOK = token(ctx)
			}
			p, _ := PrincipalFrom(ctx)
			switch {
			case !sessionOK && !tokenOK:
				next.ServeHTTP(w, r)
			case p.Method == MethodToken:
				m.authorizeToken(w, r, next, p, ts, tokenOK)
			case !sessionOK:
				// Only tokens are accepted here.
				m.Problem(w, http.StatusUnauthorized, "access token required")
			case slices.Contains(ss, ScopeAnonymous):
				next.ServeHTTP(w, r)
			case slices.Contains(ss, RoleAdmin):
				admin.ServeHTTP(w, r)
			default:
				user.ServeHTTP(w, r)
			}
		})
	}
}

func (m *Middleware) authorizeToken(w http.ResponseWriter, r *http.Request, next http.Handler, p Principal, scopes []string, accepted bool) {
	if !accepted {
		m.Problem(w, http.StatusForbidden, "this operation does not accept access tokens")
		return
	}
	for _, s := range scopes {
		if !p.HasScope(s) {
			w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+strings.Join(scopes, " ")+`"`)
			m.Problem(w, http.StatusForbidden, "the access token lacks the scope "+s)
			return
		}
	}
	if slices.Contains(scopes, ScopeAdmin) && !p.IsAdmin() {
		m.Problem(w, http.StatusForbidden, "instance admin required")
		return
	}
	next.ServeHTTP(w, r)
}
