-- Import of the non-git data of a GitHub repository (ghsync.Importer).

-- ---- id map ----

-- name: ListGitHubImportMap :many
SELECT github_id, local_id FROM github_import_map WHERE repo_id = @repo_id AND kind = @kind;

-- name: GetGitHubImportMap :one
SELECT local_id FROM github_import_map WHERE repo_id = @repo_id AND kind = @kind AND github_id = @github_id;

-- name: InsertGitHubImportMap :exec
INSERT INTO github_import_map (repo_id, kind, github_id, local_id)
VALUES (@repo_id, @kind, @github_id, @local_id)
ON CONFLICT DO NOTHING;

-- name: SetGitHubImportInclude :exec
UPDATE github_imports SET include = @include WHERE id = @id;

-- name: FindLinkedGitHubUser :one
-- The local user whose external identity is the GitHub account with the
-- numeric id subject. Matched on the id only, never on the e-mail.
SELECT l.user_id FROM oidc_links l JOIN users u ON u.id = l.user_id
WHERE l.subject = @subject AND (l.provider = 'github' OR l.issuer = ANY(@issuers::text[]))
ORDER BY l.created_at
LIMIT 1;

-- ---- issues ----

-- name: RaiseIssueCounter :exec
-- Makes the next allocated number greater than number.
INSERT INTO issue_counters (repo_id, last_number) VALUES (@repo_id, @number)
ON CONFLICT (repo_id) DO UPDATE SET last_number = GREATEST(issue_counters.last_number, EXCLUDED.last_number);

-- name: ImportLabel :one
INSERT INTO labels (id, repo_id, name, color, description)
VALUES (@id, @repo_id, @name, @color, @description)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: ImportMilestone :one
INSERT INTO milestones (id, repo_id, title, description, state, due_on, created_at, updated_at, closed_at)
VALUES (@id, @repo_id, @title, @description, @state, @due_on, @created_at, @updated_at, @closed_at)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetMilestoneByTitle :one
SELECT * FROM milestones WHERE repo_id = @repo_id AND lower(title) = lower(@title::text);

-- name: ImportIssue :one
-- No row when the number is already taken.
INSERT INTO issues (id, repo_id, number, kind, title, body, state, state_reason, locked, lock_reason,
  author_id, milestone_id, closed_at, closed_by, created_at, updated_at)
VALUES (@id, @repo_id, @number, @kind, @title, @body, @state, @state_reason, @locked, @lock_reason,
  @author_id, @milestone_id, @closed_at, @closed_by, @created_at, @updated_at)
ON CONFLICT (repo_id, number) DO NOTHING
RETURNING *;

-- name: ImportIssueComment :exec
INSERT INTO issue_comments (id, issue_id, repo_id, author_id, body, created_at, updated_at, edited_at)
VALUES (@id, @issue_id, @repo_id, @author_id, @body, @created_at, @updated_at, @edited_at);

-- name: ImportIssueReaction :exec
INSERT INTO issue_reactions (id, issue_id, comment_id, user_id, content, created_at)
VALUES (@id, @issue_id, @comment_id, @user_id, @content, @created_at)
ON CONFLICT DO NOTHING;

-- name: RecountIssueComments :exec
UPDATE issues i SET comment_count = (SELECT count(*) FROM issue_comments c WHERE c.issue_id = i.id)
WHERE i.repo_id = @repo_id;

-- ---- pull requests ----

-- name: ImportPullRequest :exec
INSERT INTO pull_requests (issue_id, repo_id, number, head_repo_id, head_ref, head_sha, base_ref, base_sha,
  draft, merged_at, merge_commit_sha, created_at, updated_at)
VALUES (@issue_id, @repo_id, @number, @head_repo_id, @head_ref, @head_sha, @base_ref, @base_sha,
  @draft, @merged_at, @merge_commit_sha, @created_at, @updated_at)
ON CONFLICT DO NOTHING;

-- name: ImportPullReview :exec
INSERT INTO pull_reviews (id, pull_id, repo_id, reviewer_id, state, body, commit_sha, submitted_at, created_at, updated_at)
VALUES (@id, @pull_id, @repo_id, @reviewer_id, @state, @body, @commit_sha, @submitted_at, @created_at, @updated_at);

-- name: ImportPullReviewComment :exec
INSERT INTO pull_review_comments (id, pull_id, repo_id, review_id, author_id, in_reply_to, path, side, line, start_line,
  original_line, original_start_line, original_commit_sha, commit_sha, diff_hunk, body, outdated, created_at, updated_at)
VALUES (@id, @pull_id, @repo_id, @review_id, @author_id, @in_reply_to, @path, @side, @line, @start_line,
  @original_line, @original_start_line, @original_commit_sha, @commit_sha, @diff_hunk, @body, @outdated, @created_at, @updated_at);

-- ---- releases ----

-- name: ImportRelease :one
-- No row when a release of the tag exists already.
INSERT INTO releases (id, repo_id, tag, name, body, draft, prerelease, target, commit_sha, author_id,
  created_at, updated_at, published_at)
VALUES (@id, @repo_id, @tag, @name, @body, @draft, @prerelease, @target, @commit_sha, @author_id,
  @created_at, @updated_at, @published_at)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetReleaseByTag :one
SELECT * FROM releases WHERE repo_id = @repo_id AND tag = @tag;
