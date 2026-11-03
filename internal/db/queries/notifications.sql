-- Notifications (internal/notify).

-- name: NotifyIssueParticipants :many
-- Users taking part in an issue or pull request, with why: author,
-- assigned, comment or mentioned (one row per user and why).
SELECT i.author_id::uuid AS user_id, 'author'::text AS why FROM issues i
WHERE i.id = @issue_id AND i.author_id IS NOT NULL
UNION
SELECT a.user_id, 'assigned'::text FROM issue_assignees a WHERE a.issue_id = @issue_id
UNION
SELECT c.author_id::uuid, 'comment'::text FROM issue_comments c
WHERE c.issue_id = @issue_id AND c.author_id IS NOT NULL
UNION
SELECT r.target_user_id::uuid, 'mentioned'::text FROM issue_references r
WHERE r.source_issue_id = @issue_id AND r.target_kind = 'user' AND r.target_user_id IS NOT NULL;

-- name: NotifyMentions :many
-- Users mentioned by one source (issue:<id> or comment:<id>).
SELECT target_user_id::uuid AS user_id FROM issue_references
WHERE source_key = $1 AND target_kind = 'user' AND target_user_id IS NOT NULL;

-- name: NotifyRepoWatchers :many
SELECT user_id FROM repo_subscriptions WHERE repo_id = $1 AND level = 'watching';

-- name: NotifyRepoLevels :many
SELECT user_id, level FROM repo_subscriptions WHERE repo_id = @repo_id AND user_id = ANY(@user_ids::uuid[]);

-- name: NotifySubjectSubscriptions :many
SELECT user_id, subscribed FROM thread_subscriptions WHERE subject_key = $1;

-- name: NotifyUsers :many
-- Active recipients with their preferences and primary verified address.
SELECT u.id, n.name, u.role, u.locale, u.email_notifications,
    coalesce((SELECT e.address FROM emails e WHERE e.user_id = u.id AND e.is_primary AND e.verified_at IS NOT NULL), '')::text AS email
FROM users u
JOIN namespaces n ON n.id = u.id
WHERE u.id = ANY(@ids::uuid[]) AND u.suspended_at IS NULL;

-- name: NotifyUserName :one
SELECT name FROM namespaces WHERE id = $1;

-- name: UpsertNotificationThread :one
-- Creates or refreshes the thread of a user for a subject. A repeated
-- delivery of the same event changes nothing and returns no row.
INSERT INTO notification_threads (id, user_id, repo_id, subject_type, subject_key, number, title, reason, summary,
    last_event_id, last_actor_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (user_id, subject_key) DO UPDATE
SET title = EXCLUDED.title, reason = EXCLUDED.reason, summary = EXCLUDED.summary, unread = true, done = false,
    last_event_id = EXCLUDED.last_event_id, last_actor_id = EXCLUDED.last_actor_id, updated_at = now()
WHERE notification_threads.last_event_id IS DISTINCT FROM EXCLUDED.last_event_id
RETURNING id;

-- name: ListNotificationThreads :many
SELECT sqlc.embed(t), n.name AS owner_name, r.name AS repo_name
FROM notification_threads t
JOIN repos r ON r.id = t.repo_id
JOIN namespaces n ON n.id = r.owner_id
WHERE t.user_id = @user_id AND NOT t.done
    AND (@include_read::boolean OR t.unread)
    AND (NOT @participating::boolean OR t.reason <> 'subscribed')
    AND (sqlc.narg('repo_id')::uuid IS NULL OR t.repo_id = sqlc.narg('repo_id')::uuid)
    AND (sqlc.narg('since')::timestamptz IS NULL OR t.updated_at >= sqlc.narg('since')::timestamptz)
ORDER BY t.updated_at DESC, t.id DESC
LIMIT @lim OFFSET @off;

-- name: GetNotificationThread :one
SELECT sqlc.embed(t), n.name AS owner_name, r.name AS repo_name
FROM notification_threads t
JOIN repos r ON r.id = t.repo_id
JOIN namespaces n ON n.id = r.owner_id
WHERE t.id = $1 AND t.user_id = $2;

-- name: CountUnreadNotifications :one
SELECT count(*) FROM notification_threads WHERE user_id = $1 AND unread AND NOT done;

-- name: MarkNotificationThreadRead :execrows
UPDATE notification_threads SET unread = false, last_read_at = now()
WHERE id = $1 AND user_id = $2;

-- name: MarkNotificationThreadDone :execrows
UPDATE notification_threads SET unread = false, done = true, last_read_at = now()
WHERE id = $1 AND user_id = $2;

-- name: MarkNotificationsRead :execrows
UPDATE notification_threads SET unread = false, last_read_at = now()
WHERE user_id = @user_id AND unread AND updated_at <= @before
    AND (sqlc.narg('repo_id')::uuid IS NULL OR repo_id = sqlc.narg('repo_id')::uuid);

-- name: GetThreadSubscription :one
SELECT * FROM thread_subscriptions WHERE user_id = $1 AND subject_key = $2;

-- name: UpsertThreadSubscription :one
INSERT INTO thread_subscriptions (user_id, repo_id, subject_key, subscribed)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, subject_key) DO UPDATE SET subscribed = EXCLUDED.subscribed, created_at = now()
RETURNING *;

-- name: DeleteThreadSubscription :exec
DELETE FROM thread_subscriptions WHERE user_id = $1 AND subject_key = $2;

-- name: GetRepoSubscription :one
SELECT * FROM repo_subscriptions WHERE user_id = $1 AND repo_id = $2;

-- name: UpsertRepoSubscription :one
INSERT INTO repo_subscriptions (user_id, repo_id, level)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, repo_id) DO UPDATE SET level = EXCLUDED.level, updated_at = now()
RETURNING *;

-- name: DeleteRepoSubscription :exec
DELETE FROM repo_subscriptions WHERE user_id = $1 AND repo_id = $2;

-- name: GetNotificationForMail :one
SELECT sqlc.embed(t), n.name AS owner_name, r.name AS repo_name
FROM notification_threads t
JOIN repos r ON r.id = t.repo_id
JOIN namespaces n ON n.id = r.owner_id
WHERE t.id = $1;

-- name: MarkNotificationEmailed :exec
UPDATE notification_threads SET emailed_at = now() WHERE id = $1;

-- name: GetEmailNotifications :one
SELECT email_notifications FROM users WHERE id = $1;

-- name: SetEmailNotifications :exec
UPDATE users SET email_notifications = $2 WHERE id = $1;
