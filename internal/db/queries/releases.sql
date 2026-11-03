-- Releases and their assets (PLAN §4.2). The asset bytes live in the storage
-- tier; the rows here own them.

-- name: InsertRelease :one
INSERT INTO releases (id, repo_id, tag, name, body, draft, prerelease, target, commit_sha, author_id, published_at)
VALUES (@id, @repo_id, @tag, @name, @body, @draft, @prerelease, @target, @commit_sha, @author_id, @published_at)
RETURNING *;

-- name: GetRelease :one
SELECT * FROM releases WHERE id = @id AND repo_id = @repo_id;

-- name: GetReleaseForUpdate :one
SELECT * FROM releases WHERE id = @id AND repo_id = @repo_id FOR UPDATE;

-- name: ListReleases :many
-- Newest first; drafts only when include_drafts. An empty tag matches all.
SELECT * FROM releases
WHERE repo_id = @repo_id
  AND (@include_drafts::bool OR NOT draft)
  AND (@tag::text = '' OR tag = @tag::text)
ORDER BY COALESCE(published_at, created_at) DESC, id DESC
LIMIT @lim OFFSET @off;

-- name: GetLatestRelease :one
-- The newest published release that is not a draft or a prerelease.
SELECT * FROM releases
WHERE repo_id = @repo_id AND NOT draft AND NOT prerelease
ORDER BY published_at DESC NULLS LAST, id DESC
LIMIT 1;

-- name: UpdateRelease :one
UPDATE releases SET tag = @tag, name = @name, body = @body, draft = @draft, prerelease = @prerelease,
    target = @target, commit_sha = @commit_sha, published_at = @published_at, updated_at = now()
WHERE id = @id AND repo_id = @repo_id
RETURNING *;

-- name: DeleteRelease :execrows
DELETE FROM releases WHERE id = @id AND repo_id = @repo_id;

-- name: InsertReleaseAsset :one
INSERT INTO release_assets (id, release_id, repo_id, name, label, content_type, size, sha256, storage_policy, storage_key, uploader_id)
VALUES (@id, @release_id, @repo_id, @name, @label, @content_type, @size, @sha256, @storage_policy, @storage_key, @uploader_id)
RETURNING *;

-- name: GetReleaseAsset :one
SELECT * FROM release_assets WHERE id = @id AND release_id = @release_id AND repo_id = @repo_id;

-- name: ListReleaseAssets :many
SELECT * FROM release_assets WHERE release_id = ANY(@release_ids::uuid[]) ORDER BY release_id, name;

-- name: ListRepoReleaseAssets :many
-- Every asset of a repository, to clean up its objects.
SELECT * FROM release_assets WHERE repo_id = @repo_id ORDER BY id;

-- name: UpdateReleaseAsset :one
UPDATE release_assets SET name = @name, label = @label, updated_at = now()
WHERE id = @id AND release_id = @release_id AND repo_id = @repo_id
RETURNING *;

-- name: DeleteReleaseAsset :execrows
DELETE FROM release_assets WHERE id = @id AND release_id = @release_id AND repo_id = @repo_id;

-- name: BumpReleaseAssetDownloads :exec
UPDATE release_assets SET download_count = download_count + 1 WHERE id = @id;

-- name: ReleaseAuthorNames :many
SELECT n.id, n.name FROM namespaces n WHERE n.id = ANY(@ids::uuid[]);

-- name: SumRepoReleaseAssetBytes :one
-- Bytes held by every release asset of a repository (released from the owner
-- quota when the repository is deleted).
SELECT COALESCE(sum(size), 0)::bigint FROM release_assets WHERE repo_id = @repo_id;

-- name: ListOrphanReleaseObjects :many
-- Objects under "<policy>/releases/" that no asset row references and
-- that are older than "before" (an upload in progress has no row yet).
SELECT so.target, so.key, so.policy FROM storage_objects so
WHERE so.policy = ANY(@policies::text[])
  AND so.key LIKE so.policy || '/releases/%'
  AND so.created_at < @before::timestamptz
  AND NOT EXISTS (SELECT 1 FROM release_assets a WHERE a.storage_policy || '/' || a.storage_key = so.key)
ORDER BY so.created_at
LIMIT @lim;
