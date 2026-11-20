-- +goose Up
CREATE TABLE ervisiocode_shares (
    id                 uuid PRIMARY KEY,
    owner_id           uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_id          uuid NOT NULL REFERENCES tokens (id) ON DELETE CASCADE,
    session_id         text NOT NULL CHECK (session_id ~ '^[A-Za-z0-9_-]{1,128}$'),
    recipient_id       uuid REFERENCES users (id) ON DELETE CASCADE,
    org_id             uuid REFERENCES orgs (id) ON DELETE CASCADE,
    role               text NOT NULL CHECK (role IN ('reader', 'proposer', 'direct')),
    full_history       boolean NOT NULL,
    after_message_id   text CHECK (after_message_id ~ '^[A-Za-z0-9_-]{1,128}$'),
    from_message_id    text CHECK (from_message_id ~ '^[A-Za-z0-9_-]{1,128}$'),
    host_cursor        text CHECK (host_cursor ~ '^[A-Za-z0-9_-]{1,128}$'),
    invite_token_hash  bytea UNIQUE CHECK (octet_length(invite_token_hash) = 32),
    invite_expires_at  timestamptz,
    accepted_at        timestamptz,
    revoked_at         timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CHECK (role <> 'direct' OR full_history),
    CHECK (num_nonnulls(after_message_id, from_message_id, host_cursor) = CASE WHEN full_history THEN 0 ELSE 1 END),
    CHECK (
        (org_id IS NOT NULL AND recipient_id IS NULL AND invite_expires_at IS NULL
            AND invite_token_hash IS NULL AND accepted_at IS NULL)
        OR
        (org_id IS NULL AND recipient_id IS NOT NULL AND recipient_id <> owner_id
            AND invite_expires_at IS NOT NULL
            AND (invite_token_hash IS NOT NULL) = (accepted_at IS NULL AND revoked_at IS NULL))
    )
);

CREATE INDEX ervisiocode_shares_owner ON ervisiocode_shares (owner_id, id DESC);
CREATE INDEX ervisiocode_shares_recipient ON ervisiocode_shares (recipient_id, id DESC);
CREATE INDEX ervisiocode_shares_org ON ervisiocode_shares (org_id, id DESC);
CREATE INDEX ervisiocode_shares_device ON ervisiocode_shares (device_id);

-- A share never gives access to a revoked or expired device, even if its host is offline.
CREATE VIEW ervisiocode_active_shares AS
SELECT s.id, s.owner_id, s.recipient_id, s.org_id
FROM ervisiocode_shares s
JOIN tokens t ON t.id = s.device_id AND t.user_id = s.owner_id
JOIN users owner ON owner.id = s.owner_id
WHERE s.revoked_at IS NULL AND (s.accepted_at IS NOT NULL OR s.org_id IS NOT NULL)
  AND t.is_ervisiocode_device AND t.expires_at > statement_timestamp()
  AND t.remote_session_metadata @> jsonb_build_array(jsonb_build_object('sessionId', s.session_id))
  AND owner.suspended_at IS NULL
  AND (s.org_id IS NULL OR EXISTS (
      SELECT 1 FROM org_members m
      WHERE m.org_id = s.org_id AND m.user_id = s.owner_id AND m.role IN ('owner', 'admin')
  ));

CREATE VIEW ervisiocode_share_recipients AS
SELECT s.id AS share_id, u.id AS account_id
FROM ervisiocode_active_shares s
JOIN users u ON u.id = s.recipient_id AND u.suspended_at IS NULL
UNION ALL
SELECT s.id, m.user_id
FROM ervisiocode_active_shares s
JOIN org_members m ON m.org_id = s.org_id AND m.user_id <> s.owner_id
JOIN users u ON u.id = m.user_id AND u.suspended_at IS NULL;

CREATE TABLE ervisiocode_proposals (
    id            uuid PRIMARY KEY,
    share_id      uuid NOT NULL REFERENCES ervisiocode_shares (id) ON DELETE CASCADE,
    author_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body          text NOT NULL CHECK (octet_length(body) BETWEEN 1 AND 16384),
    status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL DEFAULT (now() + interval '168 hours'),
    reviewed_at   timestamptz,
    CHECK ((status = 'pending') = (reviewed_at IS NULL))
);

CREATE INDEX ervisiocode_proposals_share ON ervisiocode_proposals (share_id, id DESC);
CREATE INDEX ervisiocode_proposals_author ON ervisiocode_proposals (author_id);
CREATE INDEX ervisiocode_proposals_expires ON ervisiocode_proposals (expires_at);
