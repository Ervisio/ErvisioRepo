package mfa

import (
	"net/http"
	"time"
)

// CookieBase is the name of the cookie that holds a pending login. It is
// never a session: it only lets the browser complete the second step.
const CookieBase = "erepo_mfa"

// CookieName returns the cookie name: __Host- prefixed when the session
// cookies are Secure.
func CookieName(secure bool) string {
	if secure {
		return "__Host-" + CookieBase
	}
	return CookieBase
}

// SetCookie stores the pending login token.
func SetCookie(w http.ResponseWriter, secure bool, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the public URL scheme (CookiesFor)
		Name: CookieName(secure), Value: token, Path: "/", Expires: expires,
		MaxAge: max(1, int(time.Until(expires).Seconds())), Secure: secure, HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearCookie drops the pending login cookie.
func ClearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the public URL scheme (CookiesFor)
		Name: CookieName(secure), Value: "", Path: "/", MaxAge: -1, Secure: secure, HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// FromRequest returns the pending login token of r, if any.
func FromRequest(r *http.Request, secure bool) string {
	c, err := r.Cookie(CookieName(secure))
	if err != nil {
		return ""
	}
	return c.Value
}
