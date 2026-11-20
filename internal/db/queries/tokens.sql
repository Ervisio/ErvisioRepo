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
-- Compare against the same database clock used to record heartbeats.
SELECT id, name, expires_at, last_used_at, created_at, remote_last_seen_at,
       (remote_active AND COALESCE(remote_last_seen_at > statement_timestamp() - interval '45 seconds', false))::boolean AS remote_online
FROM tokens
WHERE user_id = $1 AND is_ervisiocode_device AND expires_at > now()
ORDER BY created_at DESC, id DESC;

-- name: GetErvisioCodeDevice :one
SELECT id, name, expires_at, last_used_at, created_at FROM tokens
WHERE user_id = $1 AND id = sqlc.arg(token_id) AND is_ervisiocode_device AND expires_at > now();

-- name: DeleteErvisioCodeDevice :execrows
DELETE FROM tokens WHERE user_id = $1 AND id = sqlc.arg(token_id) AND is_ervisiocode_device;

-- name: SetErvisioCodeRemotePresence :execrows
UPDATE tokens
SET remote_active = sqlc.arg(active)::boolean,
    remote_last_seen_at = CASE WHEN sqlc.arg(active)::boolean THEN clock_timestamp() ELSE remote_last_seen_at END
WHERE tokens.user_id = $1 AND tokens.id = sqlc.arg(token_id)
  AND tokens.is_ervisiocode_device AND tokens.expires_at > clock_timestamp()
  AND EXISTS (SELECT 1 FROM users WHERE users.id = tokens.user_id AND users.suspended_at IS NULL);

-- name: SetErvisioCodeHubSession :execrows
UPDATE tokens t
SET hub_session_hash = sqlc.arg(session_hash),
    hub_session_expires_at = LEAST(t.expires_at, now() + make_interval(secs => sqlc.arg(ttl_seconds)::integer))
WHERE t.id = sqlc.arg(token_id) AND t.user_id = sqlc.arg(user_id)
  AND t.is_ervisiocode_device AND t.expires_at > now()
  AND EXISTS (SELECT 1 FROM users u WHERE u.id = t.user_id AND u.suspended_at IS NULL);

-- name: GetErvisioCodeHubSession :one
SELECT n.name AS username
FROM tokens t
JOIN users u ON u.id = t.user_id
JOIN namespaces n ON n.id = u.id
WHERE t.hub_session_hash = sqlc.arg(session_hash)
  AND t.user_id = sqlc.arg(user_id) AND t.is_ervisiocode_device
  AND t.scopes = ARRAY['ervisiocode:device']::text[]
  AND t.hub_session_expires_at > now() AND t.expires_at > now()
  AND u.suspended_at IS NULL;

-- name: GetErvisioCodeRemoteMetadata :one
SELECT remote_session_metadata FROM tokens t
WHERE t.id = sqlc.arg(token_id) AND t.user_id = sqlc.arg(user_id)
  AND t.is_ervisiocode_device AND t.expires_at > now()
  AND EXISTS (SELECT 1 FROM users u WHERE u.id = t.user_id AND u.suspended_at IS NULL);

-- name: ListErvisioCodeRemoteMetadata :many
SELECT t.id, t.remote_session_metadata FROM tokens t
WHERE t.user_id = sqlc.arg(user_id) AND t.is_ervisiocode_device AND t.expires_at > now()
  AND EXISTS (SELECT 1 FROM users u WHERE u.id = t.user_id AND u.suspended_at IS NULL);

-- name: SetErvisioCodeRemoteMetadata :exec
UPDATE tokens t SET remote_session_metadata = sqlc.arg(metadata)::jsonb
WHERE t.id = sqlc.arg(token_id) AND t.user_id = sqlc.arg(user_id)
  AND t.is_ervisiocode_device AND t.expires_at > now()
  AND t.remote_session_metadata IS DISTINCT FROM sqlc.arg(metadata)::jsonb
  AND EXISTS (SELECT 1 FROM users u WHERE u.id = t.user_id AND u.suspended_at IS NULL);
