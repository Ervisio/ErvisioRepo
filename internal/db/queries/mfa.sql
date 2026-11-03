-- Second factors and two-step login (PLAN §8.1).

-- name: GetTOTP :one
SELECT user_id, secret, confirmed_at, last_step, created_at FROM mfa_totp WHERE user_id = $1;

-- name: UpsertPendingTOTP :execrows
-- Starts (or restarts) a TOTP enrollment; a confirmed secret is kept.
INSERT INTO mfa_totp (user_id, secret) VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET secret = EXCLUDED.secret, last_step = 0, created_at = now()
WHERE mfa_totp.confirmed_at IS NULL;

-- name: ConfirmTOTP :execrows
UPDATE mfa_totp SET confirmed_at = now(), last_step = sqlc.arg(step)
WHERE user_id = $1 AND confirmed_at IS NULL AND last_step < sqlc.arg(step);

-- name: AdvanceTOTPStep :execrows
-- Accepts a code for step only once: a replay in the same window matches
-- no row.
UPDATE mfa_totp SET last_step = sqlc.arg(step)
WHERE user_id = $1 AND confirmed_at IS NOT NULL AND last_step < sqlc.arg(step);

-- name: DeleteTOTP :execrows
DELETE FROM mfa_totp WHERE user_id = $1;

-- name: InsertRecoveryCode :exec
INSERT INTO mfa_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3);

-- name: DeleteRecoveryCodes :exec
DELETE FROM mfa_recovery_codes WHERE user_id = $1;

-- name: UseRecoveryCode :execrows
UPDATE mfa_recovery_codes SET used_at = now()
WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL;

-- name: CountRecoveryCodes :one
SELECT count(*) FILTER (WHERE used_at IS NULL) AS unused, count(*) AS total
FROM mfa_recovery_codes WHERE user_id = $1;

-- name: GetWebAuthnHandle :one
SELECT handle FROM webauthn_handles WHERE user_id = $1;

-- name: InsertWebAuthnHandle :exec
INSERT INTO webauthn_handles (user_id, handle) VALUES ($1, $2) ON CONFLICT (user_id) DO NOTHING;

-- name: GetUserByWebAuthnHandle :one
SELECT user_id FROM webauthn_handles WHERE handle = $1;

-- name: ListWebAuthnCredentials :many
SELECT id, user_id, credential_id, name, data, created_at, last_used_at
FROM webauthn_credentials WHERE user_id = $1 ORDER BY created_at;

-- name: CountWebAuthnCredentials :one
SELECT count(*) FROM webauthn_credentials WHERE user_id = $1;

-- name: InsertWebAuthnCredential :exec
INSERT INTO webauthn_credentials (id, user_id, credential_id, name, data) VALUES ($1, $2, $3, $4, $5);

-- name: UpdateWebAuthnCredentialUse :exec
UPDATE webauthn_credentials SET data = $3, last_used_at = now()
WHERE user_id = $1 AND credential_id = $2;

-- name: DeleteWebAuthnCredential :execrows
DELETE FROM webauthn_credentials WHERE id = $1 AND user_id = $2;

-- name: InsertMFAChallenge :exec
INSERT INTO mfa_challenges (id, token_hash, kind, user_id, enroll, webauthn, ip, user_agent, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetMFAChallenge :one
SELECT id, kind, user_id, enroll, webauthn, attempts, expires_at
FROM mfa_challenges WHERE token_hash = $1 AND kind = $2 AND expires_at > now();

-- name: SetMFAChallengeWebAuthn :execrows
UPDATE mfa_challenges SET webauthn = $2 WHERE id = $1 AND expires_at > now();

-- name: FailMFAChallenge :one
UPDATE mfa_challenges SET attempts = attempts + 1 WHERE id = $1 RETURNING attempts;

-- name: ConsumeMFAChallenge :execrows
-- Deletes the challenge; a second use (or a concurrent one) matches no row.
DELETE FROM mfa_challenges WHERE id = $1 AND expires_at > now();

-- name: DeleteMFAChallenge :exec
DELETE FROM mfa_challenges WHERE id = $1;

-- name: DeleteExpiredMFAChallenges :execrows
DELETE FROM mfa_challenges WHERE expires_at <= now();

-- name: UserInRequire2FAOrg :one
SELECT EXISTS (
    SELECT 1 FROM org_members m JOIN orgs o ON o.id = m.org_id
    WHERE m.user_id = $1 AND o.require_2fa
) AS required;
