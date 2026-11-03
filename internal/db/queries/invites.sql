-- Organization invitations (instance invitations are in identity.sql).

-- name: InsertOrgInvite :exec
INSERT INTO org_invites (id, org_id, email, role, team_ids, token_hash, invited_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: DeleteStaleOrgInvite :execrows
-- Removes an expired pending invitation for the address, so a new one can
-- take its place.
DELETE FROM org_invites
WHERE org_id = $1 AND email_lower = lower(sqlc.arg(email)::text) AND accepted_at IS NULL AND expires_at <= sqlc.arg(now)::timestamptz;

-- name: GetOrgInvite :one
SELECT i.id, i.org_id, n.name AS org_name, i.email, i.role, i.team_ids, i.invited_by,
       COALESCE(inv.name, '')::text AS invited_by_name, COALESCE(u.locale, 'en')::text AS inviter_locale,
       i.expires_at, i.accepted_at, i.created_at
FROM org_invites i
JOIN namespaces n ON n.id = i.org_id
LEFT JOIN namespaces inv ON inv.id = i.invited_by
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.id = $1;

-- name: ListOrgInvites :many
-- Pending invitations, expired ones included (the UI shows them as such).
SELECT i.id, i.email, i.role, i.team_ids, i.invited_by, inv.name AS invited_by_name, i.expires_at, i.created_at
FROM org_invites i
LEFT JOIN namespaces inv ON inv.id = i.invited_by
WHERE i.org_id = $1 AND i.accepted_at IS NULL
ORDER BY i.created_at DESC;

-- name: SetOrgInviteToken :execrows
-- Replaces the token of a pending invitation (sending again invalidates the
-- previous link) and extends its expiry.
UPDATE org_invites SET token_hash = $2, expires_at = $3
WHERE id = $1 AND accepted_at IS NULL;

-- name: GetOrgInviteByToken :one
SELECT i.id, i.org_id, n.name AS org_name, i.email, i.role, i.team_ids, i.expires_at
FROM org_invites i
JOIN namespaces n ON n.id = i.org_id
WHERE i.token_hash = $1 AND i.accepted_at IS NULL
FOR UPDATE OF i;

-- name: MarkOrgInviteAccepted :execrows
UPDATE org_invites SET accepted_at = now(), accepted_by = $2, token_hash = NULL
WHERE id = $1 AND accepted_at IS NULL;

-- name: DeleteOrgInvite :execrows
DELETE FROM org_invites WHERE id = $1 AND org_id = $2 AND accepted_at IS NULL;

-- name: DeleteExpiredOrgInvites :execrows
-- Forgets invitations expired for more than the given age.
DELETE FROM org_invites WHERE accepted_at IS NULL AND expires_at < sqlc.arg(before)::timestamptz;

-- name: UserHasEmail :one
-- Whether the address belongs to the user, and whether it is verified.
SELECT EXISTS (SELECT 1 FROM emails e WHERE e.user_id = sqlc.arg(user_id)::uuid
               AND e.address_lower = lower(sqlc.arg(address)::text)) AS owned,
       EXISTS (SELECT 1 FROM emails e WHERE e.user_id = sqlc.arg(user_id)::uuid
               AND e.address_lower = lower(sqlc.arg(address)::text) AND e.verified_at IS NOT NULL) AS verified;
