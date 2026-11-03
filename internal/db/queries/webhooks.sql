-- Outgoing webhooks (internal/webhooks).

-- name: InsertWebhook :one
INSERT INTO webhooks (id, repo_id, org_id, url, content_type, format, secret, events, active, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetWebhook :one
SELECT * FROM webhooks WHERE id = $1;

-- name: ListRepoWebhooks :many
SELECT * FROM webhooks WHERE repo_id = $1 ORDER BY created_at, id;

-- name: ListOrgWebhooks :many
SELECT * FROM webhooks WHERE org_id = $1 ORDER BY created_at, id;

-- name: UpdateWebhook :one
UPDATE webhooks
SET url = $2, content_type = $3, format = $4, secret = $5, events = $6, active = $7, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteWebhook :execrows
DELETE FROM webhooks WHERE id = $1;

-- name: CountRepoWebhooks :one
SELECT count(*) FROM webhooks WHERE repo_id = $1;

-- name: CountOrgWebhooks :one
SELECT count(*) FROM webhooks WHERE org_id = $1;

-- name: ActiveWebhooksForRepo :many
-- Active hooks of a repository and of the organization that owns it.
SELECT w.* FROM webhooks w
WHERE w.active AND (w.repo_id = @repo_id
    OR w.org_id = (SELECT r.owner_id FROM repos r WHERE r.id = @repo_id))
ORDER BY w.created_at, w.id;

-- name: InsertWebhookDelivery :one
-- Records a delivery; for a bus event the conflict makes it idempotent and
-- no row is returned.
INSERT INTO webhook_deliveries (id, webhook_id, event_id, guid, event, action, redelivery, payload)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (webhook_id, event_id) WHERE event_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: GetWebhookDelivery :one
SELECT * FROM webhook_deliveries WHERE id = $1;

-- name: ListWebhookDeliveries :many
SELECT id, webhook_id, event_id, guid, event, action, redelivery, status, attempts, url, status_code,
    duration_ms, error, created_at, delivered_at
FROM webhook_deliveries
WHERE webhook_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: RecordWebhookAttempt :exec
UPDATE webhook_deliveries
SET status = $2, attempts = attempts + 1, url = $3, request_headers = $4, status_code = $5,
    response_headers = $6, response_body = $7, duration_ms = $8, error = $9, delivered_at = now()
WHERE id = $1;

-- name: LastWebhookDelivery :one
SELECT status, status_code, delivered_at FROM webhook_deliveries
WHERE webhook_id = $1 AND delivered_at IS NOT NULL
ORDER BY delivered_at DESC
LIMIT 1;

-- name: PruneWebhookDeliveries :execrows
DELETE FROM webhook_deliveries WHERE created_at < $1;

-- name: WebhookOrgRole :one
-- The organization named name and the role of user in it ('' when not a
-- member).
SELECT n.id, n.name,
    coalesce((SELECT m.role FROM org_members m WHERE m.org_id = n.id AND m.user_id = @user_id), '')::text AS role
FROM namespaces n
JOIN orgs o ON o.id = n.id
WHERE n.name_lower = lower(@name::text);

-- name: WebhookNamespace :one
SELECT id, name, kind FROM namespaces WHERE id = $1;

-- name: WebhookUser :one
SELECT n.id, n.name, u.display_name, u.avatar_url, u.role FROM namespaces n JOIN users u ON u.id = n.id
WHERE n.id = $1;

-- name: WebhookIssue :one
SELECT * FROM issues WHERE id = $1;

-- name: WebhookIssueByNumber :one
SELECT * FROM issues WHERE repo_id = $1 AND number = $2;

-- name: WebhookIssueComment :one
SELECT * FROM issue_comments WHERE id = $1;

-- name: WebhookIssueAssignees :many
SELECT user_id FROM issue_assignees WHERE issue_id = $1;

-- name: WebhookIssueLabels :many
SELECT l.name, l.color, l.description FROM labels l
JOIN issue_labels il ON il.label_id = l.id
WHERE il.issue_id = $1
ORDER BY lower(l.name);

-- name: WebhookRelease :one
SELECT * FROM releases WHERE id = $1;

-- name: WebhookLabel :one
SELECT * FROM labels WHERE id = $1;

-- name: WebhookMilestone :one
SELECT * FROM milestones WHERE id = $1;
