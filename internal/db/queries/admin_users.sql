-- name: AdminListUsers :many
-- Users for the instance admin, newest first. search matches the username,
-- display name or any email address (case-insensitive substring); role and
-- suspended filter when set. total is the count before paging.
SELECT u.id, n.name AS username, u.display_name, u.role, u.suspended_at, u.created_at,
       COALESCE(e.address, '')::text AS email,
       (EXISTS (SELECT 1 FROM mfa_totp t WHERE t.user_id = u.id AND t.confirmed_at IS NOT NULL)
        OR EXISTS (SELECT 1 FROM webauthn_credentials w WHERE w.user_id = u.id))::boolean AS has_2fa,
       (SELECT max(s.last_seen_at) FROM sessions s WHERE s.user_id = u.id)::timestamptz AS last_activity,
       count(*) OVER ()::bigint AS total
FROM users u
JOIN namespaces n ON n.id = u.id
LEFT JOIN emails e ON e.user_id = u.id AND e.is_primary
WHERE (sqlc.arg(search)::text = ''
       OR n.name_lower LIKE '%' || sqlc.arg(search) || '%'
       OR lower(u.display_name) LIKE '%' || sqlc.arg(search) || '%'
       OR EXISTS (SELECT 1 FROM emails x WHERE x.user_id = u.id AND lower(x.address) LIKE '%' || sqlc.arg(search) || '%'))
  AND (sqlc.arg(role)::text = '' OR u.role = sqlc.arg(role))
  AND (sqlc.narg(suspended)::boolean IS NULL OR (u.suspended_at IS NOT NULL) = sqlc.narg(suspended))
ORDER BY u.created_at DESC, u.id
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: AdminGetUser :one
SELECT u.id, n.name AS username, u.display_name, u.role, u.suspended_at, u.created_at,
       COALESCE(e.address, '')::text AS email,
       (EXISTS (SELECT 1 FROM mfa_totp t WHERE t.user_id = u.id AND t.confirmed_at IS NOT NULL)
        OR EXISTS (SELECT 1 FROM webauthn_credentials w WHERE w.user_id = u.id))::boolean AS has_2fa,
       (SELECT max(s.last_seen_at) FROM sessions s WHERE s.user_id = u.id)::timestamptz AS last_activity
FROM users u
JOIN namespaces n ON n.id = u.id
LEFT JOIN emails e ON e.user_id = u.id AND e.is_primary
WHERE n.name_lower = lower(sqlc.arg(username)::text);

-- name: LockAdminChanges :exec
-- Serializes role and suspension changes, so that two admins cannot demote
-- each other at the same time.
SELECT pg_advisory_xact_lock(hashtext('ervisio.admin_users'));

-- name: CountActiveAdmins :one
SELECT count(*)::bigint FROM users WHERE role = 'admin' AND suspended_at IS NULL;

-- name: DeleteUserTokens :execrows
DELETE FROM tokens WHERE user_id = $1;

-- name: DeleteUserLFSTokens :exec
DELETE FROM lfs_tokens WHERE user_id = $1;

-- name: DeleteUserOneTimeTokens :exec
DELETE FROM one_time_tokens WHERE user_id = $1;

-- name: DeleteUserMFAChallenges :exec
DELETE FROM mfa_challenges WHERE user_id = sqlc.arg(user_id)::uuid;

-- name: DeleteUserWebAuthnCredentials :execrows
DELETE FROM webauthn_credentials WHERE user_id = $1;

-- name: AdminListInvites :many
-- Invites newest first, with the names of their creator and user.
SELECT i.id, i.email, i.expires_at, i.used_at, i.created_at,
       COALESCE(cn.name, '')::text AS created_by, COALESCE(un.name, '')::text AS used_by,
       count(*) OVER ()::bigint AS total
FROM invites i
LEFT JOIN namespaces cn ON cn.id = i.created_by
LEFT JOIN namespaces un ON un.id = i.used_by
ORDER BY i.created_at DESC, i.id
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: DeleteUnusedInvite :one
DELETE FROM invites WHERE id = $1 AND used_at IS NULL RETURNING id, email;
