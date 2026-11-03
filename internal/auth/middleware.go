package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
)

// ProblemWriter writes an RFC 9457 error response.
type ProblemWriter func(w http.ResponseWriter, status int, detail string)

// Middleware loads the session of each request and enforces CSRF and
// authorization.
type Middleware struct {
	Sessions       *Sessions
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
		ri := RequestInfo{IP: ClientIP(r, m.TrustedProxies), UserAgent: r.UserAgent(), SessionToken: m.Cookies.Session(r)}
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

// CSRF rejects unsafe requests that a browser marks as cross-site, and those
// whose X-CSRF-Token header does not match the CSRF cookie and, with a
// session, the token bound to the session (double submit plus synchronizer
// token). It must run after LoadSession.
func (m *Middleware) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if safeMethod(r.Method) {
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

// Scopes enforces the security requirement of an OpenAPI operation, given
// the scopes the generated router put in the context: no value means a
// public operation, an empty list a signed-in user, "admin" an instance
// admin.
func (m *Middleware) Scopes(scopes func(context.Context) ([]string, bool)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		user, admin := m.RequireUser(next), m.RequireAdmin(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s, secured := scopes(r.Context())
			switch {
			case !secured:
				next.ServeHTTP(w, r)
			case slices.Contains(s, RoleAdmin):
				admin.ServeHTTP(w, r)
			default:
				user.ServeHTTP(w, r)
			}
		})
	}
}
