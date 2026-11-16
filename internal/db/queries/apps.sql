-- Apps in the style of GitHub Apps (APPS-PLAN A2): registration, private
-- keys, installations and installation access tokens.

-- name: InsertBotNamespace :exec
INSERT INTO namespaces (id, kind, name) VALUES ($1, 'bot', $2);

-- name: InsertBotUser :exec
INSERT INTO users (id, display_name, role, locale) VALUES ($1, $2, 'user', 'en');

-- name: InsertAppBot :exec
INSERT INTO bots (user_id, app_id, description) VALUES ($1, $2, $3);

-- name: UpdateBotDescription :exec
UPDATE bots SET description = $2 WHERE user_id = $1;

-- name: UpdateBotDisplayName :exec
UPDATE users SET display_name = $2 WHERE id = $1;

-- name: InsertApp :one
INSERT INTO apps (id, slug, owner_id, oauth_app_id, bot_id, name, description, homepage_url, logo_url, public,
                  permissions, events, setup_url, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING num_id;

-- name: GetAppBySlug :one
SELECT a.*, n.kind AS owner_kind, n.name AS owner_name, o.client_id, o.callback_urls, bn.name AS bot_login
FROM apps a
JOIN namespaces n ON n.id = a.owner_id
JOIN oauth_apps o ON o.id = a.oauth_app_id
JOIN namespaces bn ON bn.id = a.bot_id
WHERE a.slug = lower(sqlc.arg(slug)::text);

-- name: GetAppByID :one
SELECT a.*, n.kind AS owner_kind, n.name AS owner_name, o.client_id, o.callback_urls, bn.name AS bot_login
FROM apps a
JOIN namespaces n ON n.id = a.owner_id
JOIN oauth_apps o ON o.id = a.oauth_app_id
JOIN namespaces bn ON bn.id = a.bot_id
WHERE a.id = $1;

-- name: GetAppByNumID :one
SELECT a.*, n.kind AS owner_kind, n.name AS owner_name, o.client_id, o.callback_urls, bn.name AS bot_login
FROM apps a
JOIN namespaces n ON n.id = a.owner_id
JOIN oauth_apps o ON o.id = a.oauth_app_id
JOIN namespaces bn ON bn.id = a.bot_id
WHERE a.num_id = $1;

-- name: GetAppByClientID :one
SELECT a.*, n.kind AS owner_kind, n.name AS owner_name, o.client_id, o.callback_urls, bn.name AS bot_login
FROM apps a
JOIN namespaces n ON n.id = a.owner_id
JOIN oauth_apps o ON o.id = a.oauth_app_id
JOIN namespaces bn ON bn.id = a.bot_id
WHERE o.client_id = $1;

-- name: ListAppsByOwner :many
SELECT a.*, n.kind AS owner_kind, n.name AS owner_name, o.client_id, o.callback_urls, bn.name AS bot_login,
       (SELECT count(*) FROM app_installations i WHERE i.app_id = a.id)::bigint AS installations
FROM apps a
JOIN namespaces n ON n.id = a.owner_id
JOIN oauth_apps o ON o.id = a.oauth_app_id
JOIN namespaces bn ON bn.id = a.bot_id
WHERE a.owner_id = $1
ORDER BY a.created_at DESC;

-- name: CountAppsByOwner :one
SELECT count(*) FROM apps WHERE owner_id = $1;

-- name: CountAppInstallations :one
SELECT count(*) FROM app_installations WHERE app_id = $1;

-- name: UpdateApp :exec
UPDATE apps SET name = $2, description = $3, homepage_url = $4, logo_url = $5, public = $6, permissions = $7,
                events = $8, setup_url = $9, updated_at = now()
WHERE id = $1;

-- name: UpdateAppOAuthClient :exec
-- Keeps the linked OAuth client in step with the app.
UPDATE oauth_apps SET name = $2, description = $3, homepage_url = $4, logo_url = $5, callback_urls = $6,
                      updated_at = now()
WHERE id = $1;

-- name: DeleteApp :one
DELETE FROM apps WHERE id = $1 RETURNING oauth_app_id;

-- name: DeleteOAuthAppOfApp :exec
DELETE FROM oauth_apps WHERE id = $1;

-- name: OAuthAppIsApp :one
SELECT EXISTS (SELECT 1 FROM apps WHERE oauth_app_id = $1);

-- name: GetAppPermissionsByOAuthApp :one
SELECT id, permissions FROM apps WHERE oauth_app_id = $1;

-- name: GetAppPermissions :one
SELECT permissions FROM apps WHERE id = $1;

-- name: InsertAppKey :exec
INSERT INTO app_keys (id, app_id, public_key, fingerprint, created_by) VALUES ($1, $2, $3, $4, $5);

-- name: ListAppKeys :many
SELECT k.id, k.app_id, k.public_key, k.fingerprint, k.created_at, k.last_used_at, COALESCE(n.name, '')::text AS created_by_name
FROM app_keys k
LEFT JOIN namespaces n ON n.id = k.created_by
WHERE k.app_id = $1
ORDER BY k.created_at DESC;

-- name: CountAppKeys :one
SELECT count(*) FROM app_keys WHERE app_id = $1;

-- name: DeleteAppKey :execrows
DELETE FROM app_keys WHERE app_id = $1 AND id = sqlc.arg(key_id);

-- name: TouchAppKey :exec
UPDATE app_keys SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: LockApp :exec
SELECT id FROM apps WHERE id = $1 FOR UPDATE;

-- name: InsertInstallation :one
INSERT INTO app_installations (id, app_id, account_id, repository_selection, permissions, events, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING num_id;

-- name: GetInstallation :one
SELECT i.*, a.slug AS app_slug, a.num_id AS app_num_id, a.bot_id, a.name AS app_name, a.owner_id AS app_owner_id,
       a.permissions AS app_permissions, a.events AS app_events, a.setup_url,
       n.kind AS account_kind, n.name AS account_name
FROM app_installations i
JOIN apps a ON a.id = i.app_id
JOIN namespaces n ON n.id = i.account_id
WHERE i.id = $1;

-- name: GetInstallationByNum :one
SELECT i.*, a.slug AS app_slug, a.num_id AS app_num_id, a.bot_id, a.name AS app_name, a.owner_id AS app_owner_id,
       a.permissions AS app_permissions, a.events AS app_events, a.setup_url,
       n.kind AS account_kind, n.name AS account_name
FROM app_installations i
JOIN apps a ON a.id = i.app_id
JOIN namespaces n ON n.id = i.account_id
WHERE i.num_id = $1;

-- name: GetInstallationByAccount :one
SELECT i.id FROM app_installations i WHERE i.app_id = $1 AND i.account_id = $2;

-- name: ListAppInstallations :many
SELECT i.*, a.slug AS app_slug, a.num_id AS app_num_id, a.bot_id, a.name AS app_name, a.owner_id AS app_owner_id,
       a.permissions AS app_permissions, a.events AS app_events, a.setup_url,
       n.kind AS account_kind, n.name AS account_name
FROM app_installations i
JOIN apps a ON a.id = i.app_id
JOIN namespaces n ON n.id = i.account_id
WHERE i.app_id = $1
ORDER BY i.num_id
LIMIT $2 OFFSET $3;

-- name: ListAccountInstallations :many
SELECT i.*, a.slug AS app_slug, a.num_id AS app_num_id, a.bot_id, a.name AS app_name, a.owner_id AS app_owner_id,
       a.permissions AS app_permissions, a.events AS app_events, a.setup_url,
       n.kind AS account_kind, n.name AS account_name
FROM app_installations i
JOIN apps a ON a.id = i.app_id
JOIN namespaces n ON n.id = i.account_id
WHERE i.account_id = $1
ORDER BY i.created_at DESC;

-- name: ListUserInstallations :many
-- Installations on the account of user_id or on organizations it belongs to.
SELECT i.*, a.slug AS app_slug, a.num_id AS app_num_id, a.bot_id, a.name AS app_name, a.owner_id AS app_owner_id,
       a.permissions AS app_permissions, a.events AS app_events, a.setup_url,
       n.kind AS account_kind, n.name AS account_name
FROM app_installations i
JOIN apps a ON a.id = i.app_id
JOIN namespaces n ON n.id = i.account_id
WHERE (i.account_id = @user_id OR i.account_id IN (SELECT m.org_id FROM org_members m WHERE m.user_id = @user_id))
  AND (sqlc.narg(app_id)::uuid IS NULL OR i.app_id = sqlc.narg(app_id)::uuid)
ORDER BY i.num_id;

-- name: InstallationForRepo :one
-- The installation of app_id that covers repository repo_id.
SELECT i.id FROM app_installations i
JOIN repos r ON r.id = @repo_id AND r.owner_id = i.account_id
WHERE i.app_id = @app_id
  AND (i.repository_selection = 'all'
       OR EXISTS (SELECT 1 FROM app_installation_repos ir WHERE ir.installation_id = i.id AND ir.repo_id = r.id));

-- name: AppInstalledOnRepo :one
SELECT EXISTS (
    SELECT 1 FROM app_installations i
    JOIN repos r ON r.id = @repo_id AND r.owner_id = i.account_id
    WHERE i.app_id = @app_id AND i.suspended_at IS NULL
      AND (i.repository_selection = 'all'
           OR EXISTS (SELECT 1 FROM app_installation_repos ir WHERE ir.installation_id = i.id AND ir.repo_id = r.id)));

-- name: InstallationsForRepo :many
-- Active installations covering a repository, with the webhook of their app
-- (for event deliveries).
SELECT i.id, i.num_id, i.permissions, i.events, a.id AS app_id, a.num_id AS app_num_id, w.id AS webhook_id
FROM app_installations i
JOIN repos r ON r.id = @repo_id AND r.owner_id = i.account_id
JOIN apps a ON a.id = i.app_id
JOIN webhooks w ON w.app_id = a.id AND w.active
WHERE i.suspended_at IS NULL
  AND (i.repository_selection = 'all'
       OR EXISTS (SELECT 1 FROM app_installation_repos ir WHERE ir.installation_id = i.id AND ir.repo_id = r.id))
ORDER BY i.num_id;

-- name: UpdateInstallationSelection :exec
UPDATE app_installations SET repository_selection = $2, updated_at = now() WHERE id = $1;

-- name: DeleteInstallationRepos :exec
DELETE FROM app_installation_repos WHERE installation_id = $1;

-- name: InsertInstallationRepo :exec
INSERT INTO app_installation_repos (installation_id, repo_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: ListInstallationRepoIDs :many
SELECT repo_id FROM app_installation_repos WHERE installation_id = $1;

-- name: ListInstallationRepos :many
-- The repositories an installation covers.
SELECT r.id, r.name, r.visibility, r.description, r.default_branch, r.created_at, r.updated_at, n.name AS owner_name
FROM repos r
JOIN namespaces n ON n.id = r.owner_id
JOIN app_installations i ON i.id = @installation_id AND i.account_id = r.owner_id
WHERE i.repository_selection = 'all'
   OR EXISTS (SELECT 1 FROM app_installation_repos ir WHERE ir.installation_id = i.id AND ir.repo_id = r.id)
ORDER BY lower(r.name);

-- name: SetInstallationSuspended :exec
UPDATE app_installations SET suspended_at = sqlc.narg(suspended_at), suspended_by = sqlc.narg(suspended_by),
                             updated_at = now()
WHERE id = $1;

-- name: SetInstallationGrants :exec
UPDATE app_installations SET permissions = $2, events = $3, updated_at = now() WHERE id = $1;

-- name: ListInstallationGrants :many
SELECT id, permissions, events FROM app_installations WHERE app_id = $1;

-- name: DeleteInstallation :execrows
DELETE FROM app_installations WHERE id = $1;

-- name: InsertInstallationToken :exec
INSERT INTO app_installation_tokens (id, installation_id, token_hash, permissions, repo_ids, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetInstallationToken :one
SELECT t.id, t.permissions AS token_permissions, t.repo_ids, t.expires_at,
       i.id AS installation_id, i.num_id AS installation_num_id, i.account_id, i.repository_selection,
       i.permissions AS installation_permissions, i.suspended_at,
       a.id AS app_id, a.num_id AS app_num_id, a.slug AS app_slug, a.bot_id, a.permissions AS app_permissions,
       bn.name AS bot_login
FROM app_installation_tokens t
JOIN app_installations i ON i.id = t.installation_id
JOIN apps a ON a.id = i.app_id
JOIN namespaces bn ON bn.id = a.bot_id
WHERE t.token_hash = $1;

-- name: DeleteInstallationToken :execrows
DELETE FROM app_installation_tokens WHERE id = $1;

-- name: DeleteExpiredInstallationTokens :execrows
DELETE FROM app_installation_tokens WHERE expires_at < sqlc.arg(before);

-- name: GetAppWebhook :one
SELECT * FROM webhooks WHERE app_id = $1;

-- name: InsertAppWebhook :exec
INSERT INTO webhooks (id, app_id, url, content_type, format, secret, events, active, created_by)
VALUES ($1, $2, $3, 'json', 'github', $4, '{*}', $5, $6);

-- name: UpdateAppWebhook :exec
UPDATE webhooks SET url = $2, secret = $3, active = $4, updated_at = now() WHERE id = $1;

-- name: DeleteAppWebhook :exec
DELETE FROM webhooks WHERE app_id = $1;

-- name: ReposByNames :many
-- Repositories of an account by name (installation repository selection).
SELECT id, name FROM repos WHERE owner_id = $1 AND lower(name) = ANY(sqlc.arg(names)::text[]);

-- name: ReposByIDs :many
SELECT id, owner_id, name FROM repos WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: GetAppNumID :one
SELECT num_id FROM apps WHERE id = $1;

-- name: ListUserOwnedOrgs :many
SELECT n.id, n.name FROM org_members m
JOIN namespaces n ON n.id = m.org_id
WHERE m.user_id = $1 AND m.role = 'owner'
ORDER BY n.name_lower;
