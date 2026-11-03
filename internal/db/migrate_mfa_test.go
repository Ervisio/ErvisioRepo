package db_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbtest"
)

// TestMigrationMFA checks the constraints of 00030_mfa.sql that the
// application relies on.
func TestMigrationMFA(t *testing.T) {
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
	user := func(name string) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		must(exec(`INSERT INTO namespaces (id, kind, name) VALUES ($1, 'user', $2)`, id, name))
		must(exec(`INSERT INTO users (id) VALUES ($1)`, id))
		return id
	}
	alice, bob := user("alice"), user("bob")
	h32 := bytes.Repeat([]byte{1}, 32)
	exp := time.Now().Add(time.Minute)

	must(exec(`INSERT INTO mfa_totp (user_id, secret) VALUES ($1, $2)`, alice, bytes.Repeat([]byte{0}, 21)))
	mustFail("short secret", "mfa_totp_secret_check",
		exec(`INSERT INTO mfa_totp (user_id, secret) VALUES ($1, $2)`, bob, []byte{0, 1}))
	mustFail("second secret", "mfa_totp_pkey",
		exec(`INSERT INTO mfa_totp (user_id, secret) VALUES ($1, $2)`, alice, bytes.Repeat([]byte{0}, 21)))
	mustFail("negative step", "mfa_totp_last_step_check",
		exec(`UPDATE mfa_totp SET last_step = -1 WHERE user_id = $1`, alice))

	must(exec(`INSERT INTO mfa_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`, uuid.New(), alice, h32))
	mustFail("duplicate code", "mfa_recovery_codes_user_id_code_hash_key",
		exec(`INSERT INTO mfa_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`, uuid.New(), alice, h32))
	must(exec(`INSERT INTO mfa_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`, uuid.New(), bob, h32))
	mustFail("bad hash", "mfa_recovery_codes_code_hash_check",
		exec(`INSERT INTO mfa_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`, uuid.New(), alice, []byte{1}))

	must(exec(`INSERT INTO webauthn_handles (user_id, handle) VALUES ($1, $2)`, alice, h32))
	mustFail("shared handle", "webauthn_handles_handle_key",
		exec(`INSERT INTO webauthn_handles (user_id, handle) VALUES ($1, $2)`, bob, h32))
	must(exec(`INSERT INTO webauthn_credentials (id, user_id, credential_id, name, data) VALUES ($1, $2, $3, 'key', '{}')`, uuid.New(), alice, []byte{1, 2}))
	mustFail("shared credential", "webauthn_credentials_credential_id_key",
		exec(`INSERT INTO webauthn_credentials (id, user_id, credential_id, name, data) VALUES ($1, $2, $3, 'key', '{}')`, uuid.New(), bob, []byte{1, 2}))
	mustFail("empty name", "webauthn_credentials_name_check",
		exec(`INSERT INTO webauthn_credentials (id, user_id, credential_id, name, data) VALUES ($1, $2, $3, '', '{}')`, uuid.New(), bob, []byte{3}))

	must(exec(`INSERT INTO mfa_challenges (id, token_hash, kind, user_id, expires_at) VALUES ($1, $2, 'login', $3, $4)`, uuid.New(), h32, alice, exp))
	mustFail("duplicate token", "mfa_challenges_token_hash_key",
		exec(`INSERT INTO mfa_challenges (id, token_hash, kind, user_id, expires_at) VALUES ($1, $2, 'login', $3, $4)`, uuid.New(), h32, bob, exp))
	mustFail("login without user", "mfa_challenges_check",
		exec(`INSERT INTO mfa_challenges (id, token_hash, kind, expires_at) VALUES ($1, $2, 'login', $3)`, uuid.New(), bytes.Repeat([]byte{2}, 32), exp))
	mustFail("passkey login with user", "mfa_challenges_check",
		exec(`INSERT INTO mfa_challenges (id, token_hash, kind, user_id, expires_at) VALUES ($1, $2, 'passkey_login', $3, $4)`, uuid.New(), bytes.Repeat([]byte{3}, 32), alice, exp))
	mustFail("unknown kind", "mfa_challenges_kind_check",
		exec(`INSERT INTO mfa_challenges (id, token_hash, kind, user_id, expires_at) VALUES ($1, $2, 'sms', $3, $4)`, uuid.New(), bytes.Repeat([]byte{4}, 32), alice, exp))

	var req bool
	org := uuid.Must(uuid.NewV7())
	must(exec(`INSERT INTO namespaces (id, kind, name) VALUES ($1, 'org', 'acme')`, org))
	must(exec(`INSERT INTO orgs (id) VALUES ($1)`, org))
	must(pool.QueryRow(ctx, `SELECT require_2fa FROM orgs WHERE id = $1`, org).Scan(&req))
	if req {
		t.Fatal("require_2fa defaults to true")
	}

	// Deleting the account removes every factor.
	must(exec(`DELETE FROM users WHERE id = $1`, alice))
	var n int
	must(pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mfa_totp WHERE user_id = $1)
		+ (SELECT count(*) FROM mfa_recovery_codes WHERE user_id = $1)
		+ (SELECT count(*) FROM webauthn_credentials WHERE user_id = $1)
		+ (SELECT count(*) FROM mfa_challenges WHERE user_id = $1)`, alice).Scan(&n))
	if n != 0 {
		t.Fatalf("%d MFA rows left after deleting the account", n)
	}
}
