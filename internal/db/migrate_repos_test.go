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

// TestMigrationRepos checks the constraints of 00060_repos.sql that the
// application relies on.
func TestMigrationRepos(t *testing.T) {
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
	repo := func(owner uuid.UUID, name string) (uuid.UUID, error) {
		id := uuid.Must(uuid.NewV7())
		return id, exec(`INSERT INTO repos (id, owner_id, name) VALUES ($1, $2, $3)`, id, owner, name)
	}
	alice := ns("user", "alice")
	org := ns("org", "acme")

	r1, err := repo(alice, "Project")
	must(err)
	_, err = repo(alice, "project")
	mustFail("case-insensitive duplicate", "repos_owner_id_name_lower_key", err)
	_, err = repo(org, "project")
	must(err)
	for _, bad := range []string{"", ".x", "-x", "a/b", "a b", "x.git", "X.Git", strings.Repeat("a", 101)} {
		_, err = repo(alice, bad)
		mustFail("name "+bad, "repos_name_check", err)
	}
	mustFail("visibility", "repos_visibility_check",
		exec(`UPDATE repos SET visibility = 'secret' WHERE id = $1`, r1))
	mustFail("storage state", "repos_storage_state_check",
		exec(`UPDATE repos SET storage_state = 'lukewarm' WHERE id = $1`, r1))
	mustFail("negative size", "repos_size_bytes_check",
		exec(`UPDATE repos SET size_bytes = -1 WHERE id = $1`, r1))

	// team_repos now references repos and follows their deletion.
	team := uuid.Must(uuid.NewV7())
	must(exec(`INSERT INTO teams (id, org_id, name) VALUES ($1, $2, 'devs')`, team, org))
	mustFail("team grant on missing repo", "team_repos_repo_id_fkey",
		exec(`INSERT INTO team_repos (team_id, repo_id, permission) VALUES ($1, $2, 'read')`, team, uuid.New()))
	must(exec(`INSERT INTO team_repos (team_id, repo_id, permission) VALUES ($1, $2, 'write')`, team, r1))

	bob := ns("user", "bob")
	must(exec(`INSERT INTO repo_collaborators (repo_id, user_id, permission) VALUES ($1, $2, 'write')`, r1, bob))
	mustFail("collaborator none", "repo_collaborators_permission_check",
		exec(`UPDATE repo_collaborators SET permission = 'none' WHERE repo_id = $1`, r1))
	must(exec(`INSERT INTO repo_redirects (owner_id, name_lower, repo_id) VALUES ($1, 'old', $2)`, alice, r1))

	// A namespace with repositories cannot be deleted.
	mustFail("delete owner", "repos_owner_id_fkey", exec(`DELETE FROM namespaces WHERE id = $1`, alice))

	must(exec(`DELETE FROM repos WHERE id = $1`, r1))
	var n int
	must(pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM team_repos) + (SELECT count(*) FROM repo_collaborators)
		+ (SELECT count(*) FROM repo_redirects)`).Scan(&n))
	if n != 0 {
		t.Fatalf("%d rows survive the repository", n)
	}
	// Deleting a user drops his grants.
	r2, err := repo(org, "second")
	must(err)
	must(exec(`INSERT INTO repo_collaborators (repo_id, user_id, permission) VALUES ($1, $2, 'read')`, r2, bob))
	must(exec(`DELETE FROM namespaces WHERE id = $1`, bob))
	must(pool.QueryRow(ctx, `SELECT count(*) FROM repo_collaborators`).Scan(&n))
	if n != 0 {
		t.Fatal("collaborator survives the user")
	}
}
