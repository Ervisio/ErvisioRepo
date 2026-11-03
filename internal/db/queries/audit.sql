-- name: InsertAuditEvent :exec
INSERT INTO audit_log (id, actor_id, action, target_type, target_id, ip, data)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAuditEvents :many
-- Newest first; pass the last id of a page as before to get the next one.
SELECT id, at, actor_id, action, target_type, target_id, ip, data
FROM audit_log
WHERE (sqlc.narg(before)::uuid IS NULL OR id < sqlc.narg(before)::uuid)
  AND (sqlc.narg(actor)::uuid IS NULL OR actor_id = sqlc.narg(actor)::uuid)
ORDER BY id DESC
LIMIT sqlc.arg(max_rows);
