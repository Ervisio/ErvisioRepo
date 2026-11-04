-- name: GetActionsResultsJob :one
-- The job a runtime token was issued for, with what cache scoping needs.
SELECT j.id AS job_id, j.run_id, j.repo_id, j.status AS job_status, r.ref, r.event,
       coalesce(r.event_payload -> 'pull_request' -> 'base' ->> 'ref', '')::text AS base_ref,
       p.default_branch
FROM actions_jobs j
JOIN actions_runs r ON r.id = j.run_id
JOIN repos p ON p.id = j.repo_id
WHERE j.id = @job_id AND j.run_id = @run_id AND j.repo_id = @repo_id;

-- ---- cache ----

-- name: ReserveActionsCache :one
-- Reserves a cache entry. An existing entry is taken over only when it is
-- an upload abandoned before stale_before; otherwise no row comes back.
INSERT INTO actions_caches (repo_id, scope, key, version, run_id)
VALUES (@repo_id, @scope, @key, @version, @run_id)
ON CONFLICT (repo_id, scope, key, version) DO UPDATE
SET run_id = EXCLUDED.run_id, created_at = now(), last_used_at = now(), size = 0, storage_key = ''
WHERE NOT actions_caches.complete AND actions_caches.created_at < @stale_before
RETURNING *;

-- name: GetActionsCache :one
SELECT * FROM actions_caches WHERE id = @id;

-- name: GetActionsCacheByKey :one
SELECT * FROM actions_caches
WHERE repo_id = @repo_id AND scope = @scope AND key = @key AND version = @version;

-- name: CompleteActionsCache :one
UPDATE actions_caches
SET complete = true, size = @size, storage_key = @storage_key, completed_at = now(), last_used_at = now()
WHERE id = @id AND NOT complete
RETURNING *;

-- name: FindActionsCache :one
-- The newest complete entry of a scope whose key matches pattern (LIKE,
-- already escaped by the caller).
SELECT * FROM actions_caches
WHERE repo_id = @repo_id AND scope = @scope AND version = @version AND complete AND key LIKE @pattern
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: TouchActionsCache :exec
UPDATE actions_caches SET last_used_at = now() WHERE id = @id;

-- name: ListActionsCacheReposOver :many
-- Repositories whose complete cache entries exceed max_size bytes.
SELECT repo_id, sum(size)::bigint AS total FROM actions_caches
WHERE complete
GROUP BY repo_id
HAVING sum(size) > @max_size::bigint;

-- name: ListActionsCachesLRU :many
SELECT * FROM actions_caches
WHERE repo_id = @repo_id AND complete
ORDER BY last_used_at, id
LIMIT @lim;

-- name: ListExpiredActionsCaches :many
-- Complete entries unused since used_before and uploads abandoned before
-- stale_before.
SELECT * FROM actions_caches
WHERE (complete AND last_used_at < @used_before) OR (NOT complete AND created_at < @stale_before)
ORDER BY id
LIMIT @lim;

-- name: DeleteActionsCache :exec
DELETE FROM actions_caches WHERE id = @id;

-- ---- artifacts ----

-- name: CreateActionsArtifact :one
-- Creates an artifact, or restarts an upload not finalized yet. A
-- finalized artifact of the same name returns no row.
INSERT INTO actions_artifacts (repo_id, run_id, job_id, name, mime_type, expires_at)
VALUES (@repo_id, @run_id, @job_id, @name, @mime_type, @expires_at)
ON CONFLICT (run_id, name) DO UPDATE
SET job_id = EXCLUDED.job_id, mime_type = EXCLUDED.mime_type, expires_at = EXCLUDED.expires_at,
    size = 0, digest = '', storage_key = '', created_at = now()
WHERE NOT actions_artifacts.finalized
RETURNING *;

-- name: GetActionsArtifact :one
SELECT * FROM actions_artifacts WHERE id = @id;

-- name: GetActionsArtifactByName :one
SELECT * FROM actions_artifacts WHERE run_id = @run_id AND name = @name;

-- name: SetActionsArtifactBlob :one
UPDATE actions_artifacts SET storage_key = @storage_key, size = @size, digest = @digest
WHERE id = @id AND NOT finalized
RETURNING *;

-- name: FinalizeActionsArtifact :one
UPDATE actions_artifacts SET finalized = true, finalized_at = now()
WHERE id = @id AND NOT finalized AND storage_key <> ''
RETURNING *;

-- name: ListActionsArtifacts :many
SELECT * FROM actions_artifacts
WHERE run_id = @run_id AND finalized AND expires_at > now()
  AND (sqlc.narg('name')::text IS NULL OR name = sqlc.narg('name'))
  AND (sqlc.narg('id')::bigint IS NULL OR id = sqlc.narg('id'))
ORDER BY id;

-- name: DeleteActionsArtifact :exec
DELETE FROM actions_artifacts WHERE id = @id;

-- name: ListExpiredActionsArtifacts :many
-- Expired artifacts and uploads not finalized before stale_before.
SELECT * FROM actions_artifacts
WHERE expires_at < now() OR (NOT finalized AND created_at < @stale_before)
ORDER BY id
LIMIT @lim;

-- ---- upload parts ----

-- name: UpsertActionsUploadPart :exec
INSERT INTO actions_upload_parts (owner, block, size, storage_key)
VALUES (@owner, @block, @size, @storage_key)
ON CONFLICT (owner, block) DO UPDATE SET size = EXCLUDED.size, storage_key = EXCLUDED.storage_key,
    created_at = now();

-- name: ListActionsUploadParts :many
SELECT * FROM actions_upload_parts WHERE owner = @owner ORDER BY seq;

-- name: DeleteActionsUploadParts :exec
DELETE FROM actions_upload_parts WHERE owner = @owner;

-- name: ListStaleActionsUploadOwners :many
SELECT DISTINCT owner FROM actions_upload_parts WHERE created_at < @before LIMIT @lim;
