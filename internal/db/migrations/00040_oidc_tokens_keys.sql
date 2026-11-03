-- +goose Up
-- External identities (OIDC), personal access tokens, SSH and GPG keys
-- (PLAN §8.1, §10.2).

-- An account linked to an OIDC identity. The identity is issuer#subject,
-- never an email address: a matching email never links two accounts.
CREATE TABLE oidc_links (
    id            uuid        PRIMARY KEY,
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider      text        NOT NULL CHECK (provider ~ '^[a-z0-9][a-z0-9_-]{0,62}$'),
    issuer        text        NOT NULL CHECK (length(issuer) BETWEEN 1 AND 2048),
    subject       text        NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
    -- Informational only (shown in the settings), never used for matching.
    email         text        NOT NULL DEFAULT '' CHECK (length(email) <= 254),
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    UNIQUE (issuer, subject),
    -- One identity per provider and account.
    UNIQUE (user_id, provider)
);

-- Pending OIDC authorization requests. The state is also held in a cookie
-- of the browser that started the flow; only its SHA-256 is stored here.
CREATE TABLE oidc_flows (
    state_hash    bytea       PRIMARY KEY CHECK (length(state_hash) = 32),
    provider      text        NOT NULL,
    mode          text        NOT NULL CHECK (mode IN ('login', 'link')),
    nonce         text        NOT NULL,
    pkce_verifier text        NOT NULL,
    -- For mode = 'link': the signed-in user and session that asked for it.
    user_id       uuid        REFERENCES users (id) ON DELETE CASCADE,
    session_id    uuid        REFERENCES sessions (id) ON DELETE CASCADE,
    return_to     text        NOT NULL DEFAULT '/',
    expires_at    timestamptz NOT NULL,
    CHECK ((mode = 'link' AND user_id IS NOT NULL AND session_id IS NOT NULL)
        OR (mode = 'login' AND user_id IS NULL AND session_id IS NULL))
);

CREATE INDEX oidc_flows_expires ON oidc_flows (expires_at);

-- Personal access tokens. The token is shown once; only its SHA-256 is kept.
CREATE TABLE tokens (
    id           uuid        PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text        NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    token_hash   bytea       NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    -- First characters of the token, to recognize it in the list.
    hint         text        NOT NULL,
    scopes       text[]      NOT NULL DEFAULT '{}',
    expires_at   timestamptz,
    last_used_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);

CREATE INDEX tokens_user ON tokens (user_id);

-- SSH public keys of users. A key authenticates exactly one account.
CREATE TABLE ssh_keys (
    id           uuid        PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title        text        NOT NULL CHECK (length(title) BETWEEN 1 AND 100),
    key_type     text        NOT NULL,
    -- OpenSSH SHA256 fingerprint without the "SHA256:" prefix.
    fingerprint  text        NOT NULL UNIQUE,
    public_key   text        NOT NULL, -- authorized_keys form, without comment
    bits         integer     NOT NULL DEFAULT 0,
    last_used_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ssh_keys_user ON ssh_keys (user_id);

-- GPG public keys, used to verify commit signatures from Phase 4.
CREATE TABLE gpg_keys (
    id          uuid        PRIMARY KEY,
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Upper-case hex: 16-digit key id and 40/64-digit fingerprint.
    key_id      text        NOT NULL,
    fingerprint text        NOT NULL UNIQUE,
    subkey_ids  text[]      NOT NULL DEFAULT '{}',
    emails      text[]      NOT NULL DEFAULT '{}',
    armored     text        NOT NULL CHECK (length(armored) <= 65536),
    expires_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX gpg_keys_user ON gpg_keys (user_id);
CREATE INDEX gpg_keys_key_id ON gpg_keys (key_id);

-- +goose Down
DROP TABLE gpg_keys;
DROP TABLE ssh_keys;
DROP TABLE tokens;
DROP TABLE oidc_flows;
DROP TABLE oidc_links;
