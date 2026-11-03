package config

import (
	"slices"
	"strings"
	"testing"
)

func TestParseOIDC(t *testing.T) {
	cfg, err := Parse([]byte(minimal+`
[[auth.oidc]]
name = "dex"
display_name = "Company SSO"
issuer = "https://sso.example.com/dex"
client_id = "erepo"
client_secret_file = "/etc/ervisio-repo/dex_secret"
auto_register = true

[[auth.oidc]]
name = "local"
issuer = "http://127.0.0.1:5556/dex"
client_id = "erepo"
scopes = ["groups"]
username_claim = "nickname"
`), noEnv)
	if err != nil {
		t.Fatal(err)
	}
	ps := cfg.Auth.OIDC
	if len(ps) != 2 {
		t.Fatalf("providers = %+v", ps)
	}
	if d := ps[0]; d.Label() != "Company SSO" || !d.AutoRegister || !slices.Equal(d.Scopes, []string{"openid", "email", "profile"}) ||
		d.UsernameClaim != "preferred_username" || d.EmailClaim != "email" || d.NameClaim != "name" {
		t.Fatalf("dex = %+v", d)
	}
	if l := ps[1]; l.Label() != "local" || !slices.Equal(l.Scopes, []string{"openid", "groups"}) || l.UsernameClaim != "nickname" || l.AutoRegister {
		t.Fatalf("local = %+v", l)
	}
	if cfg.Auth.TokenMaxTTL <= 0 {
		t.Fatal("token_max_ttl default")
	}
}

func TestParseOIDCErrors(t *testing.T) {
	_, err := Parse([]byte(minimal+`
[auth]
token_max_ttl = "0s"

[[auth.oidc]]
name = "Bad Name"
issuer = "http://sso.example.com"
client_secret_file = "secret"
scopes = ["openid", "two words"]

[[auth.oidc]]
name = "dup"
issuer = "ftp://x"
client_id = "a"

[[auth.oidc]]
name = "dup"
issuer = "https://x.example.com/?a=b"
client_id = "a"

[[auth.oidc]]
name = "noissuer"
client_id = "a"
`), noEnv)
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{
		"auth.token_max_ttl: must be positive",
		"auth.oidc[0].name: must match",
		"auth.oidc[0].issuer: must use https",
		"auth.oidc[0].client_id: is required",
		"auth.oidc[0].client_secret_file: must be an absolute path",
		"auth.oidc[0].scopes[1]: invalid scope",
		"auth.oidc[1].issuer: must be an absolute https URL",
		"auth.oidc[2].name: duplicate provider name",
		"auth.oidc[2].issuer: must not have a query or fragment",
		"auth.oidc[3].issuer: is required",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}
