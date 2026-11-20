-- name: CreateErvisioCodeShare :one
WITH share AS (
INSERT INTO ervisiocode_shares (
    id, owner_id, device_id, session_id, recipient_id, org_id, role, full_history,
    after_message_id, from_message_id, host_cursor, invite_token_hash, invite_expires_at
)
SELECT sqlc.arg(id), t.user_id, t.id, sqlc.arg(session_id)::text, sqlc.narg(recipient_id)::uuid,
       sqlc.narg(org_id)::uuid, sqlc.arg(role), sqlc.arg(full_history)::boolean,
       sqlc.narg(after_message_id), sqlc.narg(from_message_id), sqlc.narg(host_cursor),
       sqlc.narg(invite_token_hash), sqlc.narg(invite_expires_at)
FROM tokens t
JOIN users owner ON owner.id = t.user_id AND owner.suspended_at IS NULL
WHERE t.id = sqlc.arg(device_id) AND t.user_id = sqlc.arg(owner_id)
  AND t.is_ervisiocode_device AND t.expires_at > statement_timestamp()
  AND t.remote_session_metadata @> jsonb_build_array(jsonb_build_object('sessionId', sqlc.arg(session_id)::text))
  AND (sqlc.narg(recipient_id)::uuid IS NULL OR EXISTS (
      SELECT 1 FROM users u WHERE u.id = sqlc.narg(recipient_id)::uuid AND u.suspended_at IS NULL
  ))
  AND (sqlc.narg(org_id)::uuid IS NULL OR EXISTS (
      SELECT 1 FROM org_members m
      WHERE m.org_id = sqlc.narg(org_id)::uuid AND m.user_id = t.user_id AND m.role IN ('owner', 'admin')
  ))
RETURNING *
)
SELECT s.*, COALESCE(d.session_title, '')::text AS session_title,
       COALESCE(d.owner_username, '')::text AS owner_username,
       COALESCE(d.owner_display_name, '')::text AS owner_display_name,
       COALESCE(d.owner_avatar_url, '')::text AS owner_avatar_url
FROM share s
LEFT JOIN ervisiocode_share_session_details d USING (owner_id, device_id, session_id);

-- name: AcceptErvisioCodeShare :one
WITH share AS (
UPDATE ervisiocode_shares s
SET accepted_at = clock_timestamp(), invite_token_hash = NULL
FROM tokens t, users owner, users recipient
WHERE s.recipient_id = sqlc.arg(recipient_id)
  AND (s.id = sqlc.narg(share_id)::uuid OR s.invite_token_hash = sqlc.narg(token_hash)::bytea)
  AND s.role = 'reader' AND s.accepted_at IS NULL AND s.revoked_at IS NULL AND s.invite_expires_at > clock_timestamp()
  AND owner.id = s.owner_id AND owner.suspended_at IS NULL
  AND recipient.id = s.recipient_id AND recipient.suspended_at IS NULL
  AND t.id = s.device_id AND t.user_id = s.owner_id
  AND t.is_ervisiocode_device AND t.expires_at > clock_timestamp()
  AND t.remote_session_metadata @> jsonb_build_array(jsonb_build_object('sessionId', s.session_id))
RETURNING s.*
)
SELECT s.*, COALESCE(d.session_title, '')::text AS session_title,
       COALESCE(d.owner_username, '')::text AS owner_username,
       COALESCE(d.owner_display_name, '')::text AS owner_display_name,
       COALESCE(d.owner_avatar_url, '')::text AS owner_avatar_url
FROM share s
LEFT JOIN ervisiocode_share_session_details d USING (owner_id, device_id, session_id);

-- name: RejectErvisioCodeShare :execrows
UPDATE ervisiocode_shares s
SET revoked_at = statement_timestamp(), invite_token_hash = NULL
FROM users recipient
WHERE s.id = sqlc.arg(id) AND s.recipient_id = recipient.id AND s.org_id IS NULL
  AND recipient.id = sqlc.arg(recipient_id) AND recipient.suspended_at IS NULL
  AND s.accepted_at IS NULL AND s.revoked_at IS NULL AND s.invite_expires_at > clock_timestamp();

