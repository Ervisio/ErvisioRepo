-- Users, emails, passwords, one-time tokens, invites and organizations.

-- name: AcquireRegistrationLock :exec
-- Serializes registrations inside a transaction, so that exactly one first
-- user becomes the instance admin.
SELECT pg_advisory_xact_lock(hashtextextended('erepo.register', 0));

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: NamespaceTaken :one
SELECT EXISTS (SELECT 1 FROM namespaces WHERE name_lower = lower(sqlc.arg(name)::text));

-- name: GetNamespaceByName :one
SELECT id, kind, name FROM namespaces WHERE name_lower = lower(sqlc.arg(name)::text);

-- name: InsertNamespace :exec
INSERT INTO namespaces (id, kind, name) VALUES ($1, $2, $3);

-- name: InsertUser :exec
INSERT INTO users (id, display_name, role, locale) VALUES ($1, $2, $3, $4);

-- name: SetUserRole :exec
UPDATE users SET role = $2, updated_at = now() WHERE id = $1;

-- name: SetUserSuspended :exec
UPDATE users SET suspended_at = CASE WHEN sqlc.arg(suspended)::boolean THEN now() END, updated_at = now()
WHERE id = $1;

-- name: GetUserByID :one
SELECT u.id, n.name AS username, u.display_name, u.avatar_url, u.role, u.locale, u.suspended_at, u.created_at,
       e.id AS email_id, e.address AS email, e.verified_at AS email_verified_at
FROM users u
JOIN namespaces n ON n.id = u.id
LEFT JOIN emails e ON e.user_id = u.id AND e.is_primary
WHERE u.id = $1;

-- name: GetUserByUsername :one
SELECT u.id, n.name AS username, u.display_name, u.role, u.locale, u.suspended_at, u.created_at
FROM users u
JOIN namespaces n ON n.id = u.id
WHERE n.name_lower = lower(sqlc.arg(username)::text);

-- name: GetLoginByUsername :one
-- Credentials for a login by username; the email is the primary address.
SELECT u.id, n.name AS username, u.role, u.locale, u.suspended_at,
       p.hash AS password_hash, e.id AS email_id, e.verified_at AS email_verified_at
FROM users u
JOIN namespaces n ON n.id = u.id
LEFT JOIN passwords p ON p.user_id = u.id
LEFT JOIN emails e ON e.user_id = u.id AND e.is_primary
WHERE n.name_lower = lower(sqlc.arg(username)::text);

-- name: GetLoginByEmail :one
-- Credentials for a login by email address; any verified address of the user works.
SELECT u.id, n.name AS username, u.role, u.locale, u.suspended_at,
       p.hash AS password_hash, e.id AS email_id, e.verified_at AS email_verified_at
FROM emails e
JOIN users u ON u.id = e.user_id
JOIN namespaces n ON n.id = u.id
LEFT JOIN passwords p ON p.user_id = u.id
WHERE e.address_lower = lower(sqlc.arg(address)::text);

-- name: InsertEmail :exec
INSERT INTO emails (id, user_id, address, is_primary, verified_at) VALUES ($1, $2, $3, $4, $5);

-- name: EmailTaken :one
SELECT EXISTS (SELECT 1 FROM emails WHERE address_lower = lower(sqlc.arg(address)::text));

-- name: GetEmailByAddress :one
SELECT e.id, e.user_id, e.address, e.is_primary, e.verified_at, u.locale, n.name AS username, u.suspended_at
FROM emails e
JOIN users u ON u.id = e.user_id
JOIN namespaces n ON n.id = u.id
WHERE e.address_lower = lower(sqlc.arg(address)::text);

-- name: GetEmailByID :one
SELECT e.id, e.user_id, e.address, e.is_primary, e.verified_at, u.locale, n.name AS username, u.suspended_at
FROM emails e
JOIN users u ON u.id = e.user_id
JOIN namespaces n ON n.id = u.id
WHERE e.id = $1;

-- name: MarkEmailVerified :execrows
UPDATE emails SET verified_at = now() WHERE id = $1 AND user_id = $2 AND verified_at IS NULL;

