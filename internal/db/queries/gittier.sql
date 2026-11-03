-- Hydration, eviction, prewarm and restore of the hot tier (PLAN §4.3, §4.5).

-- name: HotRepoUsage :one
-- Bytes held by the hot repositories, as recorded in repos.size_bytes.
SELECT COALESCE(sum(size_bytes), 0)::bigint AS used
FROM repos WHERE storage_state = 'hot';

-- name: ListHotUnpinnedRepos :many
-- Watermark eviction candidates; the caller weights age by size.
SELECT id, size_bytes, last_access_at
FROM repos
WHERE storage_state = 'hot' AND NOT pinned AND journal_dirty_since IS NULL
ORDER BY last_access_at
LIMIT sqlc.arg(lim);

-- name: ListReposByState :many
-- Repositories in a storage state with their path, for prewarm.
SELECT r.id, n.name AS owner_name, r.name
FROM repos r
JOIN namespaces n ON n.id = r.owner_id
WHERE r.storage_state = sqlc.arg(state)
ORDER BY r.last_access_at DESC;

-- name: ListRepoTiering :many
-- Every repository with its journal sequence, for restore.
SELECT id, storage_state, manifest_seq
FROM repos
ORDER BY id;

-- name: MarkAllReposCold :execrows
-- After a disaster recovery the hot tier is empty: every repository must be
-- hydrated again from the remote journal.
UPDATE repos SET storage_state = 'cold', state_changed_at = now()
WHERE storage_state <> 'cold';

-- name: ForceRepoStorageState :exec
-- Sets the state unconditionally (restore only, nothing else runs).
UPDATE repos SET storage_state = sqlc.arg(state), state_changed_at = now()
WHERE id = sqlc.arg(id);

-- name: ResetRepoManifestSeq :exec
-- Sets the journal sequence to the one of the remote manifest (restore).
UPDATE repos SET manifest_seq = sqlc.arg(seq)::bigint WHERE id = sqlc.arg(id);

-- name: RepoJournalDirty :one
-- True while the hot copy holds pushes not yet in the remote journal.
SELECT (journal_dirty_since IS NOT NULL)::boolean AS dirty FROM repos WHERE id = $1;
