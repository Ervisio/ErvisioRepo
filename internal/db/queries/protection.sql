-- Branch protection and tag rules (internal/protection).

-- name: ListBranchProtections :many
SELECT * FROM branch_protections WHERE repo_id = $1 ORDER BY pattern;

-- name: GetBranchProtection :one
SELECT * FROM branch_protections WHERE repo_id = $1 AND id = sqlc.arg(rule_id);

-- name: InsertBranchProtection :one
INSERT INTO branch_protections (
    id, repo_id, pattern, required_approvals, require_code_owner_reviews, dismiss_stale_reviews,
    required_status_checks, strict_status_checks, require_linear_history, allow_force_pushes,
    allow_deletions, require_signed_commits, restrict_pushes, push_user_ids, push_team_ids,
    admins_bypass, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
RETURNING *;

-- name: UpdateBranchProtection :one
UPDATE branch_protections SET
    pattern = $3, required_approvals = $4, require_code_owner_reviews = $5, dismiss_stale_reviews = $6,
    required_status_checks = $7, strict_status_checks = $8, require_linear_history = $9,
    allow_force_pushes = $10, allow_deletions = $11, require_signed_commits = $12,
    restrict_pushes = $13, push_user_ids = $14, push_team_ids = $15, admins_bypass = $16,
    updated_at = now()
WHERE repo_id = $1 AND id = $2
RETURNING *;

-- name: DeleteBranchProtection :execrows
DELETE FROM branch_protections WHERE repo_id = $1 AND id = sqlc.arg(rule_id);

-- name: ListTagRules :many
SELECT * FROM tag_rules WHERE repo_id = $1 ORDER BY pattern;

-- name: GetTagRule :one
SELECT * FROM tag_rules WHERE repo_id = $1 AND id = sqlc.arg(rule_id);

-- name: InsertTagRule :one
INSERT INTO tag_rules (id, repo_id, pattern, create_min, update_min, delete_min,
    allowed_user_ids, allowed_team_ids, admins_bypass, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateTagRule :one
UPDATE tag_rules SET
    pattern = $3, create_min = $4, update_min = $5, delete_min = $6,
    allowed_user_ids = $7, allowed_team_ids = $8, admins_bypass = $9, updated_at = now()
WHERE repo_id = $1 AND id = $2
RETURNING *;

-- name: DeleteTagRule :execrows
DELETE FROM tag_rules WHERE repo_id = $1 AND id = sqlc.arg(rule_id);

-- name: ListProtectionUserTeams :many
-- Teams the user belongs to, for push restrictions and CODEOWNERS teams.
SELECT team_id FROM team_members WHERE user_id = $1;

-- name: ListProtectionTeamMembers :many
SELECT user_id FROM team_members WHERE team_id = $1;

-- name: ResolveProtectionUsers :many
-- Usernames of the given user ids (unknown ids are omitted).
SELECT n.id, n.name FROM namespaces n JOIN users u ON u.id = n.id
WHERE n.id = ANY(sqlc.arg(ids)::uuid[]) ORDER BY n.name_lower;

-- name: ResolveProtectionTeams :many
-- Names of the given teams of one organization (unknown ids are omitted).
SELECT id, name FROM teams
WHERE org_id = sqlc.arg(org_id)::uuid AND id = ANY(sqlc.arg(ids)::uuid[]) ORDER BY name_lower;

-- name: GetProtectionUserByVerifiedEmail :one
-- The account owning a verified address (commit signer, CODEOWNERS email).
SELECT user_id FROM emails WHERE address_lower = lower(sqlc.arg(address)::text) AND verified_at IS NOT NULL;

-- name: ListProtectionGPGKeys :many
-- Armored public keys of a user, for commit signature verification.
SELECT key_id, fingerprint, subkey_ids, emails, armored, expires_at
FROM gpg_keys WHERE user_id = $1;

-- name: ListProtectionSSHKeys :many
-- Public keys of a user, for SSH commit signature verification.
SELECT fingerprint, public_key FROM ssh_keys WHERE user_id = $1;
