-- name: CreateErvisioCodeShare :one
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
RETURNING *;

-- name: AcceptErvisioCodeShare :one
UPDATE ervisiocode_shares s
SET accepted_at = clock_timestamp(), invite_token_hash = NULL
FROM tokens t, users owner, users recipient
WHERE s.recipient_id = sqlc.arg(recipient_id)
  AND (s.id = sqlc.narg(share_id)::uuid OR s.invite_token_hash = sqlc.narg(token_hash)::bytea)
  AND s.accepted_at IS NULL AND s.revoked_at IS NULL AND s.invite_expires_at > clock_timestamp()
  AND owner.id = s.owner_id AND owner.suspended_at IS NULL
  AND recipient.id = s.recipient_id AND recipient.suspended_at IS NULL
  AND t.id = s.device_id AND t.user_id = s.owner_id
  AND t.is_ervisiocode_device AND t.expires_at > clock_timestamp()
  AND t.remote_session_metadata @> jsonb_build_array(jsonb_build_object('sessionId', s.session_id))
RETURNING s.*;

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
SELECT s.*
FROM ervisiocode_shares s
JOIN ervisiocode_active_shares a ON a.id = s.id
WHERE s.id = sqlc.arg(id) AND (
    s.owner_id = sqlc.arg(actor_id)::uuid OR EXISTS (
        SELECT 1 FROM ervisiocode_share_recipients r WHERE r.share_id = s.id AND r.account_id = sqlc.arg(actor_id)::uuid
    )
);

-- name: ListErvisioCodeShares :many
SELECT s.*
FROM ervisiocode_shares s
JOIN users actor ON actor.id = sqlc.arg(actor_id) AND actor.suspended_at IS NULL
WHERE (sqlc.narg(before_id)::uuid IS NULL OR s.id < sqlc.narg(before_id)::uuid)
  AND (s.owner_id = actor.id OR EXISTS (
      SELECT 1 FROM ervisiocode_share_recipients r WHERE r.share_id = s.id AND r.account_id = actor.id
  ) OR (
      s.recipient_id = actor.id AND s.accepted_at IS NULL AND s.revoked_at IS NULL
      AND s.invite_expires_at > statement_timestamp()
      AND EXISTS (
          SELECT 1 FROM tokens t JOIN users owner ON owner.id = t.user_id
          WHERE t.id = s.device_id AND t.user_id = s.owner_id AND owner.suspended_at IS NULL
          AND t.is_ervisiocode_device AND t.expires_at > statement_timestamp()
          AND t.remote_session_metadata @> jsonb_build_array(jsonb_build_object('sessionId', s.session_id))
      )
  ))
ORDER BY s.id DESC
LIMIT sqlc.arg(page_size);

-- name: CreateErvisioCodeProposal :one
INSERT INTO ervisiocode_proposals (id, share_id, author_id, body)
SELECT sqlc.arg(id), s.id, r.account_id, sqlc.arg(body)
FROM ervisiocode_shares s
JOIN ervisiocode_share_recipients r ON r.share_id = s.id
WHERE s.id = sqlc.arg(share_id) AND r.account_id = sqlc.arg(author_id)
  AND s.revoked_at IS NULL AND s.role IN ('proposer', 'direct')
FOR SHARE OF s
RETURNING *;

-- name: ListErvisioCodeProposals :many
SELECT p.*
FROM ervisiocode_proposals p
JOIN ervisiocode_active_shares s ON s.id = p.share_id
WHERE p.share_id = sqlc.arg(share_id) AND p.expires_at > statement_timestamp()
  AND (sqlc.narg(before_id)::uuid IS NULL OR p.id < sqlc.narg(before_id)::uuid)
  AND (s.owner_id = sqlc.arg(actor_id)::uuid OR (
      p.author_id = sqlc.arg(actor_id)::uuid AND EXISTS (
          SELECT 1 FROM ervisiocode_share_recipients r WHERE r.share_id = s.id AND r.account_id = p.author_id
      )
  ))
ORDER BY p.id DESC
LIMIT sqlc.arg(page_size);

-- name: ReviewErvisioCodeProposal :one
WITH reviewable_share AS MATERIALIZED (
    SELECT s.id
    FROM ervisiocode_shares s
    JOIN ervisiocode_active_shares a ON a.id = s.id
    WHERE s.owner_id = sqlc.arg(owner_id) AND s.id = sqlc.arg(share_id) AND s.revoked_at IS NULL
    FOR SHARE OF s
)
UPDATE ervisiocode_proposals p
SET status = sqlc.arg(status), reviewed_at = statement_timestamp()
FROM reviewable_share s
WHERE p.id = sqlc.arg(id) AND p.share_id = s.id AND p.status = 'pending'
  AND p.expires_at > clock_timestamp()
  AND (sqlc.arg(status) = 'rejected' OR EXISTS (
      SELECT 1 FROM ervisiocode_share_recipients r WHERE r.share_id = s.id AND r.account_id = p.author_id
  ))
RETURNING p.*;

-- name: DeleteExpiredErvisioCodeProposals :execrows
DELETE FROM ervisiocode_proposals WHERE expires_at <= statement_timestamp();

-- name: GetErvisioCodeDirectoryRole :one
SELECT role FROM users WHERE id = $1 AND suspended_at IS NULL;

-- name: SearchErvisioCodeGrantOrgs :many
SELECT o.id, n.name, o.display_name
FROM org_members m
JOIN orgs o ON o.id = m.org_id
JOIN namespaces n ON n.id = o.id
JOIN users actor ON actor.id = m.user_id AND actor.suspended_at IS NULL
WHERE m.user_id = sqlc.arg(account_id) AND m.role IN ('owner', 'admin')
  AND (starts_with(n.name_lower, sqlc.arg(prefix)::text) OR starts_with(lower(o.display_name), sqlc.arg(prefix)::text))
ORDER BY (n.name_lower = sqlc.arg(prefix)::text) DESC, starts_with(n.name_lower, sqlc.arg(prefix)::text) DESC, n.name_lower
LIMIT sqlc.arg(page_size);
