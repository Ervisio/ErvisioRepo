-- +goose Up
-- Write path of the git journal (PLAN §4.3): pending replication of pushes
-- under the async durability policy, the maintenance counter and the
-- generations retired by compaction.

-- journal_dirty_since is set when the hot copy holds refs or objects not yet
-- in the remote manifest (async durability, or a failed sync upload). A
-- dirty repository must not be evicted.
ALTER TABLE repos ADD COLUMN journal_dirty_since timestamptz;
-- Pushes recorded since the last git maintenance run.
ALTER TABLE repos ADD COLUMN pushes_since_maintenance integer NOT NULL DEFAULT 0
    CHECK (pushes_since_maintenance >= 0);

CREATE INDEX repos_journal_dirty ON repos (journal_dirty_since) WHERE journal_dirty_since IS NOT NULL;

-- A generation replaced by compaction: its base and segment keys are kept
-- for a while (readers may still hold the old manifest), then deleted.
CREATE TABLE git_generations_retired (
    repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    generation bigint      NOT NULL CHECK (generation >= 0),
    keys       text[]      NOT NULL,
    retired_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, generation)
);

-- +goose Down
DROP TABLE git_generations_retired;
DROP INDEX repos_journal_dirty;
ALTER TABLE repos DROP COLUMN pushes_since_maintenance;
ALTER TABLE repos DROP COLUMN journal_dirty_since;
