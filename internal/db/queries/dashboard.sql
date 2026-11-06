-- Home dashboard and explore page. Every listing filters repositories
-- through repo_readable(repo, viewer, all_repos, internal): all_repos =
-- instance admin; internal = the viewer is signed in and not restricted.

-- name: ListUserIssues :many
-- Issues and pull requests across the readable repositories. filter is
-- assigned, created, mentioned or review_requested; kind '' (any), issue or
-- pull; state '' (any), open or closed.
SELECT i.id, i.number, i.kind, i.title, i.state, i.comment_count, i.created_at, i.updated_at,
       n.name AS owner_name, r.name AS repo_name,
       COALESCE(an.name, '')::text AS author,
       (pr.merged_at IS NOT NULL)::boolean AS merged,
       COALESCE((SELECT jsonb_agg(jsonb_build_object('name', l.name, 'color', l.color) ORDER BY lower(l.name))
                 FROM issue_labels il JOIN labels l ON l.id = il.label_id
                 WHERE il.issue_id = i.id), '[]'::jsonb)::jsonb AS labels
FROM issues i
JOIN repos r ON r.id = i.repo_id
JOIN namespaces n ON n.id = r.owner_id
LEFT JOIN namespaces an ON an.id = i.author_id
LEFT JOIN pull_requests pr ON pr.issue_id = i.id
WHERE repo_readable(r.id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
  AND (sqlc.arg(kind)::text = '' OR i.kind = sqlc.arg(kind)::text)
  AND (sqlc.arg(state)::text = '' OR i.state = sqlc.arg(state)::text)
  AND (CASE sqlc.arg(filter)::text
         WHEN 'created' THEN i.author_id = sqlc.arg(viewer)::uuid
         WHEN 'mentioned' THEN EXISTS (SELECT 1 FROM issue_references x
                WHERE x.source_issue_id = i.id AND x.target_kind = 'user' AND x.target_user_id = sqlc.arg(viewer)::uuid)
         WHEN 'review_requested' THEN EXISTS (SELECT 1 FROM pull_review_requests rq
                WHERE rq.pull_id = i.id AND rq.reviewer_id = sqlc.arg(viewer)::uuid)
         ELSE EXISTS (SELECT 1 FROM issue_assignees a WHERE a.issue_id = i.id AND a.user_id = sqlc.arg(viewer)::uuid)
       END)
ORDER BY i.updated_at DESC, i.id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountUserIssues :one
SELECT count(*)
FROM issues i
JOIN repos r ON r.id = i.repo_id
WHERE repo_readable(r.id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
  AND (sqlc.arg(kind)::text = '' OR i.kind = sqlc.arg(kind)::text)
  AND (sqlc.arg(state)::text = '' OR i.state = sqlc.arg(state)::text)
  AND (CASE sqlc.arg(filter)::text
         WHEN 'created' THEN i.author_id = sqlc.arg(viewer)::uuid
         WHEN 'mentioned' THEN EXISTS (SELECT 1 FROM issue_references x
                WHERE x.source_issue_id = i.id AND x.target_kind = 'user' AND x.target_user_id = sqlc.arg(viewer)::uuid)
         WHEN 'review_requested' THEN EXISTS (SELECT 1 FROM pull_review_requests rq
                WHERE rq.pull_id = i.id AND rq.reviewer_id = sqlc.arg(viewer)::uuid)
         ELSE EXISTS (SELECT 1 FROM issue_assignees a WHERE a.issue_id = i.id AND a.user_id = sqlc.arg(viewer)::uuid)
       END);

-- name: ListUserActionsRuns :many
-- Recent workflow runs across the readable repositories. status matches the
-- run status or its conclusion ('' = any).
SELECT sqlc.embed(a), n.name AS owner_name, r.name AS repo_name
FROM actions_runs a
JOIN repos r ON r.id = a.repo_id
JOIN namespaces n ON n.id = r.owner_id
WHERE repo_readable(r.id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
  AND (sqlc.arg(status)::text = '' OR a.status = sqlc.arg(status)::text OR a.conclusion = sqlc.arg(status)::text)
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountUserActionsRuns :one
SELECT count(*)
FROM actions_runs a
WHERE repo_readable(a.repo_id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
  AND (sqlc.arg(status)::text = '' OR a.status = sqlc.arg(status)::text OR a.conclusion = sqlc.arg(status)::text);

-- name: InsertActivityEvent :exec
INSERT INTO activity_events (id, repo_id, actor_id, kind, action, number, title, created_at)
VALUES (@id, @repo_id, @actor_id, @kind, @action, @number, @title, @created_at)
ON CONFLICT (id) DO NOTHING;

-- name: ListUserActivity :many
-- Newest first events of the readable repositories.
SELECT e.id, e.kind, e.action, e.number, e.title, e.created_at,
       n.name AS owner_name, r.name AS repo_name,
       COALESCE(an.name, '')::text AS actor
FROM activity_events e
JOIN repos r ON r.id = e.repo_id
JOIN namespaces n ON n.id = r.owner_id
LEFT JOIN namespaces an ON an.id = e.actor_id
WHERE repo_readable(r.id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
ORDER BY e.created_at DESC, e.id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: ExploreRepos :many
-- Public repositories, optionally filtered by a lower-cased name or
-- description fragment (LIKE metacharacters escaped). sort is updated or name.
SELECT sqlc.embed(r), n.name AS owner_name, n.kind AS owner_kind
FROM repos r
JOIN namespaces n ON n.id = r.owner_id
WHERE r.visibility = 'public'
  AND (sqlc.arg(pattern)::text = ''
       OR r.name_lower LIKE '%' || sqlc.arg(pattern)::text || '%' ESCAPE '\'
       OR lower(r.description) LIKE '%' || sqlc.arg(pattern)::text || '%' ESCAPE '\')
ORDER BY
  CASE WHEN sqlc.arg(sort)::text = 'name' THEN n.name_lower || '/' || r.name_lower END ASC,
  r.updated_at DESC, r.id
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountExploreRepos :one
SELECT count(*)
FROM repos r
WHERE r.visibility = 'public'
  AND (sqlc.arg(pattern)::text = ''
       OR r.name_lower LIKE '%' || sqlc.arg(pattern)::text || '%' ESCAPE '\'
       OR lower(r.description) LIKE '%' || sqlc.arg(pattern)::text || '%' ESCAPE '\');

-- name: ListUserPins :many
-- Pinned repositories the user can still read, in order.
SELECT sqlc.embed(r), n.name AS owner_name, n.kind AS owner_kind
FROM user_pinned_repos p
JOIN repos r ON r.id = p.repo_id
JOIN namespaces n ON n.id = r.owner_id
WHERE p.user_id = sqlc.arg(user_id)::uuid
  AND repo_readable(r.id, sqlc.arg(user_id)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
ORDER BY p.position, p.created_at;

-- name: CountUserPins :one
SELECT count(*) FROM user_pinned_repos WHERE user_id = $1;

-- name: PinRepo :exec
-- Appends the repository after the last pin; a repeated pin changes nothing.
INSERT INTO user_pinned_repos (user_id, repo_id, position)
VALUES (@user_id, @repo_id, COALESCE((SELECT max(position) + 1 FROM user_pinned_repos WHERE user_id = @user_id), 0))
ON CONFLICT (user_id, repo_id) DO NOTHING;

-- name: UnpinRepo :exec
DELETE FROM user_pinned_repos WHERE user_id = $1 AND repo_id = $2;

-- name: GetUserPreferences :one
SELECT * FROM user_preferences WHERE user_id = $1;

-- name: UpsertUserPreferences :one
INSERT INTO user_preferences (user_id, theme, layout)
VALUES (@user_id, @theme, @layout)
ON CONFLICT (user_id) DO UPDATE SET theme = EXCLUDED.theme, layout = EXCLUDED.layout, updated_at = now()
RETURNING *;

-- name: LockUserPins :exec
-- Serializes the pin limit check of one user within a transaction.
SELECT pg_advisory_xact_lock(hashtext('pins:' || sqlc.arg(user_key)::text));
