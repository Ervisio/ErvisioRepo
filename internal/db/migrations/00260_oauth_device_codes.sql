-- +goose Up
-- Pending OAuth 2.0 device authorizations (RFC 8628, GitHub compatible).
-- The device code is stored as its sha256; the user code is stored
-- normalized (8 letters, no dash). client_id is not a foreign key: the
-- first-party clients are built in, registered OAuth apps come later.

CREATE TABLE oauth_device_codes (
    id               uuid        PRIMARY KEY,
    device_code_hash bytea       NOT NULL UNIQUE,
    user_code        text        NOT NULL UNIQUE,
    client_id        text        NOT NULL,
    scopes           text[]      NOT NULL DEFAULT '{}',
    device_name      text        NOT NULL DEFAULT '',
    status           text        NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'approved', 'denied', 'consumed')),
    user_id          uuid        REFERENCES users (id) ON DELETE CASCADE,
    token_id         uuid        REFERENCES tokens (id) ON DELETE SET NULL,
    request_ip       text        NOT NULL DEFAULT '',
    interval_seconds integer     NOT NULL DEFAULT 5 CHECK (interval_seconds > 0),
    last_poll_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL
);

CREATE INDEX oauth_device_codes_expires ON oauth_device_codes (expires_at);

-- +goose Down
DROP TABLE oauth_device_codes;
