-- name: InsertOutboxEvent :exec
INSERT INTO events_outbox (id, kind, action, repo_id, actor_id, subject_id, payload)
VALUES (@id, @kind, @action, @repo_id, @actor_id, @subject_id, @payload);

-- name: ClaimOutboxEvents :many
-- Undispatched events, oldest first, locked for the dispatching transaction
-- (concurrent dispatchers skip each other's rows).
SELECT id, kind, action, repo_id, actor_id, subject_id, payload, created_at
FROM events_outbox
WHERE dispatched_at IS NULL
ORDER BY created_at, id
LIMIT @lim
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxDispatched :exec
UPDATE events_outbox SET dispatched_at = now() WHERE id = ANY(@ids::uuid[]);

-- name: GetOutboxEvent :one
SELECT id, kind, action, repo_id, actor_id, subject_id, payload, created_at
FROM events_outbox WHERE id = @id;

-- name: PruneOutboxEvents :execrows
-- Dispatched events older than "before" (their delivery jobs are done or
-- have given up).
DELETE FROM events_outbox WHERE dispatched_at IS NOT NULL AND dispatched_at < @before::timestamptz;
