-- +goose Up
-- Second factors: TOTP, WebAuthn credentials (passkeys), recovery codes,
-- the short-lived challenges of the two-step login and the organization
-- 2FA policy (PLAN §8.1).

-- One TOTP secret per account. secret starts with a format byte (0 plain,
-- 1 AES-GCM sealed with a key derived from the master key). confirmed_at is
-- NULL while the enrollment waits for a first valid code. last_step is the
-- highest accepted time step: a code is accepted only for a later step, so
-- a code cannot be replayed within its window.
CREATE TABLE mfa_totp (
    user_id      uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    secret       bytea       NOT NULL CHECK (length(secret) BETWEEN 17 AND 128),
    confirmed_at timestamptz,
    last_step    bigint      NOT NULL DEFAULT 0 CHECK (last_step >= 0),
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- Single-use recovery codes, stored as SHA-256.
CREATE TABLE mfa_recovery_codes (
    id         uuid        PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  bytea       NOT NULL CHECK (length(code_hash) = 32),
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, code_hash)
);

-- The random WebAuthn user handle of an account (never the account id).
CREATE TABLE webauthn_handles (
    user_id uuid  PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    handle  bytea NOT NULL UNIQUE CHECK (length(handle) = 32)
);

-- Registered WebAuthn credentials; data is the go-webauthn Credential.
CREATE TABLE webauthn_credentials (
    id            uuid        PRIMARY KEY,
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id bytea       NOT NULL UNIQUE CHECK (length(credential_id) BETWEEN 1 AND 1023),
    name          text        NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    data          jsonb       NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz
);

CREATE INDEX webauthn_credentials_user ON webauthn_credentials (user_id);

-- Short-lived challenges: a login waiting for its second factor
-- (mfa_pending), a passkey registration and a passwordless passkey login.
-- Only the SHA-256 of the token is stored.
CREATE TABLE mfa_challenges (
    id         uuid        PRIMARY KEY,
    token_hash bytea       NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    kind       text        NOT NULL CHECK (kind IN ('login', 'webauthn_register', 'passkey_login')),
    user_id    uuid        REFERENCES users (id) ON DELETE CASCADE,
    enroll     boolean     NOT NULL DEFAULT false,
    webauthn   jsonb,
    attempts   integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    ip         text        NOT NULL DEFAULT '',
    user_agent text        NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((kind = 'passkey_login') = (user_id IS NULL))
);

CREATE INDEX mfa_challenges_expires ON mfa_challenges (expires_at);

-- Organizations may require their members to use a second factor.
ALTER TABLE orgs ADD COLUMN require_2fa boolean NOT NULL DEFAULT false;
