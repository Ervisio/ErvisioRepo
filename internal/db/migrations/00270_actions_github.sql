-- +goose Up
-- GitHub Actions import (internal/actions/ghimport.go).
--
-- run_actions: the ref updates a sync link writes (mirror, sync) start
-- workflows like a push, by the sync bot. Off by default.
ALTER TABLE github_sync_links ADD COLUMN run_actions boolean NOT NULL DEFAULT false;

-- system_actor names the bot that caused a run without a user
-- (ervisio-sync[bot] for synced pushes); empty is ervisio-actions[bot].
ALTER TABLE actions_runs ADD COLUMN system_actor text NOT NULL DEFAULT ''
    CHECK (length(system_actor) <= 64);

-- One row per repository whose Actions configuration was imported from
-- GitHub. secret_names lists the secrets GitHub has ([{name, environment}]):
-- their values cannot be read, the UI shows the ones still to fill in.
CREATE TABLE actions_github_imports (
    repo_id            uuid        PRIMARY KEY REFERENCES repos (id) ON DELETE CASCADE,
    secret_names       jsonb       NOT NULL DEFAULT '[]',
    variables_imported integer     NOT NULL DEFAULT 0,
    variables_skipped  integer     NOT NULL DEFAULT 0,
    runs_imported      integer     NOT NULL DEFAULT 0,
    last_error         text        NOT NULL DEFAULT '',
    imported_at        timestamptz,
    runs_synced_at     timestamptz,
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- Workflow runs read from GitHub: read-only history, never touched by the
-- scheduler or the runners (they have no jobs). html_url links to the
-- logs on GitHub.
CREATE TABLE actions_external_runs (
    id            uuid        PRIMARY KEY,
    repo_id       uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    provider      text        NOT NULL DEFAULT 'github' CHECK (provider IN ('github')),
    external_id   bigint      NOT NULL,
    run_number    bigint      NOT NULL DEFAULT 0,
    attempt       integer     NOT NULL DEFAULT 1,
    workflow_path text        NOT NULL DEFAULT '' CHECK (length(workflow_path) <= 1024),
    workflow_name text        NOT NULL DEFAULT '' CHECK (length(workflow_name) <= 1024),
    event         text        NOT NULL DEFAULT '' CHECK (length(event) <= 64),
    ref           text        NOT NULL DEFAULT '' CHECK (length(ref) <= 1024),
    sha           text        NOT NULL DEFAULT '' CHECK (length(sha) <= 64),
    title         text        NOT NULL DEFAULT '' CHECK (length(title) <= 1024),
    status        text        NOT NULL DEFAULT 'completed'
                              CHECK (status IN ('pending', 'waiting', 'queued', 'in_progress', 'completed')),
    conclusion    text        NOT NULL DEFAULT '' CHECK (length(conclusion) <= 32),
    actor_login   text        NOT NULL DEFAULT '' CHECK (length(actor_login) <= 100),
    html_url      text        NOT NULL DEFAULT '' CHECK (length(html_url) <= 2048),
    started_at    timestamptz,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    UNIQUE (repo_id, provider, external_id)
);

CREATE INDEX actions_external_runs_repo ON actions_external_runs (repo_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE actions_external_runs;
DROP TABLE actions_github_imports;
ALTER TABLE actions_runs DROP COLUMN system_actor;
ALTER TABLE github_sync_links DROP COLUMN run_actions;
