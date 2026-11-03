-- name: InsertSSHKey :exec
INSERT INTO ssh_keys (id, user_id, title, key_type, fingerprint, public_key, bits)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: SSHKeyFingerprintTaken :one
SELECT EXISTS (SELECT 1 FROM ssh_keys WHERE fingerprint = $1);

-- name: ListUserSSHKeys :many
SELECT id, title, key_type, fingerprint, public_key, bits, last_used_at, created_at
FROM ssh_keys WHERE user_id = $1 ORDER BY created_at;

-- name: CountUserSSHKeys :one
SELECT count(*) FROM ssh_keys WHERE user_id = $1;

-- name: GetSSHKeyByFingerprint :one
-- Used by the SSH server (Phase 3) to authenticate a public key.
SELECT k.id, k.user_id, n.name AS username, u.role, u.suspended_at
FROM ssh_keys k
JOIN users u ON u.id = k.user_id
JOIN namespaces n ON n.id = u.id
WHERE k.fingerprint = $1;

-- name: DeleteUserSSHKey :execrows
DELETE FROM ssh_keys WHERE user_id = $1 AND id = sqlc.arg(key_id);

-- name: InsertGPGKey :exec
INSERT INTO gpg_keys (id, user_id, key_id, fingerprint, subkey_ids, emails, armored, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GPGKeyFingerprintTaken :one
SELECT EXISTS (SELECT 1 FROM gpg_keys WHERE fingerprint = $1);

-- name: ListUserGPGKeys :many
SELECT id, key_id, fingerprint, subkey_ids, emails, expires_at, created_at
FROM gpg_keys WHERE user_id = $1 ORDER BY created_at;

-- name: CountUserGPGKeys :one
SELECT count(*) FROM gpg_keys WHERE user_id = $1;

-- name: DeleteUserGPGKey :execrows
DELETE FROM gpg_keys WHERE user_id = $1 AND id = sqlc.arg(key_id);

-- name: ListUserVerifiedEmails :many
SELECT address FROM emails WHERE user_id = $1 AND verified_at IS NOT NULL;

-- name: LockUserForUpdate :one
-- Serializes credential changes of one account.
SELECT id FROM users WHERE id = $1 FOR UPDATE;
