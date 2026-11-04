-- +goose Up
-- Results services of Ervisio Actions (PLAN §6): the actions/cache entries,
-- the v4 artifacts and the parts of uploads in progress. Blobs live in
-- storage (policy "actions"); these tables hold the metadata.

-- One cache entry per (repo, scope, key, version). scope is the git ref
-- the entry was saved from (refs/heads/main, refs/pull/1/merge). Entries
-- are immutable once complete.
CREATE TABLE actions_caches (
    id           bigserial   PRIMARY KEY,
    repo_id      uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    scope        text        NOT NULL CHECK (length(scope) BETWEEN 1 AND 1024),
    key          text        NOT NULL CHECK (length(key) BETWEEN 1 AND 512),
    version      text        NOT NULL CHECK (length(version) BETWEEN 1 AND 512),
    size         bigint      NOT NULL DEFAULT 0 CHECK (size >= 0),
    complete     boolean     NOT NULL DEFAULT false,
    -- Storage key of the blob, set once complete.
    storage_key  text        NOT NULL DEFAULT '',
    run_id       uuid        REFERENCES actions_runs (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    last_used_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repo_id, scope, key, version)
);

CREATE INDEX actions_caches_lookup ON actions_caches (repo_id, scope, version, key text_pattern_ops)
    WHERE complete;
CREATE INDEX actions_caches_lru ON actions_caches (repo_id, last_used_at) WHERE complete;

-- Artifacts of a run (upload-artifact v4). Names are unique per run.
CREATE TABLE actions_artifacts (
    id           bigserial   PRIMARY KEY,
    repo_id      uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    run_id       uuid        NOT NULL REFERENCES actions_runs (id) ON DELETE CASCADE,
    job_id       uuid        REFERENCES actions_jobs (id) ON DELETE SET NULL,
    name         text        NOT NULL CHECK (length(name) BETWEEN 1 AND 256),
    mime_type    text        NOT NULL DEFAULT '' CHECK (length(mime_type) <= 255),
    size         bigint      NOT NULL DEFAULT 0 CHECK (size >= 0),
    -- "sha256:<hex>" of the uploaded content.
    digest       text        NOT NULL DEFAULT '',
    -- Storage key of the blob, set when the upload is committed.
    storage_key  text        NOT NULL DEFAULT '',
    finalized    boolean     NOT NULL DEFAULT false,
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    finalized_at timestamptz,
    UNIQUE (run_id, name)
);

CREATE INDEX actions_artifacts_expiry ON actions_artifacts (expires_at);

-- Parts of uploads in progress: Azure-style blocks (block = block id),
-- appended blocks (block = generated id, ordered by seq) and v1 cache
-- chunks (block = zero-padded byte offset). owner names the upload
-- ("cache-<id>", "artifact-<id>").
CREATE TABLE actions_upload_parts (
    owner       text        NOT NULL CHECK (length(owner) BETWEEN 1 AND 128),
    block       text        NOT NULL CHECK (length(block) BETWEEN 1 AND 128),
    seq         bigserial,
    size        bigint      NOT NULL CHECK (size >= 0),
    storage_key text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner, block)
);

CREATE INDEX actions_upload_parts_age ON actions_upload_parts (created_at);

-- +goose Down
DROP TABLE actions_upload_parts;
DROP TABLE actions_artifacts;
DROP TABLE actions_caches;
