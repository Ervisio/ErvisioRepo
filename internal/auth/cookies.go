package auth

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Cookie base names. On a secure origin they carry the __Host- prefix, which
// the browser accepts only with Secure, Path=/ and no Domain.
const (
	SessionCookie = "erepo_session"
	CSRFCookie    = "erepo_csrf"
	// CSRFHeader carries the CSRF token on unsafe requests.
	CSRFHeader = "X-CSRF-Token"
)

// Cookies sets and reads the authentication cookies.
type Cookies struct {
	Secure bool
}

// CookiesFor derives the cookie settings from the public URL: Secure (and
// the __Host- prefix) on https and on loopback hosts, where browsers allow
// secure cookies over plain http.
func CookiesFor(publicURL string) Cookies {
	u, err := url.Parse(publicURL)
	if err != nil {
		return Cookies{}
	}
	if u.Scheme == "https" {
		return Cookies{Secure: true}
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return Cookies{Secure: true}
	}
	ip := net.ParseIP(host)
	return Cookies{Secure: ip != nil && ip.IsLoopback()}
}

func (c Cookies) name(base string) string {
	if c.Secure {
		return "__Host-" + base
	}
	return base
}

// SessionName and CSRFName are the cookie names in use.
func (c Cookies) SessionName() string { return c.name(SessionCookie) }

// CSRFName is the name of the CSRF cookie.
func (c Cookies) CSRFName() string { return c.name(CSRFCookie) }

// SetSession sets the session cookie, which expires with the session.
func (c Cookies) SetSession(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the public URL scheme (CookiesFor)
		Name: c.SessionName(), Value: token, Path: "/", Expires: expires,
		MaxAge: int(time.Until(expires).Seconds()), Secure: c.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// SetCSRF sets the CSRF cookie. It is readable by scripts, so that the SPA
// can echo it in the X-CSRF-Token header.
func (c Cookies) SetCSRF(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // script-readable by design; Secure follows the public URL
		Name: c.CSRFName(), Value: token, Path: "/", Secure: c.Secure, SameSite: http.SameSiteLaxMode,
	})
}

// ClearSession expires the session cookie.
func (c Cookies) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the public URL scheme (CookiesFor)
		Name: c.SessionName(), Value: "", Path: "/", MaxAge: -1, Secure: c.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// Session returns the session token of r, if any.
func (c Cookies) Session(r *http.Request) string { return cookieValue(r, c.SessionName()) }

// CSRF returns the CSRF cookie of r, if any.
func (c Cookies) CSRF(r *http.Request) string { return cookieValue(r, c.CSRFName()) }

func cookieValue(r *http.Request, name string) string {
	ck, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return ck.Value
}
