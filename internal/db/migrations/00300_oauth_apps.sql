-- +goose Up
-- OAuth apps (APPS-PLAN A1). An app is owned by a user or an organization
-- namespace; the built-in first-party clients (eg, gh) are system rows
-- without an owner. Client secrets, authorization codes and device codes
-- are stored as their sha256. A grant records that a user authorized an
-- app; the tokens issued under it carry grant_id and are listed among the
-- authorized applications, not the personal access tokens.

CREATE TABLE oauth_apps (
    id            uuid        PRIMARY KEY,
    client_id     text        NOT NULL UNIQUE CHECK (length(client_id) BETWEEN 1 AND 100),
    owner_id      uuid        REFERENCES namespaces (id) ON DELETE CASCADE,
    is_system     boolean     NOT NULL DEFAULT false,
    name          text        NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    -- Names the tokens of the built-in clients ("eg on <host> (device)").
    short_name    text        NOT NULL DEFAULT '',
    description   text        NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
    homepage_url  text        NOT NULL DEFAULT '',
    logo_url      text        NOT NULL DEFAULT '',
    callback_urls text[]      NOT NULL DEFAULT '{}',
    device_flow   boolean     NOT NULL DEFAULT false,
    created_by    uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CHECK (is_system OR owner_id IS NOT NULL)
);

CREATE INDEX oauth_apps_owner ON oauth_apps (owner_id);

INSERT INTO oauth_apps (id, client_id, is_system, name, short_name, device_flow) VALUES
    ('00000000-0000-7000-8000-00000000a001', 'Ov23liErvisioEgCli01', true, 'eg (ErvisioRepo CLI)', 'eg', true),
    ('00000000-0000-7000-8000-00000000a002', '178c6fc778ccc68e1d6a', true, 'GitHub CLI', 'gh', true);

CREATE TABLE oauth_app_secrets (
    id           uuid        PRIMARY KEY,
    app_id       uuid        NOT NULL REFERENCES oauth_apps (id) ON DELETE CASCADE,
    secret_hash  bytea       NOT NULL UNIQUE CHECK (length(secret_hash) = 32),
    hint         text        NOT NULL,
    created_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);

CREATE INDEX oauth_app_secrets_app ON oauth_app_secrets (app_id);

CREATE TABLE oauth_grants (
    id         uuid        PRIMARY KEY,
    app_id     uuid        NOT NULL REFERENCES oauth_apps (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- GitHub scopes authorized so far (the union of every approval).
    scopes     text[]      NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (app_id, user_id)
);

CREATE INDEX oauth_grants_user ON oauth_grants (user_id);

CREATE TABLE oauth_codes (
    code_hash      bytea       PRIMARY KEY CHECK (length(code_hash) = 32),
    app_id         uuid        NOT NULL REFERENCES oauth_apps (id) ON DELETE CASCADE,
    user_id        uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    redirect_uri   text        NOT NULL,
    -- Whether the request named redirect_uri: then the token request must
    -- repeat it.
    redirect_given boolean     NOT NULL DEFAULT false,
    scopes         text[]      NOT NULL DEFAULT '{}',
    code_challenge text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,
    used_at        timestamptz
);

CREATE INDEX oauth_codes_expires ON oauth_codes (expires_at);

ALTER TABLE tokens ADD COLUMN grant_id uuid REFERENCES oauth_grants (id) ON DELETE CASCADE;
CREATE INDEX tokens_grant ON tokens (grant_id) WHERE grant_id IS NOT NULL;

-- +goose Down
DROP INDEX tokens_grant;
ALTER TABLE tokens DROP COLUMN grant_id;
DROP TABLE oauth_codes;
DROP TABLE oauth_grants;
DROP TABLE oauth_app_secrets;
DROP TABLE oauth_apps;
