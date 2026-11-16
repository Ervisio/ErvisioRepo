-- +goose Up
-- Apps in the style of GitHub Apps (APPS-PLAN A2). An app is owned by a
-- user or an organization, acts as its bot account slug[bot] (bots.app_id)
-- and authenticates with RS256 keys whose public half only is stored. Its
-- linked oauth_apps row gives it a client id and secrets for the
-- user-to-server web flow. num_id is the numeric id of the GitHub API
-- (the iss of the app JWT, the id of an installation).

CREATE TABLE apps (
    id           uuid        PRIMARY KEY,
    num_id       bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    slug         text        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9](-?[a-z0-9])*$' AND length(slug) <= 34),
    owner_id     uuid        NOT NULL REFERENCES namespaces (id) ON DELETE CASCADE,
    oauth_app_id uuid        NOT NULL UNIQUE REFERENCES oauth_apps (id),
    bot_id       uuid        NOT NULL UNIQUE REFERENCES users (id),
    name         text        NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    name_lower   text        GENERATED ALWAYS AS (lower(name)) STORED UNIQUE,
    description  text        NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
    homepage_url text        NOT NULL DEFAULT '',
    logo_url     text        NOT NULL DEFAULT '',
    public       boolean     NOT NULL DEFAULT false,
    -- {"contents": "write", "metadata": "read", ...}: GitHub permission
    -- names, levels read or write.
    permissions  jsonb       NOT NULL DEFAULT '{"metadata": "read"}',
    -- GitHub event names the app subscribes to.
    events       text[]      NOT NULL DEFAULT '{}',
    setup_url    text        NOT NULL DEFAULT '',
    created_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX apps_owner ON apps (owner_id);
CREATE INDEX apps_public ON apps (created_at DESC) WHERE public;

-- The bot of a deleted app stays (it authored comments and commits) and
-- keeps its login, so the slug is not reused.
ALTER TABLE bots ADD CONSTRAINT bots_app_fk FOREIGN KEY (app_id) REFERENCES apps (id) ON DELETE SET NULL;

-- Private keys: the PKIX DER public key and its SHA-256 fingerprint.
CREATE TABLE app_keys (
    id           uuid        PRIMARY KEY,
    app_id       uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    public_key   bytea       NOT NULL,
    fingerprint  text        NOT NULL UNIQUE,
    created_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);

CREATE INDEX app_keys_app ON app_keys (app_id);

-- An installation of an app on a user or organization account. The
-- permissions and events are the accepted ones: when the app asks for more,
-- the installation keeps these until an owner accepts.
CREATE TABLE app_installations (
    id                   uuid        PRIMARY KEY,
    num_id               bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    app_id               uuid        NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    account_id           uuid        NOT NULL REFERENCES namespaces (id) ON DELETE CASCADE,
    repository_selection text        NOT NULL DEFAULT 'all' CHECK (repository_selection IN ('all', 'selected')),
    permissions          jsonb       NOT NULL DEFAULT '{}',
    events               text[]      NOT NULL DEFAULT '{}',
    suspended_at         timestamptz,
    suspended_by         uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_by           uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (app_id, account_id)
);

CREATE INDEX app_installations_account ON app_installations (account_id);

CREATE TABLE app_installation_repos (
    installation_id uuid NOT NULL REFERENCES app_installations (id) ON DELETE CASCADE,
    repo_id         uuid NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    PRIMARY KEY (installation_id, repo_id)
);

CREATE INDEX app_installation_repos_repo ON app_installation_repos (repo_id);

-- Installation access tokens (erps_...), stored as their sha256. repo_ids
-- NULL means the repositories of the installation; permissions are the
-- (possibly narrowed) permissions of the token.
CREATE TABLE app_installation_tokens (
    id              uuid        PRIMARY KEY,
    installation_id uuid        NOT NULL REFERENCES app_installations (id) ON DELETE CASCADE,
    token_hash      bytea       NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    permissions     jsonb       NOT NULL,
    repo_ids        uuid[],
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL
);

CREATE INDEX app_installation_tokens_installation ON app_installation_tokens (installation_id);
CREATE INDEX app_installation_tokens_expires ON app_installation_tokens (expires_at);

-- The webhook of an app is a webhooks row with app_id; its deliveries are
-- the app's "recent deliveries".
ALTER TABLE webhooks ADD COLUMN app_id uuid REFERENCES apps (id) ON DELETE CASCADE;
ALTER TABLE webhooks DROP CONSTRAINT webhooks_check;
ALTER TABLE webhooks ADD CONSTRAINT webhooks_scope_check CHECK (num_nonnulls(repo_id, org_id, app_id) = 1);
CREATE UNIQUE INDEX webhooks_app ON webhooks (app_id) WHERE app_id IS NOT NULL;

-- Check suites reported with an installation token belong to the app.
ALTER TABLE check_suites ADD COLUMN app_id uuid REFERENCES apps (id) ON DELETE SET NULL;

-- Required status checks may name the app that must report them:
-- {"context": app num_id}.
ALTER TABLE branch_protections ADD COLUMN required_check_apps jsonb NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE branch_protections DROP COLUMN required_check_apps;
ALTER TABLE check_suites DROP COLUMN app_id;
DROP INDEX webhooks_app;
ALTER TABLE webhooks DROP CONSTRAINT webhooks_scope_check;
DELETE FROM webhooks WHERE app_id IS NOT NULL;
ALTER TABLE webhooks ADD CONSTRAINT webhooks_check CHECK ((repo_id IS NULL) <> (org_id IS NULL));
ALTER TABLE webhooks DROP COLUMN app_id;
DROP TABLE app_installation_tokens;
DROP TABLE app_installation_repos;
DROP TABLE app_installations;
DROP TABLE app_keys;
ALTER TABLE bots DROP CONSTRAINT bots_app_fk;
DROP TABLE apps;
