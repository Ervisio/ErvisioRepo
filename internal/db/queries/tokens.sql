-- name: InsertToken :exec
INSERT INTO tokens (id, user_id, name, token_hash, hint, scopes, expires_at, is_ervisiocode_device)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetTokenByHash :one
-- app_id is set for a user-to-server token of an app (a grant of the
-- app's OAuth client).
SELECT t.id, t.user_id, t.scopes, t.expires_at, t.last_used_at, t.is_ervisiocode_device,
       n.name AS username, u.role, u.suspended_at, a.id AS app_id
FROM tokens t
JOIN users u ON u.id = t.user_id
JOIN namespaces n ON n.id = u.id
LEFT JOIN oauth_grants g ON g.id = t.grant_id
LEFT JOIN apps a ON a.oauth_app_id = g.app_id
WHERE t.token_hash = $1;

-- name: TouchToken :exec
-- Records use at most once a minute to keep writes low.
UPDATE tokens SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: ListUserTokens :many
SELECT id, name, hint, scopes, expires_at, last_used_at, created_at
FROM tokens WHERE user_id = $1 AND grant_id IS NULL AND NOT is_ervisiocode_device ORDER BY created_at DESC;

-- name: CountUserTokens :one
-- PATs and first-party device credentials share a limit; app tokens are bounded per grant.
SELECT count(*) FROM tokens WHERE user_id = $1 AND grant_id IS NULL;

-- name: TokenNameTaken :one
SELECT EXISTS (SELECT 1 FROM tokens WHERE user_id = $1 AND name = sqlc.arg(name));

-- name: DeleteUserToken :execrows
DELETE FROM tokens WHERE user_id = $1 AND id = sqlc.arg(token_id)
  AND grant_id IS NULL AND NOT is_ervisiocode_device;

-- name: DeleteExpiredTokens :execrows
DELETE FROM tokens WHERE expires_at < sqlc.arg(before);

-- name: ListErvisioCodeDevices :many
SELECT id, name, expires_at, last_used_at, created_at FROM tokens
WHERE user_id = $1 AND is_ervisiocode_device AND expires_at > now()
ORDER BY created_at DESC, id DESC;

-- name: GetErvisioCodeDevice :one
SELECT id, name, expires_at, last_used_at, created_at FROM tokens
WHERE user_id = $1 AND id = sqlc.arg(token_id) AND is_ervisiocode_device AND expires_at > now();

-- name: DeleteErvisioCodeDevice :execrows
DELETE FROM tokens WHERE user_id = $1 AND id = sqlc.arg(token_id) AND is_ervisiocode_device;
