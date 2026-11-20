-- +goose Up
ALTER TABLE ervisiocode_shares
    ADD COLUMN text_collaboration boolean NOT NULL DEFAULT false,
    ADD COLUMN organization_consent boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT ervisiocode_shares_text_collaboration CHECK (
        (role = 'reader' OR revoked_at IS NOT NULL OR text_collaboration)
        AND (NOT text_collaboration OR (role = 'direct' AND full_history))
        AND organization_consent = (text_collaboration AND org_id IS NOT NULL)
    ),
    DROP CONSTRAINT ervisiocode_shares_reader_only;

CREATE OR REPLACE VIEW ervisiocode_pending_share_invites AS
SELECT s.id, s.recipient_id
FROM ervisiocode_shares s
JOIN users recipient ON recipient.id = s.recipient_id AND recipient.suspended_at IS NULL
JOIN tokens t ON t.id = s.device_id AND t.user_id = s.owner_id
JOIN users owner ON owner.id = s.owner_id AND owner.suspended_at IS NULL
WHERE (s.role = 'reader' OR s.text_collaboration) AND s.accepted_at IS NULL AND s.revoked_at IS NULL
  AND s.invite_expires_at > statement_timestamp()
  AND t.is_ervisiocode_device AND t.expires_at > statement_timestamp()
  AND t.remote_session_metadata @> jsonb_build_array(jsonb_build_object('sessionId', s.session_id));
