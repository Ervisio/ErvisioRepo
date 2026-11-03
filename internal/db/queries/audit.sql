-- name: InsertAuditEvent :exec
INSERT INTO audit_log (id, actor_id, action, target_type, target_id, ip, data, org_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListAuditEvents :many
-- Newest first; pass the last id of a page as before to get the next one.
-- action matches exactly, or a whole prefix when it ends with ".".
SELECT a.id, a.at, a.actor_id, actor.name AS actor_name, a.action, a.target_type, a.target_id, a.ip, a.data, a.org_id
FROM audit_log a
LEFT JOIN namespaces actor ON actor.id = a.actor_id
WHERE (sqlc.narg(before)::uuid IS NULL OR a.id < sqlc.narg(before)::uuid)
  AND (sqlc.narg(actor)::uuid IS NULL OR a.actor_id = sqlc.narg(actor)::uuid)
  AND (sqlc.narg(org)::uuid IS NULL OR a.org_id = sqlc.narg(org)::uuid)
  AND (sqlc.narg(action)::text IS NULL OR a.action = sqlc.narg(action)::text
       OR (right(sqlc.narg(action)::text, 1) = '.' AND starts_with(a.action, sqlc.narg(action)::text)))
ORDER BY a.id DESC
LIMIT sqlc.arg(max_rows);
