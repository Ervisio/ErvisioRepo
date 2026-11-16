-- name: InsertToken :exec
INSERT INTO tokens (id, user_id, name, token_hash, hint, scopes, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetTokenByHash :one
-- app_id is set for a user-to-server token of an app (a grant of the
-- app's OAuth client).
SELECT t.id, t.user_id, t.scopes, t.expires_at, t.last_used_at,
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
FROM tokens WHERE user_id = $1 AND grant_id IS NULL ORDER BY created_at DESC;

-- name: CountUserTokens :one
-- Personal access tokens only: OAuth app tokens are bounded per grant.
SELECT count(*) FROM tokens WHERE user_id = $1 AND grant_id IS NULL;

-- name: TokenNameTaken :one
SELECT EXISTS (SELECT 1 FROM tokens WHERE user_id = $1 AND name = sqlc.arg(name));

-- name: DeleteUserToken :execrows
DELETE FROM tokens WHERE user_id = $1 AND id = sqlc.arg(token_id) AND grant_id IS NULL;

-- name: DeleteExpiredTokens :execrows
DELETE FROM tokens WHERE expires_at < sqlc.arg(before);
