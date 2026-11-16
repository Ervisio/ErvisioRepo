-- Repository stars. repos.stars_count follows repo_stars through a trigger.

-- name: StarRepo :exec
-- A repeated star changes nothing.
INSERT INTO repo_stars (user_id, repo_id) VALUES ($1, $2)
ON CONFLICT (user_id, repo_id) DO NOTHING;

-- name: UnstarRepo :exec
DELETE FROM repo_stars WHERE user_id = $1 AND repo_id = $2;

-- name: IsRepoStarred :one
SELECT EXISTS (SELECT 1 FROM repo_stars WHERE user_id = $1 AND repo_id = $2)::boolean AS starred;

-- name: CountRepoStars :one
SELECT stars_count FROM repos WHERE id = $1;

-- name: ListStarredAmong :many
-- The repositories of repo_ids the user starred.
SELECT repo_id FROM repo_stars
WHERE user_id = sqlc.arg(user_id)::uuid AND repo_id = ANY(sqlc.arg(repo_ids)::uuid[]);

-- name: ListStargazers :many
-- Users who starred a repository, latest first.
SELECT n.name AS username, u.display_name, u.avatar_url, s.created_at AS starred_at
FROM repo_stars s
JOIN users u ON u.id = s.user_id
JOIN namespaces n ON n.id = u.id
WHERE s.repo_id = $1
ORDER BY s.created_at DESC, s.user_id
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: ListUserStarredRepos :many
-- Repositories starred by a user that a viewer can read, latest star first.
SELECT sqlc.embed(r), n.name AS owner_name, n.kind AS owner_kind, s.created_at AS starred_at
FROM repo_stars s
JOIN repos r ON r.id = s.repo_id
JOIN namespaces n ON n.id = r.owner_id
WHERE s.user_id = sqlc.arg(user_id)::uuid
  AND repo_readable(r.id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
ORDER BY s.created_at DESC, r.id
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
