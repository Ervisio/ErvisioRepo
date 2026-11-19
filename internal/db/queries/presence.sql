-- Presence and file locks (internal/presence). The locks live in lfs_locks
-- (lfs.sql) and are shared with the Git LFS Locks API.

-- name: GetRepoPresenceEnabled :one
SELECT presence_enabled FROM repos WHERE id = $1;

-- name: SetRepoPresenceEnabled :execrows
-- 0 rows when the setting already had that value.
UPDATE repos SET presence_enabled = sqlc.arg(enabled), updated_at = now()
WHERE id = sqlc.arg(id) AND presence_enabled <> sqlc.arg(enabled);

-- name: GetPresenceForUpdate :one
SELECT branch, head, dirty, truncated, ahead, unpublished, active, updated_at
FROM repo_presence
WHERE repo_id = $1 AND user_id = $2 AND client_id = $3
FOR UPDATE;

-- name: UpsertPresence :exec
INSERT INTO repo_presence (repo_id, user_id, client_id, branch, head, dirty, truncated, ahead, unpublished, active, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
ON CONFLICT (repo_id, user_id, client_id) DO UPDATE
SET branch = EXCLUDED.branch, head = EXCLUDED.head, dirty = EXCLUDED.dirty, truncated = EXCLUDED.truncated,
    ahead = EXCLUDED.ahead, unpublished = EXCLUDED.unpublished, active = EXCLUDED.active, updated_at = now();

-- name: DeletePresence :execrows
DELETE FROM repo_presence WHERE repo_id = $1 AND user_id = $2 AND client_id = $3;

-- name: DeleteRepoPresence :exec
DELETE FROM repo_presence WHERE repo_id = $1;

-- name: ListLivePresence :many
-- Presences heard from since sqlc.arg(since), with their users.
SELECT p.user_id, p.client_id, p.branch, p.head, p.dirty, p.truncated, p.ahead, p.unpublished, p.active, p.updated_at,
       n.name AS login, u.display_name, u.avatar_url
FROM repo_presence p
JOIN users u ON u.id = p.user_id
JOIN namespaces n ON n.id = u.id
WHERE p.repo_id = sqlc.arg(repo_id) AND p.updated_at >= sqlc.arg(since)
ORDER BY n.name_lower, p.client_id;

-- name: GetLivePresence :one
SELECT p.user_id, p.client_id, p.branch, p.head, p.dirty, p.truncated, p.ahead, p.unpublished, p.active, p.updated_at,
       n.name AS login, u.display_name, u.avatar_url
FROM repo_presence p
JOIN users u ON u.id = p.user_id
JOIN namespaces n ON n.id = u.id
WHERE p.repo_id = sqlc.arg(repo_id) AND p.user_id = sqlc.arg(user_id) AND p.client_id = sqlc.arg(client_id);

-- name: DeleteExpiredPresence :many
-- Presences not heard from since sqlc.arg(before), at most max_rows of them.
WITH d AS (
    DELETE FROM repo_presence p
    WHERE (p.repo_id, p.user_id, p.client_id) IN (
        SELECT e.repo_id, e.user_id, e.client_id FROM repo_presence e
        WHERE e.updated_at < sqlc.arg(before)
        LIMIT sqlc.arg(max_rows))
    RETURNING p.repo_id, p.user_id, p.client_id)
SELECT d.repo_id, d.user_id, d.client_id, n.name AS login
FROM d JOIN namespaces n ON n.id = d.user_id;

-- name: ListPresenceLocks :many
-- Every lock of a repository with its owner, oldest first.
SELECT l.id, l.path, l.refname, l.owner_id, l.created_at, n.name AS login, u.display_name, u.avatar_url
FROM lfs_locks l
JOIN users u ON u.id = l.owner_id
JOIN namespaces n ON n.id = u.id
WHERE l.repo_id = $1
ORDER BY l.created_at, l.id
LIMIT sqlc.arg(max_rows);

-- name: GetPresenceLock :one
SELECT l.id, l.path, l.refname, l.owner_id, l.created_at, n.name AS login, u.display_name, u.avatar_url
FROM lfs_locks l
JOIN users u ON u.id = l.owner_id
JOIN namespaces n ON n.id = u.id
WHERE l.repo_id = $1 AND l.id = $2;

-- name: GetPresenceLockByPath :one
SELECT l.id, l.path, l.refname, l.owner_id, l.created_at, n.name AS login, u.display_name, u.avatar_url
FROM lfs_locks l
JOIN users u ON u.id = l.owner_id
JOIN namespaces n ON n.id = u.id
WHERE l.repo_id = $1 AND l.refname = $2 AND l.path = $3;

-- name: ListForeignRefLocks :many
-- Locks on one ref held by users other than sqlc.arg(user_id) (push check).
SELECT l.path, l.created_at, n.name AS login
FROM lfs_locks l
JOIN namespaces n ON n.id = l.owner_id
WHERE l.repo_id = sqlc.arg(repo_id) AND l.refname = sqlc.arg(refname) AND l.owner_id <> sqlc.arg(user_id);

-- name: DeleteRefLocks :many
-- Releases the locks of a deleted branch.
DELETE FROM lfs_locks WHERE repo_id = $1 AND refname = $2 RETURNING id;

-- name: NotifyPresence :exec
SELECT pg_notify('erepo_presence', sqlc.arg(payload)::text);

-- name: ListenPresence :exec
-- Presence streams of every instance wait on this channel.
LISTEN erepo_presence;
