-- name: ListInstanceSettings :many
SELECT key, value, updated_by, updated_at FROM instance_settings ORDER BY key;

-- name: UpsertInstanceSetting :exec
INSERT INTO instance_settings (key, value, updated_by, updated_at)
VALUES (@key, @value, @updated_by, now())
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now();

-- name: DeleteInstanceSettings :execrows
DELETE FROM instance_settings WHERE key = ANY(@keys::text[]);
