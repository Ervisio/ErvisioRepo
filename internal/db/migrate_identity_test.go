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

// TestMigrationIdentity checks the constraints of 00020_identity.sql that
// the application relies on.
func TestMigrationIdentity(t *testing.T) {
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
	mustFail := func(what, constraint string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), constraint) {
			t.Fatalf("%s: want %q violation, got %v", what, constraint, err)
		}
	}
	ns := func(kind, name string) (uuid.UUID, error) {
		id := uuid.Must(uuid.NewV7())
		return id, exec(`INSERT INTO namespaces (id, kind, name) VALUES ($1, $2, $3)`, id, kind, name)
	}
	user := func(name string) uuid.UUID {
		t.Helper()
		id, err := ns("user", name)
		if err != nil {
			t.Fatal(err)
		}
		if err := exec(`INSERT INTO users (id) VALUES ($1)`, id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	alice := user("Alice")
	_, err = ns("org", "alice")
	mustFail("org with a user's name", "namespaces_name_lower_key", err)
	for _, bad := range []string{"-x", "x-", "a--b", "a_b", "", strings.Repeat("a", 40)} {
		_, err = ns("user", bad)
		mustFail("name "+bad, "namespaces_name_check", err)
	}
	_, err = ns("team", "teamname")
	mustFail("kind", "namespaces_kind_check", err)
	mustFail("role", "users_role_check", exec(`UPDATE users SET role = 'god' WHERE id = $1`, alice))

	e1 := uuid.Must(uuid.NewV7())
	if err := exec(`INSERT INTO emails (id, user_id, address, is_primary) VALUES ($1, $2, 'Alice@Example.com', true)`, e1, alice); err != nil {
		t.Fatal(err)
	}
	mustFail("duplicate address", "emails_address_lower_key",
		exec(`INSERT INTO emails (id, user_id, address) VALUES ($1, $2, 'alice@example.COM')`, uuid.Must(uuid.NewV7()), alice))
	mustFail("two primaries", "emails_one_primary",
		exec(`INSERT INTO emails (id, user_id, address, is_primary) VALUES ($1, $2, 'b@example.com', true)`, uuid.Must(uuid.NewV7()), alice))

	mustFail("short token hash", "sessions_token_hash_check",
		exec(`INSERT INTO sessions (id, token_hash, user_id, csrf_token, expires_at) VALUES ($1, '\x00', $2, 'c', now())`, uuid.Must(uuid.NewV7()), alice))
	hash := make([]byte, 32)
	if err := exec(`INSERT INTO sessions (id, token_hash, user_id, csrf_token, expires_at) VALUES ($1, $2, $3, 'c', now())`, uuid.Must(uuid.NewV7()), hash, alice); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO one_time_tokens (token_hash, purpose, user_id, email_id, expires_at) VALUES ($1, 'verify_email', $2, $3, now())`, hash, alice, e1); err != nil {
		t.Fatal(err)
	}
	mustFail("token purpose", "one_time_tokens_purpose_check",
		exec(`INSERT INTO one_time_tokens (token_hash, purpose, user_id, expires_at) VALUES ($1, 'login', $2, now())`, make([]byte, 32), alice))
	if err := exec(`INSERT INTO audit_log (id, actor_id, action) VALUES ($1, $2, 'test')`, uuid.Must(uuid.NewV7()), alice); err != nil {
		t.Fatal(err)
	}

	// Deleting the namespace removes the account and its data, not the audit log.
	if err := exec(`DELETE FROM namespaces WHERE id = $1`, alice); err != nil {
		t.Fatal(err)
	}
	var left, audit int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM users) + (SELECT count(*) FROM emails) +
		(SELECT count(*) FROM sessions) + (SELECT count(*) FROM one_time_tokens)`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if left != 0 || audit != 1 {
		t.Fatalf("cascade: %d rows left, %d audit entries", left, audit)
	}
}
