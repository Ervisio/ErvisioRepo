-- name: GetLFSObject :one
SELECT repo_id, oid, size, charged_to, created_at, seen_at
FROM lfs_objects WHERE repo_id = $1 AND oid = $2;

-- name: ListLFSObjectsByOIDs :many
SELECT repo_id, oid, size, charged_to, created_at, seen_at
FROM lfs_objects WHERE repo_id = $1 AND oid = ANY(sqlc.arg(oids)::text[]);

-- name: InsertLFSObject :execrows
-- Grants the repository access to an object; 0 rows when it already has it.
INSERT INTO lfs_objects (repo_id, oid, size, charged_to)
VALUES ($1, $2, $3, $4)
ON CONFLICT (repo_id, oid) DO NOTHING;

-- name: RepoLFSUsage :one
SELECT count(*)::bigint AS objects, coalesce(sum(size), 0)::bigint AS size_bytes
FROM lfs_objects WHERE repo_id = $1;

-- name: CountLFSObjectRefs :one
-- Repositories (existing or not yet collected) that reference an object.
SELECT count(*) FROM lfs_objects WHERE oid = $1;

-- name: ListOrphanLFSObjects :many
-- Rows of repositories that no longer exist.
SELECT o.repo_id, o.oid, o.size, o.charged_to
FROM lfs_objects o
WHERE NOT EXISTS (SELECT 1 FROM repos r WHERE r.id = o.repo_id)
ORDER BY o.repo_id, o.oid
LIMIT sqlc.arg(max_rows);

-- name: ListMischargedLFSObjects :many
-- Rows charged to a namespace that no longer owns the repository (transfer).
SELECT o.repo_id, o.oid, o.size, o.charged_to, r.owner_id
FROM lfs_objects o
JOIN repos r ON r.id = o.repo_id
WHERE o.charged_to <> r.owner_id
ORDER BY o.repo_id, o.oid
LIMIT sqlc.arg(max_rows);

-- name: SetLFSObjectCharge :execrows
UPDATE lfs_objects SET charged_to = sqlc.arg(owner_id)
WHERE repo_id = $1 AND oid = $2 AND charged_to = sqlc.arg(charged_to);

-- name: DeleteLFSObject :one
DELETE FROM lfs_objects WHERE repo_id = $1 AND oid = $2
RETURNING size, charged_to;

-- name: ListLFSGCRepos :many
-- Hot repositories with LFS objects not seen since before (GC candidates).
SELECT DISTINCT o.repo_id
FROM lfs_objects o
JOIN repos r ON r.id = o.repo_id
WHERE r.storage_state = 'hot' AND o.seen_at < sqlc.arg(before)
LIMIT sqlc.arg(max_repos);

-- name: ListRepoLFSObjects :many
SELECT oid, size, created_at, seen_at
FROM lfs_objects WHERE repo_id = $1 ORDER BY oid;

-- name: TouchLFSObjects :exec
UPDATE lfs_objects SET seen_at = now()
WHERE repo_id = $1 AND oid = ANY(sqlc.arg(oids)::text[]);

-- name: NamespaceExists :one
SELECT EXISTS (SELECT 1 FROM namespaces WHERE id = $1);

-- name: CreateLFSLock :one
INSERT INTO lfs_locks (id, repo_id, path, refname, owner_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (repo_id, path) DO NOTHING
RETURNING id, repo_id, path, refname, owner_id, created_at;

-- name: GetLFSLockByPath :one
SELECT l.id, l.repo_id, l.path, l.refname, l.owner_id, l.created_at, n.name AS owner_name
FROM lfs_locks l JOIN namespaces n ON n.id = l.owner_id
WHERE l.repo_id = $1 AND l.path = $2;

-- name: GetLFSLock :one
SELECT l.id, l.repo_id, l.path, l.refname, l.owner_id, l.created_at, n.name AS owner_name
FROM lfs_locks l JOIN namespaces n ON n.id = l.owner_id
WHERE l.repo_id = $1 AND l.id = $2;

-- name: ListLFSLocks :many
-- Keyset pagination on (created_at, id); filters are optional.
SELECT l.id, l.repo_id, l.path, l.refname, l.owner_id, l.created_at, n.name AS owner_name
FROM lfs_locks l JOIN namespaces n ON n.id = l.owner_id
WHERE l.repo_id = sqlc.arg(repo_id)
  AND (sqlc.arg(path)::text = '' OR l.path = sqlc.arg(path))
  AND (sqlc.narg(lock_id)::uuid IS NULL OR l.id = sqlc.narg(lock_id))
  AND (sqlc.narg(owner_id)::uuid IS NULL OR l.owner_id = sqlc.narg(owner_id))
  AND (sqlc.narg(not_owner_id)::uuid IS NULL OR l.owner_id <> sqlc.narg(not_owner_id))
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (l.created_at, l.id) > (sqlc.narg(after_created)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY l.created_at, l.id
LIMIT sqlc.arg(max_rows);

-- name: DeleteLFSLock :execrows
DELETE FROM lfs_locks WHERE repo_id = $1 AND id = $2;

-- name: InsertLFSToken :exec
INSERT INTO lfs_tokens (id, token_hash, repo_id, user_id, operation, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetLFSToken :one
SELECT t.id, t.repo_id, t.user_id, t.operation, t.expires_at,
       n.name AS username, u.role, u.suspended_at
FROM lfs_tokens t
JOIN users u ON u.id = t.user_id
JOIN namespaces n ON n.id = u.id
WHERE t.token_hash = $1;

-- name: DeleteExpiredLFSTokens :execrows
DELETE FROM lfs_tokens WHERE expires_at < sqlc.arg(before);

-- name: ListUnreferencedLFSStorage :many
-- Stored LFS objects that no repository references any more (the oid is
-- the last path segment of the key). Only the content-addressed area
-- lfs/sha256/ is considered: the class also holds release assets
-- (lfs/releases/), which are collected by internal/releases.
SELECT s.key, s.size
FROM storage_objects s
WHERE s.policy = 'lfs' AND s.target = sqlc.arg(target)
  AND s.key LIKE 'lfs/sha256/%'
  AND s.updated_at < sqlc.arg(before)
  AND NOT EXISTS (SELECT 1 FROM lfs_objects o WHERE o.oid = right(s.key, 64))
ORDER BY s.updated_at
LIMIT sqlc.arg(max_rows);
