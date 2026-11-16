-- OAuth apps, their client secrets, grants and authorization codes
-- (APPS-PLAN A1). Device codes are in device_codes.sql.

-- name: GetOAuthAppByClientID :one
SELECT a.*, COALESCE(n.kind, '')::text AS owner_kind, COALESCE(n.name, '')::text AS owner_name
FROM oauth_apps a
LEFT JOIN namespaces n ON n.id = a.owner_id
WHERE a.client_id = $1;

-- name: ListOAuthAppsByOwner :many
SELECT a.*, n.kind AS owner_kind, n.name AS owner_name,
       (SELECT count(*) FROM oauth_grants g WHERE g.app_id = a.id)::bigint AS users
FROM oauth_apps a
JOIN namespaces n ON n.id = a.owner_id
WHERE a.owner_id = $1
  AND NOT EXISTS (SELECT 1 FROM apps x WHERE x.oauth_app_id = a.id)
ORDER BY a.created_at DESC;

-- name: CountOAuthAppsByOwner :one
SELECT count(*) FROM oauth_apps o WHERE o.owner_id = $1 AND NOT EXISTS (SELECT 1 FROM apps x WHERE x.oauth_app_id = o.id);

-- name: CountOAuthAppUsers :one
SELECT count(*) FROM oauth_grants WHERE app_id = $1;

-- name: InsertOAuthApp :exec
INSERT INTO oauth_apps (id, client_id, owner_id, name, description, homepage_url, logo_url, callback_urls,
                        device_flow, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: UpdateOAuthApp :exec
UPDATE oauth_apps SET name = $2, description = $3, homepage_url = $4, logo_url = $5, callback_urls = $6,
                      device_flow = $7, updated_at = now()
WHERE id = $1 AND NOT is_system;

-- name: DeleteOAuthApp :execrows
DELETE FROM oauth_apps WHERE id = $1 AND NOT is_system;

-- name: InsertOAuthAppSecret :exec
INSERT INTO oauth_app_secrets (id, app_id, secret_hash, hint, created_by) VALUES ($1, $2, $3, $4, $5);

-- name: ListOAuthAppSecrets :many
SELECT s.id, s.hint, s.created_at, s.last_used_at, COALESCE(n.name, '')::text AS created_by_name
FROM oauth_app_secrets s
LEFT JOIN namespaces n ON n.id = s.created_by
WHERE s.app_id = $1
ORDER BY s.created_at DESC;

-- name: CountOAuthAppSecrets :one
SELECT count(*) FROM oauth_app_secrets WHERE app_id = $1;

-- name: DeleteOAuthAppSecret :execrows
DELETE FROM oauth_app_secrets WHERE app_id = $1 AND id = sqlc.arg(secret_id);

-- name: FindOAuthAppSecret :one
SELECT id FROM oauth_app_secrets WHERE app_id = $1 AND secret_hash = $2;

-- name: TouchOAuthAppSecret :exec
UPDATE oauth_app_secrets SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: GetOAuthGrant :one
SELECT * FROM oauth_grants WHERE app_id = $1 AND user_id = $2;

-- name: UpsertOAuthGrant :one
-- Adds scopes to the grant of (app, user), creating it when missing.
INSERT INTO oauth_grants (id, app_id, user_id, scopes) VALUES ($1, $2, $3, $4)
ON CONFLICT (app_id, user_id) DO UPDATE
SET scopes = ARRAY(SELECT DISTINCT unnest(oauth_grants.scopes || EXCLUDED.scopes) ORDER BY 1), updated_at = now()
RETURNING *, (xmax = 0)::boolean AS inserted;

-- name: ListUserOAuthGrants :many
SELECT g.id, g.scopes, g.created_at, a.client_id, a.name, a.description, a.homepage_url, a.logo_url, a.is_system,
       COALESCE(n.kind, '')::text AS owner_kind, COALESCE(n.name, '')::text AS owner_name,
       (SELECT count(*) FROM tokens t WHERE t.grant_id = g.id)::bigint AS tokens,
       (SELECT max(t.last_used_at) FROM tokens t WHERE t.grant_id = g.id)::timestamptz AS last_used_at
FROM oauth_grants g
JOIN oauth_apps a ON a.id = g.app_id
LEFT JOIN namespaces n ON n.id = a.owner_id
WHERE g.user_id = $1
ORDER BY last_used_at DESC NULLS LAST, g.created_at DESC;

-- name: DeleteUserOAuthGrant :one
DELETE FROM oauth_grants WHERE user_id = $1 AND id = sqlc.arg(grant_id) RETURNING app_id;

-- name: DeleteOAuthGrant :execrows
DELETE FROM oauth_grants WHERE app_id = $1 AND user_id = $2;

-- name: InsertOAuthCode :exec
INSERT INTO oauth_codes (code_hash, app_id, user_id, redirect_uri, redirect_given, scopes, code_challenge, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetOAuthCodeForUpdate :one
SELECT * FROM oauth_codes WHERE code_hash = $1 FOR UPDATE;

-- name: UseOAuthCode :exec
UPDATE oauth_codes SET used_at = now() WHERE code_hash = $1;

-- name: DeleteExpiredOAuthCodes :execrows
DELETE FROM oauth_codes WHERE expires_at < now() - interval '1 hour';

-- name: InsertGrantToken :exec
INSERT INTO tokens (id, user_id, name, token_hash, hint, scopes, expires_at, grant_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListGrantTokenIDs :many
-- Tokens of a grant, newest first.
SELECT id FROM tokens WHERE grant_id = $1 ORDER BY created_at DESC;

-- name: DeleteToken :execrows
DELETE FROM tokens WHERE id = $1;

-- name: GetAppTokenByHash :one
-- A token issued to the app with client id app_id, with its owner.
SELECT t.id, t.user_id, t.scopes, t.expires_at, t.created_at, t.grant_id, t.hint, g.scopes AS grant_scopes,
       g.updated_at AS grant_updated_at, n.name AS username
FROM tokens t
JOIN oauth_grants g ON g.id = t.grant_id
JOIN namespaces n ON n.id = t.user_id
WHERE t.token_hash = $1 AND g.app_id = sqlc.arg(app_id);

-- name: LockOAuthApp :exec
-- Serializes secret changes of one app inside a transaction.
SELECT pg_advisory_xact_lock(hashtextextended('erepo.oauth_app:' || sqlc.arg(app_id)::uuid::text, 0));
