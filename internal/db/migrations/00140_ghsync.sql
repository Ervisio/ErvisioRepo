-- +goose Up
-- GitHub code sync (PLAN §5.6): connections, one-off imports, pull
-- mirrors, backups and bidirectional sync. Credentials are sealed with
-- AES-256-GCM under a key derived from the master key (never in clear).

-- A GitHub App installation or a fine-grained token, owned by a user or an
-- organization namespace.
CREATE TABLE github_connections (
    id              uuid        PRIMARY KEY,
    owner_id        uuid        NOT NULL REFERENCES namespaces (id) ON DELETE CASCADE,
    name            text        NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    kind            text        NOT NULL CHECK (kind IN ('app', 'token')),
    -- Web URL of github.com or of a GitHub Enterprise Server.
    base_url        text        NOT NULL DEFAULT 'https://github.com' CHECK (base_url ~ '^https://'),
    app_id          bigint      NOT NULL DEFAULT 0 CHECK (app_id >= 0),
    installation_id bigint      NOT NULL DEFAULT 0 CHECK (installation_id >= 0),
    -- Sealed private key PEM (app) or token (token).
    secret          bytea       NOT NULL,
    -- Sealed secret of the webhook signatures (X-Hub-Signature-256).
    webhook_secret  bytea       NOT NULL,
    created_by      uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (kind <> 'app' OR (app_id > 0 AND installation_id > 0))
);

CREATE UNIQUE INDEX github_connections_owner_name ON github_connections (owner_id, lower(name));

-- The GitHub counterpart of a repository: at most one per repository.
CREATE TABLE github_sync_links (
    id               uuid        PRIMARY KEY,
    repo_id          uuid        NOT NULL UNIQUE REFERENCES repos (id) ON DELETE CASCADE,
    connection_id    uuid        NOT NULL REFERENCES github_connections (id) ON DELETE CASCADE,
    mode             text        NOT NULL CHECK (mode IN ('mirror', 'backup', 'sync')),
    remote_owner     text        NOT NULL CHECK (remote_owner ~ '^[A-Za-z0-9][A-Za-z0-9-]{0,38}$'),
    remote_name      text        NOT NULL CHECK (remote_name ~ '^[A-Za-z0-9._-]{1,100}$'),
    -- Deletions propagate only with prune.
    prune            boolean     NOT NULL DEFAULT false,
    -- Branch patterns (path.Match) on which GitHub wins a divergence (sync).
    trust_github     text[]      NOT NULL DEFAULT '{}',
    -- Create the GitHub repository (private) when it does not exist.
    create_missing   boolean     NOT NULL DEFAULT false,
    -- Period of the scheduled fetch (mirror, sync) or check (backup).
    interval_seconds integer     NOT NULL DEFAULT 600 CHECK (interval_seconds >= 600),
    enabled          boolean     NOT NULL DEFAULT true,
    status           text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ok', 'diverged', 'error')),
    last_error       text        NOT NULL DEFAULT '',
    -- Consecutive failed runs: the next one waits an exponential backoff.
    failures         integer     NOT NULL DEFAULT 0 CHECK (failures >= 0),
    next_attempt_at  timestamptz,
    last_run_at      timestamptz,
    last_success_at  timestamptz,
    -- The repository has Git LFS objects, which are never sent to GitHub.
    lfs_skipped      boolean     NOT NULL DEFAULT false,
    created_by       uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX github_sync_links_connection ON github_sync_links (connection_id);
CREATE INDEX github_sync_links_due ON github_sync_links (mode, last_run_at) WHERE enabled;

-- Per-ref state of a link. local_sha and remote_sha are the values seen on
-- each side after the last run ('' = absent).
CREATE TABLE github_sync_refs (
    link_id      uuid        NOT NULL REFERENCES github_sync_links (id) ON DELETE CASCADE,
    ref          text        NOT NULL CHECK (length(ref) BETWEEN 1 AND 255),
    local_sha    text        NOT NULL DEFAULT '' CHECK (local_sha = '' OR local_sha ~ '^[0-9a-f]{40,64}$'),
    remote_sha   text        NOT NULL DEFAULT '' CHECK (remote_sha = '' OR remote_sha ~ '^[0-9a-f]{40,64}$'),
    status       text        NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'diverged', 'error')),
    last_error   text        NOT NULL DEFAULT '',
    retries      integer     NOT NULL DEFAULT 0 CHECK (retries >= 0),
    -- GitHub commit kept at refs/heads/sync-conflict/github/<branch>.
    conflict_sha text        NOT NULL DEFAULT '' CHECK (conflict_sha = '' OR conflict_sha ~ '^[0-9a-f]{40,64}$'),
    -- Recorded resolution of a divergence still open ('' or merge_request).
    resolution   text        NOT NULL DEFAULT '' CHECK (resolution IN ('', 'merge_request')),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (link_id, ref)
);

-- One-off import of the git data of a GitHub repository.
CREATE TABLE github_imports (
    id            uuid        PRIMARY KEY,
    repo_id       uuid        NOT NULL UNIQUE REFERENCES repos (id) ON DELETE CASCADE,
    connection_id uuid        REFERENCES github_connections (id) ON DELETE SET NULL,
    remote_owner  text        NOT NULL,
    remote_name   text        NOT NULL,
    status        text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'done', 'error')),
    last_error    text        NOT NULL DEFAULT '',
    created_by    uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    finished_at   timestamptz
);

-- +goose Down
DROP TABLE github_imports;
DROP TABLE github_sync_refs;
DROP TABLE github_sync_links;
DROP TABLE github_connections;
