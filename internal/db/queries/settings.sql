-- name: GetSetting :one
SELECT key, value, updated_at FROM settings WHERE key = $1;

-- name: UpsertSetting :exec
INSERT INTO settings (key, value, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();

-- name: InsertSettingIfAbsent :exec
INSERT INTO settings (key, value, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (key) DO NOTHING;

-- name: InstanceCounts :one
-- Totals shown by the Ervisio plugin's status page.
SELECT (SELECT count(*) FROM repos)::bigint AS repos,
       (SELECT count(*) FROM users)::bigint AS users,
       (SELECT count(*) FROM orgs)::bigint  AS orgs;