-- name: RevokeErvisioCodeShare :execrows
UPDATE ervisiocode_shares s
SET revoked_at = statement_timestamp(), invite_token_hash = NULL
FROM users actor
WHERE s.id = sqlc.arg(id) AND s.revoked_at IS NULL
  AND actor.id = sqlc.arg(actor_id) AND actor.suspended_at IS NULL
  AND (s.owner_id = actor.id OR EXISTS (
      SELECT 1 FROM org_members m
      WHERE m.org_id = s.org_id AND m.user_id = actor.id AND m.role IN ('owner', 'admin')
  ));

-- name: GetActiveErvisioCodeShare :one
SELECT s.owner_id, s.device_id, s.session_id, s.full_history,
       s.after_message_id, s.from_message_id, s.host_cursor
FROM ervisiocode_shares s
JOIN ervisiocode_active_shares a ON a.id = s.id
WHERE s.id = sqlc.arg(id) AND s.role = 'reader' AND (
    s.owner_id = sqlc.arg(actor_id)::uuid OR EXISTS (
        SELECT 1 FROM ervisiocode_share_recipients r WHERE r.share_id = s.id AND r.account_id = sqlc.arg(actor_id)::uuid
    )
);

-- name: ListErvisioCodeShares :many
SELECT s.*, COALESCE(d.session_title, '')::text AS session_title,
       COALESCE(d.owner_username, '')::text AS owner_username,
       COALESCE(d.owner_display_name, '')::text AS owner_display_name,
       COALESCE(d.owner_avatar_url, '')::text AS owner_avatar_url
FROM ervisiocode_shares s
JOIN users actor ON actor.id = sqlc.arg(actor_id) AND actor.suspended_at IS NULL
LEFT JOIN ervisiocode_share_session_details d USING (owner_id, device_id, session_id)
WHERE (sqlc.narg(before_id)::uuid IS NULL OR s.id < sqlc.narg(before_id)::uuid)
  AND (s.owner_id = actor.id OR (s.role = 'reader' AND EXISTS (
      SELECT 1 FROM ervisiocode_share_recipients r WHERE r.share_id = s.id AND r.account_id = actor.id
  )) OR EXISTS (
      SELECT 1 FROM ervisiocode_pending_share_invites p WHERE p.id = s.id AND p.recipient_id = actor.id
  ))
ORDER BY s.id DESC
LIMIT sqlc.arg(page_size);

-- name: CountErvisioCodePendingInvitations :one
SELECT count(*) FROM ervisiocode_pending_share_invites WHERE recipient_id = sqlc.arg(recipient_id)::uuid;

-- name: DeleteExpiredErvisioCodeProposals :execrows
DELETE FROM ervisiocode_proposals WHERE expires_at <= statement_timestamp();

-- name: GetErvisioCodeDirectoryRole :one
SELECT role FROM users WHERE id = $1 AND suspended_at IS NULL;

-- name: SearchErvisioCodeGrantOrgs :many
SELECT o.id, n.name, o.display_name, o.avatar_url
FROM org_members m
JOIN orgs o ON o.id = m.org_id
JOIN namespaces n ON n.id = o.id
JOIN users actor ON actor.id = m.user_id AND actor.suspended_at IS NULL
WHERE m.user_id = sqlc.arg(account_id) AND m.role IN ('owner', 'admin')
  AND (starts_with(n.name_lower, sqlc.arg(prefix)::text) OR starts_with(lower(o.display_name), sqlc.arg(prefix)::text))
ORDER BY (n.name_lower = sqlc.arg(prefix)::text) DESC, starts_with(n.name_lower, sqlc.arg(prefix)::text) DESC, n.name_lower
LIMIT sqlc.arg(page_size);
