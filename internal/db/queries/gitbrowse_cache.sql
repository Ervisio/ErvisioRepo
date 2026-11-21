-- name: GetBrowseTree :one
SELECT entries, truncated FROM git_browse_trees WHERE repo_id = @repo_id AND oid = @oid;

-- name: PutBrowseTree :exec
INSERT INTO git_browse_trees (repo_id, oid, entries, truncated)
VALUES (@repo_id, @oid, @entries, @truncated)
ON CONFLICT (repo_id, oid) DO NOTHING;

-- name: GetBrowseCommit :one
SELECT commit_sha, tree_oid FROM git_browse_commits WHERE repo_id = @repo_id AND oid = @oid;

-- name: PutBrowseCommit :exec
INSERT INTO git_browse_commits (repo_id, oid, commit_sha, tree_oid)
VALUES (@repo_id, @oid, @commit_sha, @tree_oid)
ON CONFLICT (repo_id, oid) DO NOTHING;

-- name: GetBrowseBlob :one
SELECT content FROM git_browse_blobs WHERE repo_id = @repo_id AND oid = @oid;

-- name: PutBrowseBlob :exec
INSERT INTO git_browse_blobs (repo_id, oid, content)
VALUES (@repo_id, @oid, @content)
ON CONFLICT (repo_id, oid) DO NOTHING;

-- name: PruneBrowseTrees :execrows
DELETE FROM git_browse_trees WHERE created_at < @before;

-- name: PruneBrowseCommits :execrows
DELETE FROM git_browse_commits WHERE created_at < @before;

-- name: PruneBrowseBlobs :execrows
DELETE FROM git_browse_blobs WHERE created_at < @before;