-- name: UpsertPassword :exec
INSERT INTO passwords (user_id, hash, updated_at) VALUES ($1, $2, now())
ON CONFLICT (user_id) DO UPDATE SET hash = EXCLUDED.hash, updated_at = now();

-- name: InsertOneTimeToken :exec
INSERT INTO one_time_tokens (token_hash, purpose, user_id, email_id, expires_at) VALUES ($1, $2, $3, $4, $5);

-- name: DeleteOneTimeTokens :exec
-- Invalidates the outstanding tokens of one purpose for a user.
DELETE FROM one_time_tokens WHERE user_id = $1 AND purpose = $2;

-- name: ConsumeOneTimeToken :one
-- Deletes and returns a token; the caller checks expires_at.
DELETE FROM one_time_tokens WHERE token_hash = $1 AND purpose = $2
RETURNING user_id, email_id, expires_at;

-- name: DeleteExpiredOneTimeTokens :execrows
DELETE FROM one_time_tokens WHERE expires_at < now();

-- name: InsertInvite :exec
INSERT INTO invites (id, token_hash, email, created_by, expires_at) VALUES ($1, $2, $3, $4, $5);

-- name: GetValidInvite :one
SELECT id, email FROM invites
WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now();

-- name: ConsumeInvite :one
UPDATE invites SET used_at = now(), used_by = sqlc.arg(used_by)
WHERE token_hash = sqlc.arg(token_hash) AND used_at IS NULL AND expires_at > now()
RETURNING id, email;

-- name: InsertOrg :exec
INSERT INTO orgs (id, display_name, description) VALUES ($1, $2, $3);

-- name: GetOrgByName :one
SELECT o.id, n.name, o.display_name, o.description, o.verified_at, o.created_at
FROM orgs o
JOIN namespaces n ON n.id = o.id
WHERE n.name_lower = lower(sqlc.arg(name)::text);

-- name: UpsertOrgMember :exec
INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)
ON CONFLICT (org_id, user_id) DO UPDATE SET role = EXCLUDED.role;

-- name: GetOrgMemberRole :one
SELECT role FROM org_members WHERE org_id = $1 AND user_id = $2;

-- name: SearchUsers :many
-- Active users whose username or display name starts with pattern (a
-- LIKE pattern, lower case), exact username first. Without instance only
-- the users visible to the caller: itself, the members of its
-- organizations, the people of the repositories it owns or collaborates
-- on and, with repo_id, the collaborators, owner and owning organization
-- members of that repository.
WITH visible AS (
    SELECT @caller::uuid AS user_id
    UNION SELECT m2.user_id FROM org_members m1 JOIN org_members m2 ON m2.org_id = m1.org_id WHERE m1.user_id = @caller
    UNION SELECT c.user_id FROM repo_collaborators c JOIN repos r ON r.id = c.repo_id WHERE r.owner_id = @caller
    UNION SELECT c2.user_id FROM repo_collaborators c1 JOIN repo_collaborators c2 ON c2.repo_id = c1.repo_id
        WHERE c1.user_id = @caller
    UNION SELECT r.owner_id FROM repo_collaborators c JOIN repos r ON r.id = c.repo_id WHERE c.user_id = @caller
    UNION SELECT c.user_id FROM repo_collaborators c WHERE c.repo_id = sqlc.narg('repo_id')::uuid
    UNION SELECT r.owner_id FROM repos r WHERE r.id = sqlc.narg('repo_id')::uuid
    UNION SELECT m.user_id FROM org_members m JOIN repos r ON r.owner_id = m.org_id
        WHERE r.id = sqlc.narg('repo_id')::uuid
)
SELECT u.id, n.name AS username, u.display_name, u.avatar_url
FROM users u
JOIN namespaces n ON n.id = u.id
WHERE u.suspended_at IS NULL
    AND (@instance::boolean OR u.id IN (SELECT v.user_id FROM visible v))
    AND (n.name_lower LIKE @pattern::text OR lower(u.display_name) LIKE @pattern::text)
ORDER BY (n.name_lower = @exact::text) DESC, (n.name_lower LIKE @pattern::text) DESC, n.name_lower
LIMIT @lim;
