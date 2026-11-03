package config

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
)

// OIDCProvider is an external OpenID Connect identity provider (PLAN §8.1),
// declared as [[auth.oidc]]. Accounts are linked to issuer#sub, never to an
// email address.
type OIDCProvider struct {
	// Name identifies the provider in URLs (/auth/oidc/<name>/...).
	Name        string `toml:"name"`
	DisplayName string `toml:"display_name"`
	// Issuer is the issuer URL; discovery reads
	// <issuer>/.well-known/openid-configuration. https is required except
	// on loopback hosts.
	Issuer   string `toml:"issuer"`
	ClientID string `toml:"client_id"`
	// ClientSecretFile holds the client secret; empty for public clients,
	// which rely on PKCE alone.
	ClientSecretFile string   `toml:"client_secret_file"`
	Scopes           []string `toml:"scopes"`
	// Claims read from the ID token (and the userinfo endpoint).
	UsernameClaim string `toml:"username_claim"`
	EmailClaim    string `toml:"email_claim"`
	NameClaim     string `toml:"name_claim"`
	// AutoRegister creates an account at the first login of an identity
	// that is not linked yet, whatever security.registration says. Without
	// it, users must sign in first and link the provider from their
	// settings.
	AutoRegister bool `toml:"auto_register"`
}

// Label is the name shown to users.
func (p OIDCProvider) Label() string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.Name
}

func applyOIDCDefaults(ps []OIDCProvider) {
	for i := range ps {
		p := &ps[i]
		if len(p.Scopes) == 0 {
			p.Scopes = []string{"openid", "email", "profile"}
		} else if !slices.Contains(p.Scopes, "openid") {
			p.Scopes = append([]string{"openid"}, p.Scopes...)
		}
		if p.UsernameClaim == "" {
			p.UsernameClaim = "preferred_username"
		}
		if p.EmailClaim == "" {
			p.EmailClaim = "email"
		}
		if p.NameClaim == "" {
			p.NameClaim = "name"
		}
	}
}

func (v *validator) oidc(ps []OIDCProvider) {
	seen := map[string]bool{}
	for i, p := range ps {
		path := fmt.Sprintf("auth.oidc[%d]", i)
		switch {
		case !targetName.MatchString(p.Name):
			v.add(path+".name", "must match %s, got %q", targetName, p.Name)
		case seen[p.Name]:
			v.add(path+".name", "duplicate provider name %q", p.Name)
		default:
			seen[p.Name] = true
		}
		if v.required(path+".issuer", p.Issuer) {
			u, err := url.Parse(p.Issuer)
			switch {
			case err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http"):
				v.add(path+".issuer", "must be an absolute https URL")
			case u.Scheme == "http" && !loopbackHost(u.Hostname()):
				v.add(path+".issuer", "must use https (plain http is only allowed on loopback hosts)")
			case u.RawQuery != "" || u.Fragment != "":
				v.add(path+".issuer", "must not have a query or fragment")
			}
		}
		v.required(path+".client_id", p.ClientID)
		if p.ClientSecretFile != "" && !filepath.IsAbs(p.ClientSecretFile) {
			v.add(path+".client_secret_file", "must be an absolute path")
		}
		for j, s := range p.Scopes {
			if s == "" || strings.ContainsAny(s, " \t\r\n\"") {
				v.add(fmt.Sprintf("%s.scopes[%d]", path, j), "invalid scope %q", s)
			}
		}
	}
}

func loopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
