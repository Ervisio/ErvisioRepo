-- Teams, their members and their repository permissions.

-- name: InsertTeam :exec
INSERT INTO teams (id, org_id, name, description) VALUES ($1, $2, $3, $4);

-- name: GetTeamByName :one
SELECT t.id, t.org_id, t.name, t.description, t.created_at, t.updated_at,
       (SELECT count(*) FROM team_members tm WHERE tm.team_id = t.id) AS members
FROM teams t
WHERE t.org_id = sqlc.arg(org_id)::uuid AND t.name_lower = lower(sqlc.arg(name)::text);

-- name: ListTeams :many
SELECT t.id, t.org_id, t.name, t.description, t.created_at, t.updated_at,
       (SELECT count(*) FROM team_members tm WHERE tm.team_id = t.id) AS members
FROM teams t
WHERE t.org_id = $1
ORDER BY t.name_lower;

-- name: UpdateTeam :exec
UPDATE teams SET name = $2, description = $3, updated_at = now() WHERE id = $1;

-- name: DeleteTeam :execrows
DELETE FROM teams WHERE id = $1;

-- name: ListTeamMembers :many
SELECT tm.user_id, n.name AS username, u.display_name, tm.role, tm.created_at
FROM team_members tm
JOIN users u ON u.id = tm.user_id
JOIN namespaces n ON n.id = u.id
WHERE tm.team_id = $1
ORDER BY n.name_lower;

-- name: GetTeamMemberRole :one
SELECT role FROM team_members WHERE team_id = $1 AND user_id = $2;

-- name: UpsertTeamMember :exec
INSERT INTO team_members (team_id, org_id, user_id, role) VALUES ($1, $2, $3, $4)
ON CONFLICT (team_id, user_id) DO UPDATE SET role = EXCLUDED.role;

-- name: AddTeamMembers :exec
-- Adds a user to the given teams of an organization, keeping existing roles.
INSERT INTO team_members (team_id, org_id, user_id, role)
SELECT t.id, t.org_id, sqlc.arg(user_id)::uuid, 'member'
FROM teams t
WHERE t.org_id = sqlc.arg(org_id)::uuid AND t.id = ANY (sqlc.arg(team_ids)::uuid[])
ON CONFLICT (team_id, user_id) DO NOTHING;

-- name: DeleteTeamMember :execrows
DELETE FROM team_members WHERE team_id = $1 AND user_id = $2;

-- name: CountTeamsInOrg :one
SELECT count(*) FROM teams WHERE org_id = sqlc.arg(org_id)::uuid AND id = ANY (sqlc.arg(team_ids)::uuid[]);

-- name: SetTeamRepoPermission :exec
INSERT INTO team_repos (team_id, repo_id, permission) VALUES ($1, $2, $3)
ON CONFLICT (team_id, repo_id) DO UPDATE SET permission = EXCLUDED.permission;

-- name: DeleteTeamRepo :execrows
DELETE FROM team_repos WHERE team_id = $1 AND repo_id = $2;

-- name: ListTeamRepos :many
SELECT repo_id, permission, created_at FROM team_repos WHERE team_id = $1 ORDER BY repo_id;

-- name: ListUserTeamRepoPermissions :many
-- The permissions a user receives on a repository through teams.
SELECT tr.permission
FROM team_repos tr
JOIN team_members tm ON tm.team_id = tr.team_id
WHERE tr.repo_id = $1 AND tm.user_id = $2;
