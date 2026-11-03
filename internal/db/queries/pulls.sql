-- ---- forks ----

-- name: InsertRepoFork :exec
INSERT INTO repo_forks (repo_id, parent_id) VALUES (@repo_id, @parent_id);

-- name: GetRepoFork :one
SELECT * FROM repo_forks WHERE repo_id = @repo_id;

-- name: CountRepoForks :one
SELECT count(*) FROM repo_forks WHERE parent_id = @parent_id;

-- ---- pull requests ----

-- name: InsertPullRequest :one
INSERT INTO pull_requests (issue_id, repo_id, number, head_repo_id, head_ref, head_sha, base_ref, base_sha, merge_base_sha, draft)
VALUES (@issue_id, @repo_id, @number, @head_repo_id, @head_ref, @head_sha, @base_ref, @base_sha, @merge_base_sha, @draft)
RETURNING *;

-- name: GetPullRequest :one
SELECT * FROM pull_requests WHERE repo_id = @repo_id AND number = @number;

-- name: GetPullRequestByID :one
SELECT * FROM pull_requests WHERE issue_id = @issue_id;

-- name: GetPullRequestForUpdate :one
SELECT * FROM pull_requests WHERE repo_id = @repo_id AND number = @number FOR UPDATE;

-- name: GetPullRequestsByIDs :many
SELECT * FROM pull_requests WHERE issue_id = ANY(@ids::uuid[]);

-- name: SearchPullRequests :many
-- Pull requests of a repository. state is open, closed (not merged), merged,
-- all ('' = all). base/head filter on the branch names ('' = any).
SELECT i.* FROM issues i
JOIN pull_requests p ON p.issue_id = i.id
WHERE i.repo_id = @repo_id AND i.kind = 'pull'
  AND (CASE @state::text
         WHEN 'open' THEN i.state = 'open'
         WHEN 'closed' THEN i.state = 'closed'
         WHEN 'merged' THEN p.merged_at IS NOT NULL
         ELSE true END)
  AND (@base_ref::text = '' OR p.base_ref = @base_ref::text)
  AND (@head_ref::text = '' OR p.head_ref = @head_ref::text)
  AND (NOT @by_head_repo::boolean OR p.head_repo_id = @head_repo_id::uuid)
  AND (NOT @by_author::boolean OR i.author_id = @author_id::uuid)
ORDER BY
  CASE WHEN @sort::text = 'updated' THEN i.updated_at END DESC,
  i.number DESC
LIMIT @lim OFFSET @off;

-- name: OpenPullsByHead :many
-- Open pull requests whose head is ref of repository repo_id.
SELECT p.* FROM pull_requests p JOIN issues i ON i.id = p.issue_id
WHERE p.head_repo_id = @head_repo_id AND p.head_ref = @head_ref AND i.state = 'open' AND p.merged_at IS NULL;

-- name: OpenPullsByBase :many
SELECT p.* FROM pull_requests p JOIN issues i ON i.id = p.issue_id
WHERE p.repo_id = @repo_id AND p.base_ref = @base_ref AND i.state = 'open' AND p.merged_at IS NULL;

-- name: OpenPullExists :one
SELECT EXISTS (
  SELECT 1 FROM pull_requests p JOIN issues i ON i.id = p.issue_id
  WHERE p.repo_id = @repo_id AND p.head_repo_id = @head_repo_id AND p.head_ref = @head_ref
    AND p.base_ref = @base_ref AND i.state = 'open' AND p.merged_at IS NULL
);

-- name: SetPullHead :one
UPDATE pull_requests SET head_sha = @head_sha, updated_at = now()
WHERE issue_id = @issue_id
RETURNING *;

-- name: SetPullBase :one
UPDATE pull_requests SET base_ref = @base_ref, base_sha = @base_sha, mergeable_state = 'unknown', merge_sha = '',
  conflict_files = '{}', updated_at = now()
WHERE issue_id = @issue_id
RETURNING *;

-- name: SetPullDraft :one
UPDATE pull_requests SET draft = @draft, updated_at = now()
WHERE issue_id = @issue_id
RETURNING *;

-- name: SetPullMergeability :one
-- Records the test merge computed for head_sha on base_sha; a row whose
-- head or base moved in the meantime is left alone (no row returned).
UPDATE pull_requests SET mergeable_state = @mergeable_state, merge_sha = @merge_sha, conflict_files = @conflict_files::text[],
  merge_base_sha = @merge_base_sha, base_sha = @base_sha, updated_at = now()
WHERE issue_id = @issue_id AND head_sha = @head_sha
RETURNING *;

-- name: SetPullMerged :one
UPDATE pull_requests SET merged_at = now(), merged_by = @merged_by, merge_commit_sha = @merge_commit_sha,
  merge_method = @merge_method, auto_merge_method = '', auto_merge_by = NULL, updated_at = now()
WHERE issue_id = @issue_id
RETURNING *;

-- name: SetPullAutoMerge :one
UPDATE pull_requests SET auto_merge_method = @auto_merge_method, auto_merge_by = @auto_merge_by,
  auto_merge_title = @auto_merge_title, auto_merge_message = @auto_merge_message,
  auto_merge_delete = @auto_merge_delete, updated_at = now()
