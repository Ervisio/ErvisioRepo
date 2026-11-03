package httpapi

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

var nonceRe = regexp.MustCompile(`'nonce-([A-Za-z0-9+/=]+)'`)

// The SPA page gets a fresh, unpredictable style nonce per response; every
// other response keeps the strict policy without nonces.
func TestSecCSPNonce(t *testing.T) {
	h := newTestRouter(t, func(d *Deps) {
		d.Web = fstest.MapFS{
			"index.html": {Data: []byte(`<meta property="csp-nonce" nonce="__EREPO_CSP_NONCE__">` +
				`<script type="module" src="/assets/a.js" nonce="__EREPO_CSP_NONCE__"></script>`)},
			"assets/a.js": {Data: []byte("1")},
		}
	})
	seen := map[string]bool{}
	for range 5 {
		rec := do(h, http.MethodGet, "/some/route")
		csp := rec.Header().Get("Content-Security-Policy")
		m := nonceRe.FindStringSubmatch(csp)
		if m == nil {
			t.Fatalf("no nonce in CSP %q", csp)
		}
		nonce := m[1]
		if len(nonce) < 24 || seen[nonce] {
			t.Fatalf("weak or reused nonce %q", nonce)
		}
		seen[nonce] = true
		body := rec.Body.String()
		if strings.Contains(body, cspNoncePlaceholder) || strings.Count(body, `nonce="`+nonce+`"`) != 2 {
			t.Fatalf("placeholder not replaced: %s", body)
		}
		for _, bad := range []string{"unsafe-inline", "unsafe-eval"} {
			if strings.Contains(csp, bad) {
				t.Fatalf("CSP contains %s: %q", bad, csp)
			}
		}
		if !strings.Contains(csp, "script-src 'self';") {
			t.Fatalf("script-src must stay 'self' only: %q", csp)
		}
	}
	if rec := do(h, http.MethodHead, "/"); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: %d %q", rec.Code, rec.Body)
	}
	for _, p := range []string{"/assets/a.js", "/api/v1/meta"} {
		if csp := do(h, http.MethodGet, p).Header().Get("Content-Security-Policy"); csp != contentSecurityPolicy {
			t.Fatalf("%s: CSP %q", p, csp)
		}
	}
}
