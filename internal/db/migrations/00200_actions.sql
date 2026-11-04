-- +goose Up
-- Ervisio Actions (PLAN §6): runners, workflow runs, jobs, steps, logs,
-- secrets, variables, environments and schedules.

-- Runners dial the server and take jobs whose runs-on labels they carry.
-- A runner serves the whole instance, the repositories of one owner (org
-- or user) or a single repository.
CREATE TABLE actions_runners (
    id              uuid        PRIMARY KEY,
    name            text        NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    scope           text        NOT NULL CHECK (scope IN ('instance', 'org', 'repo')),
    owner_id        uuid        REFERENCES namespaces (id) ON DELETE CASCADE,
    repo_id         uuid        REFERENCES repos (id) ON DELETE CASCADE,
    -- Label names, lower case (the "name" part of "name:scheme://image").
    labels          text[]      NOT NULL DEFAULT '{}',
    -- Labels as declared by the runner, for display.
    raw_labels      text[]      NOT NULL DEFAULT '{}',
    version         text        NOT NULL DEFAULT '' CHECK (length(version) <= 255),
    ephemeral       boolean     NOT NULL DEFAULT false,
    -- SHA-256 of the erun_ credential.
    credential_hash bytea       NOT NULL UNIQUE,
    online          boolean     NOT NULL DEFAULT false,
    last_seen_at    timestamptz,
    created_by      uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK ((scope = 'instance' AND owner_id IS NULL AND repo_id IS NULL)
        OR (scope = 'org' AND owner_id IS NOT NULL AND repo_id IS NULL)
        OR (scope = 'repo' AND owner_id IS NULL AND repo_id IS NOT NULL))
);

CREATE INDEX actions_runners_owner ON actions_runners (owner_id) WHERE owner_id IS NOT NULL;
CREATE INDEX actions_runners_repo ON actions_runners (repo_id) WHERE repo_id IS NOT NULL;

-- Registration tokens (errt_) are reusable until they expire.
CREATE TABLE actions_registration_tokens (
    id         uuid        PRIMARY KEY,
    token_hash bytea       NOT NULL UNIQUE,
    scope      text        NOT NULL CHECK (scope IN ('instance', 'org', 'repo')),
    owner_id   uuid        REFERENCES namespaces (id) ON DELETE CASCADE,
    repo_id    uuid        REFERENCES repos (id) ON DELETE CASCADE,
    created_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((scope = 'instance' AND owner_id IS NULL AND repo_id IS NULL)
        OR (scope = 'org' AND owner_id IS NOT NULL AND repo_id IS NULL)
        OR (scope = 'repo' AND owner_id IS NULL AND repo_id IS NOT NULL))
);

-- run_number is counted per (repository, workflow file).
CREATE TABLE actions_run_numbers (
    repo_id       uuid   NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    workflow_path text   NOT NULL,
    last          bigint NOT NULL,
    PRIMARY KEY (repo_id, workflow_path)
);

CREATE TABLE actions_runs (
    id                 uuid        PRIMARY KEY,
    repo_id            uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    -- e.g. ".github/workflows/ci.yml"; the content is the file at sha.
    workflow_path      text        NOT NULL CHECK (length(workflow_path) BETWEEN 1 AND 1024),
    workflow_name      text        NOT NULL DEFAULT '' CHECK (length(workflow_name) <= 1024),
    workflow           bytea       NOT NULL,
    event              text        NOT NULL CHECK (length(event) BETWEEN 1 AND 64),
    event_action       text        NOT NULL DEFAULT '' CHECK (length(event_action) <= 64),
    ref                text        NOT NULL DEFAULT '' CHECK (length(ref) <= 1024),
    sha                text        NOT NULL CHECK (sha ~ '^[0-9a-f]{40,64}$'),
    title              text        NOT NULL DEFAULT '' CHECK (length(title) <= 1024),
    run_number         bigint      NOT NULL CHECK (run_number > 0),
    attempt            integer     NOT NULL DEFAULT 1 CHECK (attempt > 0),
    -- pending: waits for the workflow concurrency group;
    -- waiting: waits for an approval (untrusted contributor).
    status             text        NOT NULL DEFAULT 'queued'
                                   CHECK (status IN ('pending', 'waiting', 'queued', 'in_progress', 'completed')),
    conclusion         text        NOT NULL DEFAULT '' CHECK (conclusion IN ('', 'success', 'failure',
                                       'cancelled', 'skipped', 'timed_out', 'action_required')),
    actor_id           uuid        REFERENCES users (id) ON DELETE SET NULL,
    trigger_actor_id   uuid        REFERENCES users (id) ON DELETE SET NULL,
    -- GitHub-shaped event payload (github.event).
    event_payload      jsonb       NOT NULL DEFAULT '{}',
    concurrency_group  text        NOT NULL DEFAULT '' CHECK (length(concurrency_group) <= 1024),
    concurrency_cancel boolean     NOT NULL DEFAULT false,
    -- The head repository of a pull request run, when it is a fork.
    head_repo_id       uuid        REFERENCES repos (id) ON DELETE SET NULL,
    -- fork runs get no secrets and a read-only token.
    fork               boolean     NOT NULL DEFAULT false,
    -- needs_approval holds the run in "waiting" until a maintainer
    -- approves it; approved_by records who did.
    needs_approval     boolean     NOT NULL DEFAULT false,
    approved_by        uuid        REFERENCES users (id) ON DELETE SET NULL,
    started_at         timestamptz,
    completed_at       timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repo_id, workflow_path, run_number),
    CHECK ((status = 'completed') = (conclusion <> ''))
);

