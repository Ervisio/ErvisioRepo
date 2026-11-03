-- name: GetOIDCLinkBySubject :one
SELECT l.id, l.user_id, l.provider, l.issuer, l.subject, l.email, l.created_at, l.last_login_at,
       n.name AS username, u.role, u.suspended_at
FROM oidc_links l
JOIN users u ON u.id = l.user_id
JOIN namespaces n ON n.id = u.id
WHERE l.issuer = $1 AND l.subject = $2;

-- name: InsertOIDCLink :exec
INSERT INTO oidc_links (id, user_id, provider, issuer, subject, email)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: TouchOIDCLink :exec
UPDATE oidc_links SET last_login_at = now(), email = sqlc.arg(email) WHERE id = $1;

-- name: ListUserOIDCLinks :many
SELECT id, provider, issuer, subject, email, created_at, last_login_at
FROM oidc_links WHERE user_id = $1 ORDER BY created_at;

-- name: GetUserOIDCLink :one
SELECT id, provider, issuer, subject, email, created_at, last_login_at
FROM oidc_links WHERE user_id = $1 AND id = sqlc.arg(link_id);

-- name: CountUserOIDCLinks :one
SELECT count(*) FROM oidc_links WHERE user_id = $1;

-- name: DeleteUserOIDCLink :execrows
DELETE FROM oidc_links WHERE user_id = $1 AND id = sqlc.arg(link_id);

-- name: UserHasPassword :one
SELECT EXISTS (SELECT 1 FROM passwords WHERE user_id = $1);

-- name: InsertOIDCFlow :exec
INSERT INTO oidc_flows (state_hash, provider, mode, nonce, pkce_verifier, user_id, session_id, return_to, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: TakeOIDCFlow :one
-- Consumes the flow: a state is good for a single callback.
DELETE FROM oidc_flows WHERE state_hash = $1
RETURNING state_hash, provider, mode, nonce, pkce_verifier, user_id, session_id, return_to, expires_at;

-- name: DeleteExpiredOIDCFlows :execrows
DELETE FROM oidc_flows WHERE expires_at < now();

-- name: GetUserIDByEmail :one
SELECT user_id FROM emails WHERE address_lower = lower(sqlc.arg(address)::text);
