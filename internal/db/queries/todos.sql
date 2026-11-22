-- name: GetSharedTodo :one
SELECT revision, categories, items FROM shared_todos
WHERE repo_id = sqlc.narg(repo_id)::uuid OR org_id = sqlc.narg(org_id)::uuid;

-- name: SaveSharedTodo :one
WITH replaced AS (
    UPDATE shared_todos
    SET revision = revision + 1, categories = sqlc.arg(categories), items = sqlc.arg(items)
    WHERE (repo_id = sqlc.narg(repo_id)::uuid OR org_id = sqlc.narg(org_id)::uuid)
      AND revision = sqlc.arg(expected_revision)::bigint
    RETURNING revision
), created AS (
    INSERT INTO shared_todos (repo_id, org_id, revision, categories, items)
    SELECT sqlc.narg(repo_id)::uuid, sqlc.narg(org_id)::uuid, 1, sqlc.arg(categories)::jsonb, sqlc.arg(items)::jsonb
    WHERE sqlc.arg(expected_revision)::bigint = 0
    ON CONFLICT (repo_id, org_id) DO NOTHING
    RETURNING revision
)
SELECT revision FROM replaced
UNION ALL
SELECT revision FROM created;

-- name: GetSharedTodoPreference :one
SELECT collapsed FROM shared_todo_preferences
WHERE (repo_id = sqlc.narg(repo_id)::uuid OR org_id = sqlc.narg(org_id)::uuid)
  AND user_id = sqlc.arg(user_id);

-- name: SaveSharedTodoPreference :exec
INSERT INTO shared_todo_preferences (repo_id, org_id, user_id, collapsed)
VALUES (sqlc.narg(repo_id), sqlc.narg(org_id), sqlc.arg(user_id), sqlc.arg(collapsed))
ON CONFLICT (repo_id, org_id, user_id) DO UPDATE SET collapsed = EXCLUDED.collapsed;