CREATE INDEX actions_runs_repo ON actions_runs (repo_id, created_at DESC, id DESC);
CREATE INDEX actions_runs_workflow ON actions_runs (repo_id, workflow_path, created_at DESC);
CREATE INDEX actions_runs_open ON actions_runs (status) WHERE status <> 'completed';
CREATE INDEX actions_runs_concurrency ON actions_runs (repo_id, concurrency_group)
    WHERE concurrency_group <> '' AND status <> 'completed';

-- Jobs are the units a runner takes: one row per matrix combination and
-- per attempt. job_key is the id in the workflow ("build"), prefixed by
-- the caller job for jobs of a called workflow ("deploy.build").
CREATE TABLE actions_jobs (
    id                 uuid        PRIMARY KEY,
    run_id             uuid        NOT NULL REFERENCES actions_runs (id) ON DELETE CASCADE,
    repo_id            uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    attempt            integer     NOT NULL CHECK (attempt > 0),
    job_key            text        NOT NULL CHECK (length(job_key) BETWEEN 1 AND 1024),
    name               text        NOT NULL CHECK (length(name) BETWEEN 1 AND 1024),
    needs              text[]      NOT NULL DEFAULT '{}',
    matrix             jsonb,
    -- Single-job workflow sent to the runner (YAML).
    payload            bytea       NOT NULL,
    if_expr            text        NOT NULL DEFAULT '',
    -- runs-on labels, lower case: a runner must carry all of them.
    runs_on            text[]      NOT NULL DEFAULT '{}',
    timeout_minutes    integer     NOT NULL CHECK (timeout_minutes > 0),
    -- Effective token permissions, e.g. {"contents": "read"}.
    permissions        jsonb       NOT NULL DEFAULT '{}',
    -- inputs of a called workflow (github.event.inputs of its jobs).
    inputs             jsonb,
    environment        text        NOT NULL DEFAULT '' CHECK (length(environment) <= 255),
    -- blocked: needs are not done; waiting: environment approval or
    -- concurrency group.
    status             text        NOT NULL DEFAULT 'blocked'
                                   CHECK (status IN ('blocked', 'waiting', 'queued', 'in_progress', 'completed')),
    conclusion         text        NOT NULL DEFAULT '' CHECK (conclusion IN ('', 'success', 'failure',
                                       'cancelled', 'skipped', 'timed_out')),
    cancel_requested   boolean     NOT NULL DEFAULT false,
    approved_by        uuid        REFERENCES users (id) ON DELETE SET NULL,
    approved_at        timestamptz,
    concurrency_group  text        NOT NULL DEFAULT '' CHECK (length(concurrency_group) <= 1024),
    concurrency_cancel boolean     NOT NULL DEFAULT false,
    fail_fast          boolean     NOT NULL DEFAULT true,
    max_parallel       integer     NOT NULL DEFAULT 0 CHECK (max_parallel >= 0),
    runner_id          uuid        REFERENCES actions_runners (id) ON DELETE SET NULL,
    label              text        NOT NULL DEFAULT '',
    outputs            jsonb       NOT NULL DEFAULT '{}',
    -- SHA-256 of the erj_ job token, valid while the job runs.
    token_hash         bytea       UNIQUE,
    token_expires_at   timestamptz,
    log_length         bigint      NOT NULL DEFAULT 0 CHECK (log_length >= 0),
    -- Storage key of the gzip archive, once the log left Postgres.
    log_key            text        NOT NULL DEFAULT '',
    check_run_id       uuid,
    started_at         timestamptz,
    completed_at       timestamptz,
    last_update_at     timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'completed') = (conclusion <> ''))
);

CREATE INDEX actions_jobs_run ON actions_jobs (run_id, attempt, created_at, id);
CREATE INDEX actions_jobs_queued ON actions_jobs (created_at, id) WHERE status = 'queued';
CREATE INDEX actions_jobs_running ON actions_jobs (runner_id) WHERE status = 'in_progress';
CREATE INDEX actions_jobs_concurrency ON actions_jobs (repo_id, concurrency_group)
    WHERE concurrency_group <> '' AND status <> 'completed';

CREATE TABLE actions_steps (
    job_id       uuid        NOT NULL REFERENCES actions_jobs (id) ON DELETE CASCADE,
    idx          integer     NOT NULL CHECK (idx >= 0),
    name         text        NOT NULL DEFAULT '' CHECK (length(name) <= 1024),
    status       text        NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'in_progress', 'completed')),
    conclusion   text        NOT NULL DEFAULT '' CHECK (conclusion IN ('', 'success', 'failure', 'cancelled', 'skipped')),
    log_index    bigint      NOT NULL DEFAULT 0,
    log_length   bigint      NOT NULL DEFAULT 0,
    started_at   timestamptz,
    completed_at timestamptz,
    PRIMARY KEY (job_id, idx)
);

