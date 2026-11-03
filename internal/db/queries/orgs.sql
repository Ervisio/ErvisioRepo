-- Organizations and their members (InsertOrg, GetOrgByName, UpsertOrgMember
-- and GetOrgMemberRole are in identity.sql).

-- name: GetOrgDetails :one
SELECT o.id, n.name, o.display_name, o.description, o.base_permission, o.require_2fa, o.verified_at, o.created_at, o.updated_at
FROM orgs o
JOIN namespaces n ON n.id = o.id
WHERE n.name_lower = lower(sqlc.arg(name)::text);

-- name: UpdateOrg :exec
UPDATE orgs SET display_name = $2, description = $3, base_permission = $4, require_2fa = $5, updated_at = now()
WHERE id = $1;

-- name: DeleteOrgNamespace :execrows
-- Deletes the organization with its namespace, members, teams and invites.
DELETE FROM namespaces WHERE id = $1 AND kind = 'org';

-- name: ListUserOrgs :many
SELECT o.id, n.name, o.display_name, o.description, o.base_permission, o.require_2fa, o.verified_at, o.created_at, o.updated_at,
       m.role
FROM org_members m
JOIN orgs o ON o.id = m.org_id
JOIN namespaces n ON n.id = o.id
WHERE m.user_id = $1
ORDER BY n.name_lower;

-- name: ListOrgMembers :many
SELECT m.user_id, n.name AS username, u.display_name, m.role, m.created_at
FROM org_members m
JOIN users u ON u.id = m.user_id
JOIN namespaces n ON n.id = u.id
WHERE m.org_id = $1
ORDER BY n.name_lower;

-- name: LockOrgMembers :exec
-- Serializes membership changes of one organization inside a transaction,
-- so that the last-owner check cannot race.
SELECT pg_advisory_xact_lock(hashtextextended('erepo.org_members:' || sqlc.arg(org_id)::uuid::text, 0));

-- name: CountOrgOwners :one
SELECT count(*) FROM org_members WHERE org_id = $1 AND role = 'owner';

-- name: InsertOrgMember :execrows
INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)
ON CONFLICT (org_id, user_id) DO NOTHING;

-- name: SetOrgMemberRole :execrows
UPDATE org_members SET role = $3 WHERE org_id = $1 AND user_id = $2;

-- name: DeleteOrgMember :execrows
DELETE FROM org_members WHERE org_id = $1 AND user_id = $2;
