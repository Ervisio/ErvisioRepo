-- GitHub code sync (internal/ghsync).

-- name: InsertGitHubConnection :one
INSERT INTO github_connections (id, owner_id, name, kind, base_url, app_id, installation_id, secret, webhook_secret, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetGitHubConnection :one
SELECT sqlc.embed(c), n.name AS owner_name, n.kind AS owner_kind
FROM github_connections c
JOIN namespaces n ON n.id = c.owner_id
WHERE c.id = $1;

-- name: ListGitHubConnections :many
SELECT sqlc.embed(c), n.name AS owner_name, n.kind AS owner_kind
FROM github_connections c
JOIN namespaces n ON n.id = c.owner_id
WHERE c.owner_id = $1
ORDER BY lower(c.name);

-- name: UpdateGitHubConnection :one
UPDATE github_connections
SET name = $2, app_id = $3, installation_id = $4, secret = $5, webhook_secret = $6, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteGitHubConnection :execrows
DELETE FROM github_connections WHERE id = $1;

-- name: CountGitHubConnectionLinks :one
SELECT count(*) FROM github_sync_links WHERE connection_id = $1;

-- name: UpsertGitHubSyncLink :one
INSERT INTO github_sync_links (id, repo_id, connection_id, mode, remote_owner, remote_name, prune, trust_github,
    create_missing, interval_seconds, enabled, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (repo_id) DO UPDATE
SET connection_id = EXCLUDED.connection_id, mode = EXCLUDED.mode, remote_owner = EXCLUDED.remote_owner,
    remote_name = EXCLUDED.remote_name, prune = EXCLUDED.prune, trust_github = EXCLUDED.trust_github,
    create_missing = EXCLUDED.create_missing, interval_seconds = EXCLUDED.interval_seconds,
    enabled = EXCLUDED.enabled, status = 'pending', last_error = '', failures = 0, next_attempt_at = NULL,
    updated_at = now()
RETURNING *;

-- name: GetGitHubSyncLinkByRepo :one
SELECT * FROM github_sync_links WHERE repo_id = $1;

-- name: GetGitHubSyncLink :one
SELECT * FROM github_sync_links WHERE id = $1;

-- name: DeleteGitHubSyncLinkByRepo :execrows
DELETE FROM github_sync_links WHERE repo_id = $1;

-- name: ResetGitHubSyncRefsOnModeChange :exec
-- A new remote or mode makes the stored per-ref state meaningless.
DELETE FROM github_sync_refs WHERE link_id = $1;

-- name: ListGitHubSyncLinksByRemote :many
-- Links of a connection that follow one GitHub repository (webhooks).
SELECT * FROM github_sync_links
WHERE connection_id = $1 AND lower(remote_owner) = lower(sqlc.arg(remote_owner)::text)
  AND lower(remote_name) = lower(sqlc.arg(remote_name)::text) AND enabled;

-- name: ListDueGitHubSyncLinks :many
-- Enabled links whose scheduled run is due and not held by a backoff.
SELECT l.* FROM github_sync_links l
JOIN repos r ON r.id = l.repo_id
WHERE l.enabled AND r.storage_state = 'hot'
  AND (l.last_run_at IS NULL OR l.last_run_at <= now() - make_interval(secs => l.interval_seconds))
  AND (l.next_attempt_at IS NULL OR l.next_attempt_at <= now())
ORDER BY l.last_run_at NULLS FIRST
LIMIT $1;

-- name: ListGitHubBackupLinks :many
-- Every enabled backup or sync link of a hot repository (nightly check).
SELECT l.* FROM github_sync_links l
JOIN repos r ON r.id = l.repo_id
WHERE l.enabled AND l.mode IN ('backup', 'sync') AND r.storage_state = 'hot'
ORDER BY l.id
LIMIT $1 OFFSET $2;

-- name: GetGitHubSyncModeByRepo :one
-- The mode of the enabled link of a repository (hook path).
SELECT mode FROM github_sync_links WHERE repo_id = $1 AND enabled;

-- name: FinishGitHubSyncRun :exec
UPDATE github_sync_links
SET status = $2, last_error = $3, failures = $4, next_attempt_at = $5, last_run_at = now(),
    last_success_at = CASE WHEN $3::text = '' THEN now() ELSE last_success_at END,
    lfs_skipped = $6, updated_at = now()
WHERE id = $1;

-- name: ListGitHubSyncRefs :many
SELECT * FROM github_sync_refs WHERE link_id = $1 ORDER BY ref;

-- name: GetGitHubSyncRef :one
SELECT * FROM github_sync_refs WHERE link_id = $1 AND ref = $2;

-- name: UpsertGitHubSyncRef :exec
INSERT INTO github_sync_refs (link_id, ref, local_sha, remote_sha, status, last_error, retries, conflict_sha, resolution)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (link_id, ref) DO UPDATE
SET local_sha = EXCLUDED.local_sha, remote_sha = EXCLUDED.remote_sha, status = EXCLUDED.status,
    last_error = EXCLUDED.last_error, retries = EXCLUDED.retries, conflict_sha = EXCLUDED.conflict_sha,
    resolution = EXCLUDED.resolution, updated_at = now();

-- name: DeleteGitHubSyncRef :exec
DELETE FROM github_sync_refs WHERE link_id = $1 AND ref = $2;

-- name: RepoHasLFSObjects :one
SELECT EXISTS (SELECT 1 FROM lfs_objects WHERE repo_id = $1);

-- name: InsertGitHubImport :one
INSERT INTO github_imports (id, repo_id, connection_id, remote_owner, remote_name, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetGitHubImportByRepo :one
SELECT * FROM github_imports WHERE repo_id = $1;

-- name: GetGitHubImport :one
SELECT * FROM github_imports WHERE id = $1;

-- name: SetGitHubImportStatus :exec
UPDATE github_imports
SET status = $2, last_error = $3,
    started_at = CASE WHEN $2::text = 'running' THEN now() ELSE started_at END,
    finished_at = CASE WHEN $2::text IN ('done', 'error') THEN now() ELSE finished_at END
WHERE id = $1;