-- Log rows of running jobs; archived to storage when the job ends.
CREATE TABLE actions_log_rows (
    job_id  uuid        NOT NULL REFERENCES actions_jobs (id) ON DELETE CASCADE,
    idx     bigint      NOT NULL CHECK (idx >= 0),
    at      timestamptz NOT NULL,
    content text        NOT NULL,
    PRIMARY KEY (job_id, idx)
);

-- Secrets are sealed (envelope encryption, never returned once written).
-- scope_id folds the owner of each scope into one column for uniqueness.
CREATE TABLE actions_secrets (
    id          uuid        PRIMARY KEY,
    scope       text        NOT NULL CHECK (scope IN ('instance', 'org', 'repo', 'environment')),
    owner_id    uuid        REFERENCES namespaces (id) ON DELETE CASCADE,
    repo_id     uuid        REFERENCES repos (id) ON DELETE CASCADE,
    environment text        NOT NULL DEFAULT '' CHECK (length(environment) <= 255),
    name        text        NOT NULL CHECK (name ~ '^[A-Z_][A-Z0-9_]*$' AND length(name) <= 255),
    sealed      bytea       NOT NULL,
    created_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    scope_id    uuid        GENERATED ALWAYS AS
                    (COALESCE(repo_id, owner_id, '00000000-0000-0000-0000-000000000000'::uuid)) STORED,
    CHECK ((scope = 'instance' AND owner_id IS NULL AND repo_id IS NULL AND environment = '')
        OR (scope = 'org' AND owner_id IS NOT NULL AND repo_id IS NULL AND environment = '')
        OR (scope = 'repo' AND owner_id IS NULL AND repo_id IS NOT NULL AND environment = '')
        OR (scope = 'environment' AND owner_id IS NULL AND repo_id IS NOT NULL AND environment <> '')),
    UNIQUE (scope, scope_id, environment, name)
);

CREATE TABLE actions_variables (
    id          uuid        PRIMARY KEY,
    scope       text        NOT NULL CHECK (scope IN ('instance', 'org', 'repo', 'environment')),
    owner_id    uuid        REFERENCES namespaces (id) ON DELETE CASCADE,
    repo_id     uuid        REFERENCES repos (id) ON DELETE CASCADE,
    environment text        NOT NULL DEFAULT '' CHECK (length(environment) <= 255),
    name        text        NOT NULL CHECK (name ~ '^[A-Z_][A-Z0-9_]*$' AND length(name) <= 255),
    value       text        NOT NULL CHECK (length(value) <= 65536),
    created_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    scope_id    uuid        GENERATED ALWAYS AS
                    (COALESCE(repo_id, owner_id, '00000000-0000-0000-0000-000000000000'::uuid)) STORED,
    CHECK ((scope = 'instance' AND owner_id IS NULL AND repo_id IS NULL AND environment = '')
        OR (scope = 'org' AND owner_id IS NOT NULL AND repo_id IS NULL AND environment = '')
        OR (scope = 'repo' AND owner_id IS NULL AND repo_id IS NOT NULL AND environment = '')
        OR (scope = 'environment' AND owner_id IS NULL AND repo_id IS NOT NULL AND environment <> '')),
    UNIQUE (scope, scope_id, environment, name)
);

-- Deployment environments; jobs targeting one with require_approval wait
-- until a reviewer (or a repository admin) approves them.
CREATE TABLE actions_environments (
    id               uuid        PRIMARY KEY,
    repo_id          uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    name             text        NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    require_approval boolean     NOT NULL DEFAULT false,
    reviewers        uuid[]      NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repo_id, name)
);

-- on.schedule entries of the workflows of each default branch.
CREATE TABLE actions_schedules (
    id            uuid        PRIMARY KEY,
    repo_id       uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    workflow_path text        NOT NULL,
    cron          text        NOT NULL CHECK (length(cron) BETWEEN 1 AND 255),
    next_at       timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repo_id, workflow_path, cron)
);

CREATE INDEX actions_schedules_next ON actions_schedules (next_at);

-- Bumped whenever a job becomes queued, so that a runner polling with an
-- up to date tasks_version is answered without scanning the queue.
CREATE TABLE actions_tasks_version (
    id      boolean PRIMARY KEY DEFAULT true CHECK (id),
    version bigint  NOT NULL DEFAULT 0
);

INSERT INTO actions_tasks_version (id, version) VALUES (true, 0);

-- +goose Down
DROP TABLE actions_tasks_version;
DROP TABLE actions_schedules;
DROP TABLE actions_environments;
DROP TABLE actions_variables;
DROP TABLE actions_secrets;
DROP TABLE actions_log_rows;
DROP TABLE actions_steps;
DROP TABLE actions_jobs;
DROP TABLE actions_runs;
DROP TABLE actions_run_numbers;
DROP TABLE actions_registration_tokens;
DROP TABLE actions_runners;
