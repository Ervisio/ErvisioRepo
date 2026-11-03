-- name: InsertToken :exec
INSERT INTO tokens (id, user_id, name, token_hash, hint, scopes, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetTokenByHash :one
SELECT t.id, t.user_id, t.scopes, t.expires_at, t.last_used_at,
       n.name AS username, u.role, u.suspended_at
FROM tokens t
JOIN users u ON u.id = t.user_id
JOIN namespaces n ON n.id = u.id
WHERE t.token_hash = $1;

-- name: TouchToken :exec
-- Records use at most once a minute to keep writes low.
UPDATE tokens SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: ListUserTokens :many
SELECT id, name, hint, scopes, expires_at, last_used_at, created_at
FROM tokens WHERE user_id = $1 ORDER BY created_at DESC;

-- name: CountUserTokens :one
SELECT count(*) FROM tokens WHERE user_id = $1;

-- name: TokenNameTaken :one
SELECT EXISTS (SELECT 1 FROM tokens WHERE user_id = $1 AND name = sqlc.arg(name));

-- name: DeleteUserToken :execrows
DELETE FROM tokens WHERE user_id = $1 AND id = sqlc.arg(token_id);

-- name: DeleteExpiredTokens :execrows
DELETE FROM tokens WHERE expires_at < sqlc.arg(before);
