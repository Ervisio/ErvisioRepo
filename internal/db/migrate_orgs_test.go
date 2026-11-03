package db_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbtest"
)

// TestMigrationOrgs checks the constraints of 00050_orgs_teams.sql that the
// application relies on.
func TestMigrationOrgs(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, config.Database{URL: dbtest.URL(t), MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) error {
		_, err := pool.Exec(ctx, sql, args...)
		return err
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	mustFail := func(what, constraint string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), constraint) {
			t.Fatalf("%s: want %q violation, got %v", what, constraint, err)
		}
	}
	ns := func(kind, name string) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		must(exec(`INSERT INTO namespaces (id, kind, name) VALUES ($1, $2, $3)`, id, kind, name))
		if kind == "user" {
			must(exec(`INSERT INTO users (id) VALUES ($1)`, id))
		} else {
			must(exec(`INSERT INTO orgs (id) VALUES ($1)`, id))
		}
		return id
	}
	alice, bob := ns("user", "alice"), ns("user", "bob")
	org, other := ns("org", "acme"), ns("org", "other")

	must(exec(`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'admin')`, org, alice))
	mustFail("unknown org role", "org_members_role_check",
		exec(`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'root')`, org, bob))
	mustFail("bad base permission", "orgs_base_permission_check",
		exec(`UPDATE orgs SET base_permission = 'owner' WHERE id = $1`, org))
	var base string
	must(pool.QueryRow(ctx, `SELECT base_permission FROM orgs WHERE id = $1`, org).Scan(&base))
	if base != "read" {
		t.Fatalf("default base permission %q", base)
	}

	team := uuid.Must(uuid.NewV7())
	must(exec(`INSERT INTO teams (id, org_id, name) VALUES ($1, $2, 'Core')`, team, org))
	mustFail("duplicate team name", "teams_org_id_name_lower_key",
		exec(`INSERT INTO teams (id, org_id, name) VALUES ($1, $2, 'core')`, uuid.New(), org))
	must(exec(`INSERT INTO teams (id, org_id, name) VALUES ($1, $2, 'core')`, uuid.New(), other))
	mustFail("bad team name", "teams_name_check",
		exec(`INSERT INTO teams (id, org_id, name) VALUES ($1, $2, 'a b')`, uuid.New(), org))

	must(exec(`INSERT INTO team_members (team_id, org_id, user_id) VALUES ($1, $2, $3)`, team, org, alice))
	mustFail("team member outside the org", "team_members_org_id_user_id_fkey",
		exec(`INSERT INTO team_members (team_id, org_id, user_id) VALUES ($1, $2, $3)`, team, org, bob))
	mustFail("team repo permission none", "team_repos_permission_check",
		exec(`INSERT INTO team_repos (team_id, repo_id, permission) VALUES ($1, $2, 'none')`, team, uuid.New()))
	// Since 00060 team_repos references repos.
	repoID := uuid.New()
	must(exec(`INSERT INTO repos (id, owner_id, name) VALUES ($1, $2, 'r')`, repoID, org))
	must(exec(`INSERT INTO team_repos (team_id, repo_id, permission) VALUES ($1, $2, 'write')`, team, repoID))

	// Leaving the org drops the team memberships.
	must(exec(`DELETE FROM org_members WHERE org_id = $1 AND user_id = $2`, org, alice))
	var n int
	must(pool.QueryRow(ctx, `SELECT count(*) FROM team_members WHERE user_id = $1`, alice).Scan(&n))
	if n != 0 {
		t.Fatalf("%d team memberships left", n)
	}

	inv := func(email string) error {
		return exec(`INSERT INTO org_invites (id, org_id, email, role, expires_at) VALUES ($1, $2, $3, 'member', now() + interval '1 day')`,
			uuid.New(), org, email)
	}
	must(inv("Carol@example.com"))
	mustFail("second pending invite", "org_invites_pending", inv("carol@EXAMPLE.com"))
	must(exec(`UPDATE org_invites SET accepted_at = now() WHERE email_lower = 'carol@example.com'`))
	must(inv("carol@example.com"))
	mustFail("short token hash", "org_invites_token_hash_check",
		exec(`UPDATE org_invites SET token_hash = '\x00' WHERE accepted_at IS NULL`))

	var kinds int
	must(pool.QueryRow(ctx, `SELECT count(*) FROM quota_defaults WHERE max_repos IS NULL AND max_storage_bytes IS NULL`).Scan(&kinds))
	if kinds != 2 {
		t.Fatalf("%d default quota rows", kinds)
	}
	mustFail("negative quota", "quotas_max_repos_check",
		exec(`INSERT INTO quotas (namespace_id, max_repos) VALUES ($1, -1)`, org))
	mustFail("negative usage", "usage_counters_repos_check",
		exec(`INSERT INTO usage_counters (namespace_id, repos) VALUES ($1, -1)`, org))

	// Deleting the org cascades to teams, invites and quotas; audit entries stay.
	must(exec(`INSERT INTO quotas (namespace_id, max_repos) VALUES ($1, 5)`, org))
	must(exec(`INSERT INTO audit_log (id, action, org_id) VALUES ($1, 'org.created', $2)`, uuid.New(), org))
	must(exec(`DELETE FROM repos WHERE id = $1`, repoID)) // repositories block the deletion (00060)
	must(exec(`DELETE FROM namespaces WHERE id = $1`, org))
	for _, table := range []string{"teams", "org_invites"} {
		must(pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE org_id = $1`, org).Scan(&n))
		if n != 0 {
			t.Fatalf("%s: %d rows left", table, n)
		}
	}
	must(pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE org_id = $1`, org).Scan(&n))
	if n != 1 {
		t.Fatal("audit entry removed with the org")
	}
}
