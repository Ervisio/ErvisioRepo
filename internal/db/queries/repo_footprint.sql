-- name: ListRepoStorageFootprints :many
-- Callers supply only repository IDs that they have already authorized.
WITH repo_runs AS (
    SELECT id, repo_id FROM actions_runs WHERE repo_id = ANY(sqlc.arg(repo_ids)::uuid[])
), jobs AS (
    SELECT j.id, j.repo_id, j.log_key
    FROM repo_runs r JOIN actions_jobs j ON j.run_id = r.id AND j.repo_id = r.repo_id
), payload AS (
    -- The LFS primary key is (repo_id, oid), even when storage is shared.
    SELECT repo_id, size FROM lfs_objects WHERE repo_id = ANY(sqlc.arg(repo_ids)::uuid[])
    UNION ALL
    SELECT repo_id, size FROM release_assets WHERE repo_id = ANY(sqlc.arg(repo_ids)::uuid[])
    UNION ALL
    SELECT repo_id, size FROM issue_attachments WHERE repo_id = ANY(sqlc.arg(repo_ids)::uuid[])
    UNION ALL
    SELECT repo_id, size FROM actions_caches
    WHERE repo_id = ANY(sqlc.arg(repo_ids)::uuid[]) AND complete
    UNION ALL
    SELECT a.repo_id, a.size FROM repo_runs r
    JOIN actions_artifacts a ON a.run_id = r.id AND a.repo_id = r.repo_id
    WHERE a.finalized AND a.expires_at > now()
    UNION ALL
    -- While a log is being archived the old rows can still exist; count only one form.
    SELECT j.repo_id, octet_length(l.content)::bigint AS size
    FROM jobs j JOIN actions_log_rows l ON l.job_id = j.id
    WHERE j.log_key = ''
    UNION ALL
    SELECT j.repo_id, o.size
    FROM jobs j
    CROSS JOIN LATERAL (
        -- Keep one logical copy even if a historical primary target also has a row.
        SELECT max(size)::bigint AS size FROM storage_objects so
        WHERE so.policy = 'actions' AND so.key = 'actions/' || j.log_key
    ) o
    WHERE j.log_key <> ''
), extra AS (
    SELECT repo_id, sum(size)::bigint AS bytes FROM payload GROUP BY repo_id
)
SELECT r.id, (r.size_bytes + COALESCE(e.bytes, 0))::bigint AS total_size_bytes
FROM repos r LEFT JOIN extra e ON e.repo_id = r.id
WHERE r.id = ANY(sqlc.arg(repo_ids)::uuid[]);
