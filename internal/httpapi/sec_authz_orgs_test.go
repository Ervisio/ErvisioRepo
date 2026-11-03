package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/identity/identitytest"
	"github.com/Ervisio/ErvisioRepo/internal/orgs"
)

// orgsRouter is a router on real identity and organization services, with
// open registration and mail disabled (invitation tokens are returned).
func orgsRouter(t *testing.T) http.Handler {
	t.Helper()
	t.Parallel()
	env := identitytest.New(t, func(c *config.Config) {
		c.Mail.Enabled = false
		c.Security.Registration = config.RegistrationOpen
	})
	return newTestRouter(t, func(d *Deps) {
		d.Identity = env.Service
		d.Auth = &auth.Middleware{Sessions: env.Sessions, Cookies: auth.CookiesFor(env.Config.Server.PublicURL)}
		d.Storage = &fakeStorage{}
		d.Meta.Registration = env.Config.Security.Registration
		d.Orgs = orgs.New(orgs.Options{Pool: env.Pool, Jobs: env.Jobs, Config: env.Config})
		d.Audit = env.Queries
	})
}

// signUp registers and signs in name; the first account is the instance admin.
func signUp(t *testing.T, h http.Handler, name, ip string) *browser {
	t.Helper()
	b := newBrowser(t, h, ip).prime()
	if rec := b.register(name, name+"@example.com", testPassword); rec.Code != http.StatusAccepted {
		t.Fatalf("register %s: %d %s", name, rec.Code, rec.Body)
	}
	if rec := b.login(name, testPassword); rec.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", name, rec.Code, rec.Body)
	}
	return b
}

func mustStatus(t *testing.T, what string, got, want int, body any) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status %d, want %d; body %s", what, got, want, body)
	}
}

// join invites name into org with role and makes them accept.
func join(t *testing.T, owner, user *browser, org, name, role string) {
	t.Helper()
	rec := owner.do(http.MethodPost, "/api/v1/orgs/"+org+"/invites", map[string]any{"email": name + "@example.com", "role": role})
	mustStatus(t, "invite "+name, rec.Code, http.StatusCreated, rec.Body)
	var inv struct{ Token string }
	if err := json.Unmarshal(rec.Body.Bytes(), &inv); err != nil || inv.Token == "" {
		t.Fatalf("invite token: %s %v", rec.Body, err)
	}
	rec = user.do(http.MethodPost, "/api/v1/orgs/invites/accept", map[string]string{"token": inv.Token})
	mustStatus(t, "accept "+name, rec.Code, http.StatusOK, rec.Body)
	// The token is single use.
	assertProblem(t, user.do(http.MethodPost, "/api/v1/orgs/invites/accept", map[string]string{"token": inv.Token}), http.StatusBadRequest)
}

