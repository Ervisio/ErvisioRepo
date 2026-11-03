-- name: InsertSession :exec
INSERT INTO sessions (id, token_hash, user_id, csrf_token, ip, user_agent, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetSessionByTokenHash :one
SELECT s.id, s.user_id, s.csrf_token, s.created_at, s.last_seen_at, s.expires_at,
       n.name AS username, u.role, u.suspended_at
FROM sessions s
JOIN users u ON u.id = s.user_id
JOIN namespaces n ON n.id = u.id
WHERE s.token_hash = $1;

-- name: TouchSession :exec
-- Records activity at most once a minute to keep writes low.
UPDATE sessions SET last_seen_at = now()
WHERE id = $1 AND last_seen_at < now() - interval '1 minute';

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = $1;

-- name: DeleteUserSessions :execrows
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteUserSessionsExcept :execrows
DELETE FROM sessions WHERE user_id = $1 AND id <> sqlc.arg(keep);

-- name: ListUserSessions :many
SELECT id, ip, user_agent, created_at, last_seen_at, expires_at
FROM sessions WHERE user_id = $1 ORDER BY last_seen_at DESC;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < now() OR last_seen_at < sqlc.arg(idle_before);
