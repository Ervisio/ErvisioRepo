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

// TestMigrationCredentials checks the constraints of 00040_oidc_tokens_keys.sql
// that the application relies on.
func TestMigrationCredentials(t *testing.T) {
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
	ok := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	id := func() uuid.UUID { return uuid.Must(uuid.NewV7()) }
	user := func(name string) uuid.UUID {
		t.Helper()
		u := id()
		ok(exec(`INSERT INTO namespaces (id, kind, name) VALUES ($1, 'user', $2)`, u, name))
		ok(exec(`INSERT INTO users (id) VALUES ($1)`, u))
		return u
	}
	alice, bob := user("alice"), user("bob")

	// OIDC links: one account per issuer#subject, one link per provider.
	link := `INSERT INTO oidc_links (id, user_id, provider, issuer, subject) VALUES ($1, $2, $3, $4, $5)`
	ok(exec(link, id(), alice, "dex", "https://idp", "sub-1"))
	mustFail("same identity twice", "oidc_links_issuer_subject_key", exec(link, id(), bob, "dex2", "https://idp", "sub-1"))
	mustFail("two links per provider", "oidc_links_user_id_provider_key", exec(link, id(), alice, "dex", "https://idp", "sub-2"))
	mustFail("provider name", "oidc_links_provider_check", exec(link, id(), bob, "Bad Name", "https://idp", "sub-3"))

	// OIDC flows: link mode needs the user and the session.
	flow := `INSERT INTO oidc_flows (state_hash, provider, mode, nonce, pkce_verifier, user_id, session_id, expires_at)
		VALUES ($1, 'dex', $2, 'n', 'v', $3, $4, now())`
	h := make([]byte, 32)
	mustFail("link without session", "oidc_flows_check", exec(flow, h, "link", alice, nil))
	mustFail("login with user", "oidc_flows_check", exec(flow, h, "login", alice, nil))
	mustFail("mode", "oidc_flows_", exec(flow, h, "steal", nil, nil))
	mustFail("short state", "oidc_flows_state_hash_check", exec(flow, h[:3], "login", nil, nil))
	ok(exec(flow, h, "login", nil, nil))

	// Tokens: unique hash and per-user name.
	tok := `INSERT INTO tokens (id, user_id, name, token_hash, hint, expires_at) VALUES ($1, $2, $3, $4, 'erp_x', now())`
	h2 := make([]byte, 32)
	h2[0] = 1
	ok(exec(tok, id(), alice, "ci", h2))
	mustFail("same hash", "tokens_token_hash_key", exec(tok, id(), bob, "ci", h2))
	h3 := make([]byte, 32)
	h3[0] = 2
	mustFail("same name", "tokens_user_id_name_key", exec(tok, id(), alice, "ci", h3))
	mustFail("empty name", "tokens_name_check", exec(tok, id(), alice, "", h3))

	// SSH and GPG keys belong to one account.
	ssh := `INSERT INTO ssh_keys (id, user_id, title, key_type, fingerprint, public_key) VALUES ($1, $2, 'k', 'ssh-ed25519', 'FP', 'ssh-ed25519 AAAA')`
	ok(exec(ssh, id(), alice))
	mustFail("same ssh key", "ssh_keys_fingerprint_key", exec(ssh, id(), bob))
	gpg := `INSERT INTO gpg_keys (id, user_id, key_id, fingerprint, armored) VALUES ($1, $2, 'ABCD', 'FPGPG', 'x')`
	ok(exec(gpg, id(), alice))
	mustFail("same gpg key", "gpg_keys_fingerprint_key", exec(gpg, id(), bob))

	// Deleting the account removes its credentials.
	ok(exec(`DELETE FROM users WHERE id = $1`, alice))
	for _, table := range []string{"oidc_links", "tokens", "ssh_keys", "gpg_keys"} {
		var n int
		ok(pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE user_id = $1`, alice).Scan(&n))
		if n != 0 {
			t.Fatalf("%s: %d rows left", table, n)
		}
	}
}
