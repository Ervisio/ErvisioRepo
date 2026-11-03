-- +goose Up
-- Commit statuses and check runs/suites reported by CI systems (PLAN §5).
-- They feed the required status checks of branch protection and the
-- auto-merge of pull requests. Rows are keyed by commit sha, not by ref.
CREATE TABLE commit_statuses (
    id          uuid        PRIMARY KEY,
    repo_id     uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    sha         text        NOT NULL CHECK (sha ~ '^[0-9a-f]{40,64}$'),
    state       text        NOT NULL CHECK (state IN ('error', 'failure', 'pending', 'success')),
    context     text        NOT NULL DEFAULT 'default' CHECK (length(context) BETWEEN 1 AND 255),
    description text        NOT NULL DEFAULT '' CHECK (length(description) <= 1024),
    target_url  text        NOT NULL DEFAULT '' CHECK (length(target_url) <= 2048),
    creator_id  uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX commit_statuses_sha ON commit_statuses (repo_id, sha, context, created_at DESC);

-- One suite per (commit, app): the app is the reporter (token owner).
CREATE TABLE check_suites (
    id          uuid        PRIMARY KEY,
    repo_id     uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    head_sha    text        NOT NULL CHECK (head_sha ~ '^[0-9a-f]{40,64}$'),
    head_branch text        NOT NULL DEFAULT '' CHECK (length(head_branch) <= 255),
    app         text        NOT NULL CHECK (length(app) BETWEEN 1 AND 255),
    status      text        NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'in_progress', 'completed')),
    conclusion  text        NOT NULL DEFAULT '' CHECK (conclusion IN ('', 'success', 'failure', 'neutral',
                    'cancelled', 'skipped', 'timed_out', 'action_required', 'stale', 'startup_failure')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX check_suites_head ON check_suites (repo_id, head_sha, app);

CREATE TABLE check_runs (
    id             uuid        PRIMARY KEY,
    suite_id       uuid        NOT NULL REFERENCES check_suites (id) ON DELETE CASCADE,
    repo_id        uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    head_sha       text        NOT NULL CHECK (head_sha ~ '^[0-9a-f]{40,64}$'),
    name           text        NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    status         text        NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'in_progress', 'completed')),
    conclusion     text        NOT NULL DEFAULT '' CHECK (conclusion IN ('', 'success', 'failure', 'neutral',
                       'cancelled', 'skipped', 'timed_out', 'action_required', 'stale', 'startup_failure')),
    external_id    text        NOT NULL DEFAULT '' CHECK (length(external_id) <= 255),
    details_url    text        NOT NULL DEFAULT '' CHECK (length(details_url) <= 2048),
    output_title   text        NOT NULL DEFAULT '' CHECK (length(output_title) <= 1024),
    output_summary text        NOT NULL DEFAULT '' CHECK (length(output_summary) <= 65535),
    output_text    text        NOT NULL DEFAULT '' CHECK (length(output_text) <= 65535),
    -- JSON array of {path, start_line, end_line, annotation_level, message, title, raw_details}.
    annotations    jsonb       NOT NULL DEFAULT '[]',
    creator_id     uuid        REFERENCES users (id) ON DELETE SET NULL,
    started_at     timestamptz,
    completed_at   timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    -- A completed run always has a conclusion, and only a completed run has one.
    CHECK ((status = 'completed') = (conclusion <> ''))
);

CREATE INDEX check_runs_head ON check_runs (repo_id, head_sha, name, created_at DESC);
CREATE INDEX check_runs_suite ON check_runs (suite_id, name, created_at DESC);

-- +goose Down
DROP TABLE check_runs;
DROP TABLE check_suites;
DROP TABLE commit_statuses;
