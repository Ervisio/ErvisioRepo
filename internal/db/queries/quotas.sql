-- Quotas per user and organization, and their usage counters (PLAN §8.2).

-- name: GetEffectiveQuota :one
-- Limits of a namespace (override, else instance default; -1 = unlimited)
-- and its current usage.
SELECT n.id, n.kind,
       COALESCE(q.max_repos, d.max_repos, -1)::integer AS max_repos,
       COALESCE(q.max_storage_bytes, d.max_storage_bytes, -1)::bigint AS max_storage_bytes,
       COALESCE(u.repos, 0)::integer AS repos,
       COALESCE(u.storage_bytes, 0)::bigint AS storage_bytes
FROM namespaces n
LEFT JOIN quota_defaults d ON d.kind = n.kind
LEFT JOIN quotas q ON q.namespace_id = n.id
LEFT JOIN usage_counters u ON u.namespace_id = n.id
WHERE n.id = $1;

-- name: GetQuotaDefaults :many
SELECT kind, max_repos, max_storage_bytes, updated_at FROM quota_defaults ORDER BY kind;

-- name: SetQuotaDefault :execrows
UPDATE quota_defaults SET max_repos = $2, max_storage_bytes = $3, updated_at = now() WHERE kind = $1;

-- name: GetQuotaOverride :one
SELECT namespace_id, max_repos, max_storage_bytes, updated_at FROM quotas WHERE namespace_id = $1;

-- name: SetQuotaOverride :exec
INSERT INTO quotas (namespace_id, max_repos, max_storage_bytes, updated_at) VALUES ($1, $2, $3, now())
ON CONFLICT (namespace_id) DO UPDATE
SET max_repos = EXCLUDED.max_repos, max_storage_bytes = EXCLUDED.max_storage_bytes, updated_at = now();

-- name: DeleteQuotaOverride :execrows
DELETE FROM quotas WHERE namespace_id = $1;

-- name: EnsureUsageCounter :exec
INSERT INTO usage_counters (namespace_id) VALUES ($1) ON CONFLICT (namespace_id) DO NOTHING;

-- name: ReserveUsage :execrows
-- Adds the deltas to the usage of a namespace only if the result stays
-- within its effective limits. Negative deltas (releases) always apply and
-- never go below zero. Zero rows means the quota would be exceeded; call
-- EnsureUsageCounter first.
UPDATE usage_counters c
SET repos = GREATEST(c.repos + sqlc.arg(repos)::integer, 0),
    storage_bytes = GREATEST(c.storage_bytes + sqlc.arg(bytes)::bigint, 0),
    updated_at = now()
FROM namespaces n
LEFT JOIN quota_defaults d ON d.kind = n.kind
LEFT JOIN quotas q ON q.namespace_id = n.id
WHERE c.namespace_id = sqlc.arg(namespace_id)::uuid AND n.id = c.namespace_id
  AND (sqlc.arg(repos)::integer <= 0 OR COALESCE(q.max_repos, d.max_repos) IS NULL
       OR c.repos + sqlc.arg(repos)::integer <= COALESCE(q.max_repos, d.max_repos))
  AND (sqlc.arg(bytes)::bigint <= 0 OR COALESCE(q.max_storage_bytes, d.max_storage_bytes) IS NULL
       OR c.storage_bytes + sqlc.arg(bytes)::bigint <= COALESCE(q.max_storage_bytes, d.max_storage_bytes));
