package sso

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// fakeIdP is a minimal OpenID provider: discovery, JWKS and a token
// endpoint that checks the PKCE verifier. Authorization is simulated by
// authorize, which plays the user consenting at the provider.
type fakeIdP struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu    sync.Mutex
	codes map[string]grant
	// Claims of the next authorization.
	Claims map[string]any
	// BadNonce makes the provider sign a wrong nonce.
	BadNonce bool
}

type grant struct {
	challenge, nonce, clientID string
	claims                     map[string]any
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{t: t, key: key, codes: map[string]grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer": f.Issuer(), "authorization_endpoint": f.Issuer() + "/auth", "token_endpoint": f.Issuer() + "/token",
			"jwks_uri": f.Issuer() + "/keys", "response_types_supported": []string{"code"},
			"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("POST /token", f.token)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) Issuer() string { return f.srv.URL }

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// authorize simulates the user approving the request at authURL and
// returns the callback query the provider redirects to.
func (f *fakeIdP) authorize(authURL string) url.Values {
	f.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		f.t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("state") == "" {
		f.t.Fatalf("authorization request without PKCE, nonce or state: %s", authURL)
	}
	code := b64([]byte(time.Now().String()))
	f.mu.Lock()
	f.codes[code] = grant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), clientID: q.Get("client_id"), claims: f.Claims}
	f.mu.Unlock()
	return url.Values{"code": {code}, "state": {q.Get("state")}}
}

func (f *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	g, ok := f.codes[r.PostForm.Get("code")]
	delete(f.codes, r.PostForm.Get("code"))
	f.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || b64(sum[:]) != g.challenge {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	nonce := g.nonce
	if f.BadNonce {
		nonce = "forged"
	}
	now := time.Now()
	claims := map[string]any{"iss": f.Issuer(), "aud": g.clientID, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": nonce}
	for k, v := range g.claims {
		claims[k] = v
	}
	writeJSON(w, map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": f.sign(claims)})
}

func (f *fakeIdP) sign(claims map[string]any) string {
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	body, _ := json.Marshal(claims)
	in := b64(hdr) + "." + b64(body)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return in + "." + b64(sig)
}
