-- +goose Up
-- Keep historical grants for audit, but invalidate their pending invitation tokens.
UPDATE ervisiocode_shares
SET revoked_at = statement_timestamp(), invite_token_hash = NULL
WHERE role IN ('proposer', 'direct') AND revoked_at IS NULL;

ALTER TABLE ervisiocode_shares ADD CONSTRAINT ervisiocode_shares_reader_only
    CHECK (role = 'reader' OR revoked_at IS NOT NULL);

CREATE VIEW ervisiocode_share_session_details AS
SELECT t.user_id AS owner_id, t.id AS device_id, session.value->>'sessionId' AS session_id,
       session.value->>'title' AS session_title,
       n.name AS owner_username, u.display_name AS owner_display_name, u.avatar_url AS owner_avatar_url
FROM tokens t
JOIN users u ON u.id = t.user_id
JOIN namespaces n ON n.id = u.id
CROSS JOIN LATERAL jsonb_array_elements(t.remote_session_metadata) AS session(value)
WHERE t.is_ervisiocode_device AND t.expires_at > statement_timestamp() AND u.suspended_at IS NULL;

CREATE VIEW ervisiocode_pending_share_invites AS
SELECT s.id, s.recipient_id
FROM ervisiocode_shares s
JOIN users recipient ON recipient.id = s.recipient_id AND recipient.suspended_at IS NULL
JOIN tokens t ON t.id = s.device_id AND t.user_id = s.owner_id
JOIN users owner ON owner.id = s.owner_id AND owner.suspended_at IS NULL
WHERE s.role = 'reader' AND s.accepted_at IS NULL AND s.revoked_at IS NULL
  AND s.invite_expires_at > statement_timestamp()
  AND t.is_ervisiocode_device AND t.expires_at > statement_timestamp()
  AND t.remote_session_metadata @> jsonb_build_array(jsonb_build_object('sessionId', s.session_id));