WHERE issue_id = @issue_id
RETURNING *;

-- name: ClearPullHeadRepo :exec
UPDATE pull_requests SET head_repo_id = NULL, updated_at = now() WHERE issue_id = @issue_id;

-- ---- review requests ----

-- name: AddPullReviewRequest :execrows
INSERT INTO pull_review_requests (pull_id, reviewer_id) VALUES (@pull_id, @reviewer_id) ON CONFLICT DO NOTHING;

-- name: RemovePullReviewRequest :execrows
DELETE FROM pull_review_requests WHERE pull_id = @pull_id AND reviewer_id = @reviewer_id;

-- name: PullReviewRequestsOf :many
SELECT r.pull_id, r.reviewer_id FROM pull_review_requests r
WHERE r.pull_id = ANY(@pull_ids::uuid[])
ORDER BY r.created_at, r.reviewer_id;

-- ---- reviews ----

-- name: InsertPullReview :one
INSERT INTO pull_reviews (id, pull_id, repo_id, reviewer_id, state, body, commit_sha, submitted_at)
VALUES (@id, @pull_id, @repo_id, @reviewer_id, @state, @body, @commit_sha, @submitted_at)
RETURNING *;

-- name: GetPullReview :one
SELECT * FROM pull_reviews WHERE id = @id AND pull_id = @pull_id;

-- name: GetPendingPullReview :one
SELECT * FROM pull_reviews WHERE pull_id = @pull_id AND reviewer_id = @reviewer_id AND state = 'pending';

-- name: ListPullReviews :many
-- Submitted reviews plus the caller's own pending review.
SELECT r.* FROM pull_reviews r
WHERE r.pull_id = @pull_id AND (r.state <> 'pending' OR r.reviewer_id = @viewer_id)
ORDER BY r.created_at, r.id
LIMIT @lim OFFSET @off;

-- name: SubmitPullReview :one
UPDATE pull_reviews SET state = @state, body = @body, submitted_at = now(), updated_at = now()
WHERE id = @id AND state = 'pending'
RETURNING *;

-- name: DismissPullReview :one
UPDATE pull_reviews SET state = 'dismissed', dismissed_at = now(), dismissed_by = @dismissed_by,
  dismiss_message = @dismiss_message, updated_at = now()
WHERE id = @id AND state IN ('approved', 'changes_requested')
RETURNING *;

-- name: LatestPullReviews :many
-- The latest decisive review (approved, changes_requested or dismissed) of
-- each reviewer of a pull request.
SELECT DISTINCT ON (reviewer_id) reviewer_id, state, commit_sha
FROM pull_reviews
WHERE pull_id = @pull_id AND reviewer_id IS NOT NULL AND state IN ('approved', 'changes_requested', 'dismissed')
ORDER BY reviewer_id, submitted_at DESC NULLS LAST, created_at DESC;

-- ---- review comments ----

-- name: InsertPullReviewComment :one
INSERT INTO pull_review_comments (id, pull_id, repo_id, review_id, author_id, in_reply_to, path, side, line, start_line,
  original_line, original_start_line, original_commit_sha, commit_sha, diff_hunk, body)
VALUES (@id, @pull_id, @repo_id, @review_id, @author_id, @in_reply_to, @path, @side, @line, @start_line,
  @original_line, @original_start_line, @original_commit_sha, @commit_sha, @diff_hunk, @body)
RETURNING *;

-- name: GetPullReviewComment :one
SELECT c.* FROM pull_review_comments c
WHERE c.id = @id AND c.pull_id = @pull_id;

-- name: ListPullReviewComments :many
-- Comments of submitted reviews (or outside any review) plus the caller's
-- own pending ones, in thread order.
SELECT c.* FROM pull_review_comments c
LEFT JOIN pull_reviews r ON r.id = c.review_id
WHERE c.pull_id = @pull_id AND (r.id IS NULL OR r.state <> 'pending' OR r.reviewer_id = @viewer_id)
  AND (@review_id::uuid = '00000000-0000-0000-0000-000000000000'::uuid OR c.review_id = @review_id::uuid)
ORDER BY c.created_at, c.id
LIMIT @lim OFFSET @off;

-- name: ThreadCommentsOfPull :many
-- Root comments (not replies) of a pull request, for re-anchoring.
SELECT * FROM pull_review_comments WHERE pull_id = @pull_id AND in_reply_to IS NULL AND NOT outdated;

-- name: ReanchorPullReviewComment :exec
-- Moves a thread (root and replies) to a new position on commit_sha, or
-- marks it outdated (line NULL).
UPDATE pull_review_comments SET line = @line, start_line = @start_line, commit_sha = @commit_sha,
  outdated = @outdated, updated_at = now()
WHERE id = @id OR in_reply_to = @id;

-- name: SetPullThreadResolved :execrows
UPDATE pull_review_comments SET resolved_at = @resolved_at, resolved_by = @resolved_by, updated_at = now()
WHERE pull_id = @pull_id AND (id = @root_id OR in_reply_to = @root_id);
