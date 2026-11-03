-- Profile, password, sessions and email addresses of the signed-in user.

-- name: UpdateUserProfile :exec
UPDATE users SET display_name = $2, avatar_url = $3, locale = $4, updated_at = now() WHERE id = $1;

-- name: ListUserEmails :many
SELECT id, address, is_primary, verified_at, created_at
FROM emails WHERE user_id = $1 ORDER BY is_primary DESC, created_at, id;

-- name: GetUserEmail :one
SELECT id, address, is_primary, verified_at, created_at
FROM emails WHERE id = $1 AND user_id = $2;

-- name: CountUserEmails :one
SELECT count(*) FROM emails WHERE user_id = $1;

-- name: ClearPrimaryEmail :exec
UPDATE emails SET is_primary = false WHERE user_id = $1 AND is_primary;

-- name: SetPrimaryEmail :execrows
UPDATE emails SET is_primary = true WHERE id = $1 AND user_id = $2;

-- name: DeleteUserEmail :execrows
DELETE FROM emails WHERE id = $1 AND user_id = $2 AND NOT is_primary;

-- name: DeleteUserSessionByID :execrows
DELETE FROM sessions WHERE id = $1 AND user_id = $2;
