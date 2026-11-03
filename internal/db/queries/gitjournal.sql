-- Write path of the git journal: durability, compaction, maintenance and
-- garbage collection (PLAN §4.3).

-- name: MarkRepoJournalDirty :exec
-- Records that the hot copy is ahead of the remote manifest; the first
-- unreplicated change wins.
UPDATE repos SET journal_dirty_since = COALESCE(journal_dirty_since, now())
WHERE id = sqlc.arg(id);

-- name: ClearRepoJournalDirty :exec
-- Clears the marker after a flush. Markers are set and cleared only under
-- the repository write lease, so no change can slip in between.
UPDATE repos SET journal_dirty_since = NULL WHERE id = $1;

-- name: GetRepoJournal :one
SELECT id, owner_id, storage_state, journal_dirty_since, pushes_since_maintenance, manifest_seq, size_bytes
FROM repos WHERE id = $1;

-- name: ListDirtyRepos :many
-- Repositories still waiting for replication since before "before".
SELECT id FROM repos
WHERE journal_dirty_since IS NOT NULL AND journal_dirty_since < sqlc.arg(before)::timestamptz
ORDER BY journal_dirty_since
LIMIT sqlc.arg(lim);

-- name: IncRepoPushes :one
UPDATE repos SET pushes_since_maintenance = pushes_since_maintenance + 1
WHERE id = $1
RETURNING pushes_since_maintenance;

-- name: ResetRepoPushes :exec
UPDATE repos SET pushes_since_maintenance = 0 WHERE id = $1;

-- name: ListRepoIDsAfter :many
-- Keyset pagination over every repository, for the garbage collector.
SELECT id FROM repos WHERE id > sqlc.arg(after)::uuid ORDER BY id LIMIT sqlc.arg(lim);

-- name: InsertRetiredGeneration :exec
INSERT INTO git_generations_retired (repo_id, generation, keys)
VALUES (sqlc.arg(repo_id), sqlc.arg(generation), sqlc.arg(keys)::text[])
ON CONFLICT (repo_id, generation) DO UPDATE SET keys = EXCLUDED.keys;

-- name: ListRetiredGenerations :many
-- Newest first.
SELECT repo_id, generation, keys, retired_at
FROM git_generations_retired WHERE repo_id = $1
ORDER BY generation DESC;

-- name: DeleteRetiredGeneration :exec
DELETE FROM git_generations_retired WHERE repo_id = $1 AND generation = $2;
