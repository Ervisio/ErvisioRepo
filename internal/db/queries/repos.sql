-- Repositories, redirects, collaborators and tiering state (PLAN §4.3, §5.2).

-- name: InsertRepo :exec
INSERT INTO repos (id, owner_id, name, description, visibility, default_branch, storage_state, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetRepo :one
SELECT sqlc.embed(r), n.name AS owner_name, n.kind AS owner_kind
FROM repos r
JOIN namespaces n ON n.id = r.owner_id
WHERE r.id = $1;

-- name: GetRepoForUpdate :one
SELECT * FROM repos WHERE id = $1 FOR UPDATE;

-- name: GetRepoByPath :one
SELECT sqlc.embed(r), n.name AS owner_name, n.kind AS owner_kind
FROM repos r
JOIN namespaces n ON n.id = r.owner_id
WHERE n.name_lower = lower(sqlc.arg(owner)::text) AND r.name_lower = lower(sqlc.arg(name)::text);

-- name: GetRepoRedirect :one
-- The repository an old path points to.
SELECT rr.repo_id
FROM repo_redirects rr
JOIN namespaces n ON n.id = rr.owner_id
WHERE n.name_lower = lower(sqlc.arg(owner)::text) AND rr.name_lower = lower(sqlc.arg(name)::text);

-- name: UpsertRepoRedirect :exec
INSERT INTO repo_redirects (owner_id, name_lower, repo_id) VALUES ($1, lower(sqlc.arg(name)::text), $2)
ON CONFLICT (owner_id, name_lower) DO UPDATE SET repo_id = EXCLUDED.repo_id, created_at = now();

-- name: DeleteRepoRedirect :exec
DELETE FROM repo_redirects WHERE owner_id = $1 AND name_lower = lower(sqlc.arg(name)::text);

-- name: ListRepoRedirects :many
SELECT n.name AS owner_name, rr.name_lower, rr.created_at
FROM repo_redirects rr
JOIN namespaces n ON n.id = rr.owner_id
WHERE rr.repo_id = $1
ORDER BY rr.created_at;

-- name: CountOwnerRepos :one
SELECT count(*) FROM repos WHERE owner_id = $1;

-- name: ListOwnerRepos :many
-- Repositories of a namespace visible to a viewer. all = the viewer sees
-- every repository (instance admin, the user himself, organization
-- owner/admin); internal = the viewer is signed in and not restricted.
SELECT sqlc.embed(r), n.name AS owner_name, n.kind AS owner_kind
FROM repos r
JOIN namespaces n ON n.id = r.owner_id
WHERE r.owner_id = sqlc.arg(owner_id)::uuid
  AND (sqlc.arg(all_repos)::boolean
       OR r.visibility = 'public'
       OR (sqlc.arg(internal)::boolean AND r.visibility = 'internal')
       OR EXISTS (SELECT 1 FROM repo_collaborators c WHERE c.repo_id = r.id AND c.user_id = sqlc.arg(viewer)::uuid)
       OR EXISTS (SELECT 1 FROM team_repos tr JOIN team_members tm ON tm.team_id = tr.team_id
                  WHERE tr.repo_id = r.id AND tm.user_id = sqlc.arg(viewer)::uuid)
       OR EXISTS (SELECT 1 FROM org_members m JOIN orgs o ON o.id = m.org_id
                  WHERE m.org_id = r.owner_id AND m.user_id = sqlc.arg(viewer)::uuid
                    AND m.role = 'member' AND o.base_permission <> 'none'))
ORDER BY r.name_lower
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: ListUserAccessibleRepos :many
-- Repositories a user can reach without public or internal visibility: his
-- own, those of organizations he owns or administers or whose base
-- permission he receives, and those granted through teams or directly.
SELECT sqlc.embed(r), n.name AS owner_name, n.kind AS owner_kind
FROM repos r
JOIN namespaces n ON n.id = r.owner_id
WHERE r.owner_id = sqlc.arg(user_id)::uuid
   OR EXISTS (SELECT 1 FROM repo_collaborators c WHERE c.repo_id = r.id AND c.user_id = sqlc.arg(user_id)::uuid)
   OR EXISTS (SELECT 1 FROM team_repos tr JOIN team_members tm ON tm.team_id = tr.team_id
              WHERE tr.repo_id = r.id AND tm.user_id = sqlc.arg(user_id)::uuid)
   OR EXISTS (SELECT 1 FROM org_members m JOIN orgs o ON o.id = m.org_id
              WHERE m.org_id = r.owner_id AND m.user_id = sqlc.arg(user_id)::uuid
                AND (m.role IN ('owner', 'admin') OR (m.role = 'member' AND o.base_permission <> 'none')))
ORDER BY n.name_lower, r.name_lower
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: UpdateRepo :exec
UPDATE repos SET description = $2, visibility = $3, default_branch = $4, updated_at = now() WHERE id = $1;

-- name: RenameRepo :exec
UPDATE repos SET name = $2, updated_at = now() WHERE id = $1;

-- name: TransferRepo :exec
UPDATE repos SET owner_id = $2, name = $3, updated_at = now() WHERE id = $1;

-- name: DeleteRepo :execrows
DELETE FROM repos WHERE id = $1;

-- name: DeleteRepoTeamGrants :exec
-- Removes the team grants of a repository (on transfer to another owner).
DELETE FROM team_repos WHERE repo_id = $1;

-- name: GetRepoAccess :one
-- What the permission of a user on a repository depends on, besides the
-- teams (ListUserTeamRepoPermissions) and the account itself.
SELECT COALESCE(m.role, '')::text AS org_role,
       COALESCE(o.base_permission, 'none')::text AS base_permission,
       COALESCE(c.permission, 'none')::text AS collaborator
FROM repos r
LEFT JOIN orgs o ON o.id = r.owner_id
LEFT JOIN org_members m ON m.org_id = r.owner_id AND m.user_id = sqlc.arg(user_id)::uuid
LEFT JOIN repo_collaborators c ON c.repo_id = r.id AND c.user_id = sqlc.arg(user_id)::uuid
WHERE r.id = sqlc.arg(repo_id)::uuid;

-- name: UpsertRepoCollaborator :exec
INSERT INTO repo_collaborators (repo_id, user_id, permission, added_by) VALUES ($1, $2, $3, $4)
ON CONFLICT (repo_id, user_id) DO UPDATE SET permission = EXCLUDED.permission;

-- name: DeleteRepoCollaborator :execrows
DELETE FROM repo_collaborators WHERE repo_id = $1 AND user_id = $2;

-- name: ListRepoCollaborators :many
SELECT c.user_id, n.name AS username, u.display_name, c.permission, c.created_at
FROM repo_collaborators c
JOIN users u ON u.id = c.user_id
JOIN namespaces n ON n.id = u.id
WHERE c.repo_id = $1
ORDER BY n.name_lower;

-- name: GetRepoStorage :one
-- Tiering state read by gitstore.Tier.Acquire.
SELECT id, owner_id, storage_state, pinned, size_bytes, manifest_seq, last_access_at
FROM repos WHERE id = $1;

-- name: SetRepoStorageState :execrows
-- Compare-and-set of the tiering state: zero rows means the state was not
-- from_state (someone else moved it).
UPDATE repos SET storage_state = sqlc.arg(to_state), state_changed_at = now()
WHERE id = sqlc.arg(id) AND storage_state = sqlc.arg(from_state);

-- name: TouchRepoAccess :exec
-- Records an access at time "at"; skipped when the last recorded access is
-- newer than not_before, to keep writes off the read path.
UPDATE repos SET last_access_at = sqlc.arg(at)::timestamptz
WHERE id = sqlc.arg(id)::uuid AND last_access_at < sqlc.arg(not_before)::timestamptz;

-- name: SetRepoPinned :exec
UPDATE repos SET pinned = $2, updated_at = now() WHERE id = $1;

-- name: SetRepoManifestSeq :exec
-- Records the manifest sequence after a successful CAS save.
UPDATE repos SET manifest_seq = GREATEST(manifest_seq, sqlc.arg(seq)::bigint) WHERE id = sqlc.arg(id);

-- name: SetRepoSize :one
-- Sets the size of a repository and returns the previous one, so that the
-- caller can adjust the storage usage of the owner by the difference.
UPDATE repos r SET size_bytes = sqlc.arg(size_bytes)::bigint
FROM (SELECT id, size_bytes FROM repos WHERE id = sqlc.arg(id)::uuid FOR UPDATE) old
WHERE r.id = old.id
RETURNING old.size_bytes AS previous;

-- name: ListEvictionCandidates :many
SELECT id, size_bytes, last_access_at
FROM repos
WHERE storage_state = 'hot' AND NOT pinned AND last_access_at < sqlc.arg(before)
ORDER BY last_access_at
LIMIT sqlc.arg(lim);

-- name: LockRepoWrite :exec
-- Transaction-scoped form of the repository write lock taken by
-- gitstore.Tier.Acquire (same key): use it for metadata changes that must
-- not interleave with a git write.
SELECT pg_advisory_xact_lock(hashtextextended('erepo.repo:' || sqlc.arg(repo_id)::uuid::text, 0));

-- name: LockRepoSession :exec
-- Session-scoped repository write lock held by gitstore.Tier.Acquire on a
-- dedicated connection; release it with UnlockRepoSession on the same one.
SELECT pg_advisory_lock(hashtextextended('erepo.repo:' || sqlc.arg(repo_id)::uuid::text, 0));

-- name: TryLockRepoSession :one
SELECT pg_try_advisory_lock(hashtextextended('erepo.repo:' || sqlc.arg(repo_id)::uuid::text, 0))::bool AS locked;

-- name: UnlockRepoSession :one
SELECT pg_advisory_unlock(hashtextextended('erepo.repo:' || sqlc.arg(repo_id)::uuid::text, 0))::bool AS unlocked;
