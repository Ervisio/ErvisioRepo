-- name: GetStorageObject :one
SELECT * FROM storage_objects WHERE target = $1 AND key = $2;

-- name: UpsertStorageObject :one
-- Called only after the object is confirmed on the primary. Re-uploading an
-- existing key with the same content keeps its replica and verification state.
INSERT INTO storage_objects (target, key, policy, size, sha256, replicas_ok, verified_at)
VALUES (@target, @key, @policy, @size, @sha256, @replicas_ok, now())
ON CONFLICT (target, key) DO UPDATE SET
    policy      = EXCLUDED.policy,
    size        = EXCLUDED.size,
    sha256      = EXCLUDED.sha256,
    replicas_ok = CASE WHEN storage_objects.sha256 = EXCLUDED.sha256 AND storage_objects.size = EXCLUDED.size
                       THEN storage_objects.replicas_ok OR EXCLUDED.replicas_ok
                       ELSE EXCLUDED.replicas_ok END,
    verified_at = now(),
    last_error  = NULL,
    updated_at  = now()
RETURNING *;

-- name: DeleteStorageObject :exec
DELETE FROM storage_objects WHERE target = $1 AND key = $2;

-- name: MarkStorageObjectReplicated :exec
UPDATE storage_objects
SET replicas_ok = true, last_error = NULL, updated_at = now()
WHERE target = @target AND key = @key AND sha256 = @sha256;

-- name: MarkStorageObjectVerified :exec
UPDATE storage_objects
SET verified_at = now(), last_error = NULL, updated_at = now()
WHERE target = @target AND key = @key AND sha256 = @sha256;

-- name: SetStorageObjectError :exec
UPDATE storage_objects
SET last_error = @last_error, updated_at = now()
WHERE target = @target AND key = @key;

-- name: SetStorageObjectReplicasPending :exec
UPDATE storage_objects
SET replicas_ok = false, last_error = @last_error, updated_at = now()
WHERE target = @target AND key = @key;

-- name: ListStorageObjectsPendingReplicas :many
SELECT * FROM storage_objects
WHERE NOT replicas_ok AND created_at < @created_before
ORDER BY created_at
LIMIT @max_rows;

-- name: SampleStorageObjectsForVerify :many
-- Oldest-verified first, random within the same age, so repeated runs cover
-- every object over time.
SELECT * FROM storage_objects
WHERE verified_at IS NULL OR verified_at < @verified_before
ORDER BY verified_at NULLS FIRST, random()
LIMIT @max_rows;

-- name: ListStorageObjectsByKeys :many
SELECT * FROM storage_objects WHERE target = @target AND key = ANY(@keys::text[]);

-- name: StorageObjectStats :many
SELECT policy,
       count(*)::bigint                                AS objects,
       coalesce(sum(size), 0)::bigint                  AS bytes,
       count(*) FILTER (WHERE NOT replicas_ok)::bigint AS pending_replicas,
       count(*) FILTER (WHERE last_error IS NOT NULL)::bigint AS errors
FROM storage_objects
GROUP BY policy
ORDER BY policy;