func TestSecAuthzOrgRoles(t *testing.T) {
	h := orgsRouter(t)
	root := signUp(t, h, "boss", "198.51.100.20") // instance admin
	owner := signUp(t, h, "olive", "198.51.100.21")
	adm := signUp(t, h, "adam", "198.51.100.22")
	mem := signUp(t, h, "mia", "198.51.100.23")
	outsider := signUp(t, h, "otto", "198.51.100.24")

	rec := owner.do(http.MethodPost, "/api/v1/orgs", map[string]string{"name": "acme"})
	mustStatus(t, "create org", rec.Code, http.StatusCreated, rec.Body)
	join(t, owner, adm, "acme", "adam", "admin")
	join(t, owner, mem, "acme", "mia", "member")
	rec = outsider.do(http.MethodPost, "/api/v1/orgs", map[string]string{"name": "other"})
	mustStatus(t, "create other org", rec.Code, http.StatusCreated, rec.Body)
	rec = owner.do(http.MethodPost, "/api/v1/orgs/acme/teams", map[string]string{"name": "core"})
	mustStatus(t, "create team", rec.Code, http.StatusCreated, rec.Body)

	t.Run("admin cannot promote to owner", func(t *testing.T) {
		assertProblem(t, adm.do(http.MethodPut, "/api/v1/orgs/acme/members/mia", map[string]string{"role": "owner"}), http.StatusForbidden)
		assertProblem(t, adm.do(http.MethodPut, "/api/v1/orgs/acme/members/adam", map[string]string{"role": "owner"}), http.StatusForbidden)
		assertProblem(t, adm.do(http.MethodPost, "/api/v1/orgs/acme/invites", map[string]string{"email": "x@example.com", "role": "owner"}), http.StatusForbidden)
		assertProblem(t, adm.do(http.MethodPut, "/api/v1/orgs/acme/members/olive", map[string]string{"role": "member"}), http.StatusForbidden)
		assertProblem(t, adm.do(http.MethodDelete, "/api/v1/orgs/acme/members/olive", nil), http.StatusForbidden)
		assertProblem(t, adm.do(http.MethodDelete, "/api/v1/orgs/acme", nil), http.StatusForbidden)
		// A plain member cannot manage anything.
		assertProblem(t, mem.do(http.MethodPut, "/api/v1/orgs/acme/members/mia", map[string]string{"role": "admin"}), http.StatusForbidden)
		assertProblem(t, mem.do(http.MethodPatch, "/api/v1/orgs/acme", map[string]string{"description": "x"}), http.StatusForbidden)
		assertProblem(t, mem.do(http.MethodPost, "/api/v1/orgs/acme/teams", map[string]string{"name": "x"}), http.StatusForbidden)
		assertProblem(t, mem.do(http.MethodGet, "/api/v1/orgs/acme/audit", nil), http.StatusForbidden)
		assertProblem(t, mem.do(http.MethodGet, "/api/v1/orgs/acme/invites", nil), http.StatusForbidden)
		// Admins still manage non-owners.
		rec := adm.do(http.MethodPut, "/api/v1/orgs/acme/members/mia", map[string]string{"role": "admin"})
		mustStatus(t, "admin promotes to admin", rec.Code, http.StatusOK, rec.Body)
		rec = adm.do(http.MethodPut, "/api/v1/orgs/acme/members/mia", map[string]string{"role": "member"})
		mustStatus(t, "admin demotes", rec.Code, http.StatusOK, rec.Body)
	})

	t.Run("last owner cannot be removed", func(t *testing.T) {
		assertProblem(t, owner.do(http.MethodDelete, "/api/v1/orgs/acme/members/olive", nil), http.StatusConflict)
		assertProblem(t, owner.do(http.MethodPut, "/api/v1/orgs/acme/members/olive", map[string]string{"role": "admin"}), http.StatusConflict)
		// With a second owner the first one may step down.
		rec := owner.do(http.MethodPut, "/api/v1/orgs/acme/members/adam", map[string]string{"role": "owner"})
		mustStatus(t, "owner promotes", rec.Code, http.StatusOK, rec.Body)
		rec = owner.do(http.MethodPut, "/api/v1/orgs/acme/members/olive", map[string]string{"role": "admin"})
		mustStatus(t, "owner steps down", rec.Code, http.StatusOK, rec.Body)
		assertProblem(t, adm.do(http.MethodDelete, "/api/v1/orgs/acme/members/adam", nil), http.StatusConflict)
	})

	t.Run("member of another org sees nothing", func(t *testing.T) {
		for _, p := range []string{"/api/v1/orgs/acme", "/api/v1/orgs/acme/members", "/api/v1/orgs/acme/teams",
			"/api/v1/orgs/acme/teams/core", "/api/v1/orgs/acme/teams/core/members", "/api/v1/orgs/acme/invites",
			"/api/v1/orgs/acme/quota", "/api/v1/orgs/acme/audit", "/api/v1/orgs/nope"} {
			assertProblem(t, outsider.do(http.MethodGet, p, nil), http.StatusNotFound)
		}
		assertProblem(t, outsider.do(http.MethodPatch, "/api/v1/orgs/acme", map[string]string{"description": "x"}), http.StatusNotFound)
		assertProblem(t, outsider.do(http.MethodDelete, "/api/v1/orgs/acme/members/mia", nil), http.StatusNotFound)
		assertProblem(t, outsider.do(http.MethodPut, "/api/v1/orgs/acme/teams/core/members/otto", map[string]string{"role": "member"}), http.StatusNotFound)
		assertProblem(t, outsider.do(http.MethodPost, "/api/v1/orgs/acme/invites", map[string]string{"email": "y@example.com", "role": "member"}), http.StatusNotFound)
		// Their own list holds only their organization.
		rec := outsider.do(http.MethodGet, "/api/v1/orgs", nil)
		mustStatus(t, "list orgs", rec.Code, http.StatusOK, rec.Body)
		if body := rec.Body.String(); strings.Contains(body, "acme") || !strings.Contains(body, "other") {
			t.Fatalf("outsider orgs = %s", body)
		}
		// The members of acme cannot see the other organization either.
		assertProblem(t, mem.do(http.MethodGet, "/api/v1/orgs/other", nil), http.StatusNotFound)
	})

	t.Run("instance audit log is for instance admins", func(t *testing.T) {
		assertProblem(t, owner.do(http.MethodGet, "/api/v1/admin/audit", nil), http.StatusForbidden)
		assertProblem(t, newBrowser(t, h, "198.51.100.29").prime().do(http.MethodGet, "/api/v1/admin/audit", nil), http.StatusUnauthorized)
		rec := root.do(http.MethodGet, "/api/v1/admin/audit?action=org.&limit=2", nil)
		mustStatus(t, "admin audit", rec.Code, http.StatusOK, rec.Body)
		var page struct {
			Entries []struct{ Action string }
			Next    string
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Entries) != 2 || page.Next == "" {
			t.Fatalf("admin audit page = %s %v", rec.Body, err)
		}
		rec = root.do(http.MethodGet, "/api/v1/admin/audit?action=org.&limit=2&before="+page.Next, nil)
		mustStatus(t, "admin audit next", rec.Code, http.StatusOK, rec.Body)
		// The org audit log is for the org's owners and admins.
		rec = owner.do(http.MethodGet, "/api/v1/orgs/acme/audit", nil)
		mustStatus(t, "org audit", rec.Code, http.StatusOK, rec.Body)
		if strings.Contains(rec.Body.String(), `"name":"other"`) {
			t.Fatalf("org audit leaks another org: %s", rec.Body)
		}
	})
}
