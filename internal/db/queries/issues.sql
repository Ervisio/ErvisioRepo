-- ---- numbering (shared by issues and pull requests) ----

-- name: NextIssueNumber :one
-- Allocates the next number of a repository. The counter row stays locked
-- until the calling transaction ends, which serializes number allocation.
INSERT INTO issue_counters (repo_id, last_number) VALUES (@repo_id, 1)
ON CONFLICT (repo_id) DO UPDATE SET last_number = issue_counters.last_number + 1
RETURNING last_number;

-- ---- issues ----

-- name: InsertIssue :one
INSERT INTO issues (id, repo_id, number, kind, title, body, author_id, milestone_id)
VALUES (@id, @repo_id, @number, @kind, @title, @body, @author_id, @milestone_id)
RETURNING *;

-- name: GetIssueByNumber :one
SELECT * FROM issues WHERE repo_id = @repo_id AND number = @number;

-- name: GetIssueByID :one
SELECT * FROM issues WHERE id = @id;

-- name: GetIssueForUpdate :one
SELECT * FROM issues WHERE repo_id = @repo_id AND number = @number FOR UPDATE;

-- name: GetIssuesByNumbers :many
SELECT * FROM issues WHERE repo_id = @repo_id AND number = ANY(@numbers::bigint[]);

-- name: UpdateIssueContent :one
UPDATE issues SET title = @title, body = @body, edited_at = @edited_at, edited_by = @edited_by, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: SetIssueState :one
UPDATE issues SET state = @state, state_reason = @state_reason, closed_at = @closed_at, closed_by = @closed_by, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: SetIssueLock :one
UPDATE issues SET locked = @locked, lock_reason = @lock_reason, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: SetIssueMilestone :one
UPDATE issues SET milestone_id = @milestone_id, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: TouchIssue :exec
UPDATE issues SET updated_at = now() WHERE id = @id;

-- name: AddIssueCommentCount :exec
UPDATE issues SET comment_count = greatest(comment_count + @delta::integer, 0), updated_at = now() WHERE id = @id;

-- name: SearchIssues :many
-- Issues of a repository matching every given filter. Modes: milestone_mode
-- and assignee_mode are '' (no filter), 'none', 'any' or 'id'. query is a
-- websearch_to_tsquery text matched against title, body and comments.
-- sort is created, updated, comments or relevance.
SELECT i.* FROM issues i
WHERE i.repo_id = @repo_id AND i.kind = @kind
  AND (@state::text = '' OR i.state = @state::text)
  AND (cardinality(@label_ids::uuid[]) = 0 OR
       (SELECT count(*) FROM issue_labels il WHERE il.issue_id = i.id AND il.label_id = ANY(@label_ids::uuid[])) = cardinality(@label_ids::uuid[]))
  AND (CASE @milestone_mode::text
         WHEN 'none' THEN i.milestone_id IS NULL
         WHEN 'any' THEN i.milestone_id IS NOT NULL
         WHEN 'id' THEN i.milestone_id = @milestone_id::uuid
         ELSE true END)
  AND (CASE @assignee_mode::text
         WHEN 'none' THEN NOT EXISTS (SELECT 1 FROM issue_assignees a WHERE a.issue_id = i.id)
         WHEN 'any' THEN EXISTS (SELECT 1 FROM issue_assignees a WHERE a.issue_id = i.id)
         WHEN 'id' THEN EXISTS (SELECT 1 FROM issue_assignees a WHERE a.issue_id = i.id AND a.user_id = @assignee_id::uuid)
         ELSE true END)
  AND (NOT @by_author::boolean OR i.author_id = @author_id::uuid)
  AND (NOT @by_mention::boolean OR EXISTS (
         SELECT 1 FROM issue_references r
         WHERE r.source_issue_id = i.id AND r.target_kind = 'user' AND r.target_user_id = @mentioned_id::uuid))
  AND (@query::text = '' OR
       issue_search_vector(i.title, i.body) @@ websearch_to_tsquery('simple', @query::text) OR
       EXISTS (SELECT 1 FROM issue_comments c WHERE c.issue_id = i.id
               AND issue_comment_search_vector(c.body) @@ websearch_to_tsquery('simple', @query::text)))
ORDER BY
  CASE WHEN @sort::text = 'relevance' THEN ts_rank(issue_search_vector(i.title, i.body), websearch_to_tsquery('simple', @query::text)) END DESC NULLS LAST,
  CASE WHEN @sort::text = 'created' AND @ascending::boolean THEN i.created_at END ASC,
  CASE WHEN @sort::text = 'created' AND NOT @ascending::boolean THEN i.created_at END DESC,
  CASE WHEN @sort::text = 'updated' AND @ascending::boolean THEN i.updated_at END ASC,
  CASE WHEN @sort::text = 'updated' AND NOT @ascending::boolean THEN i.updated_at END DESC,
  CASE WHEN @sort::text = 'comments' AND @ascending::boolean THEN i.comment_count END ASC,
  CASE WHEN @sort::text = 'comments' AND NOT @ascending::boolean THEN i.comment_count END DESC,
  i.number DESC
LIMIT @lim OFFSET @off;

-- ---- labels ----

-- name: ListLabels :many
SELECT * FROM labels WHERE repo_id = @repo_id ORDER BY lower(name);

-- name: GetLabelByName :one
SELECT * FROM labels WHERE repo_id = @repo_id AND lower(name) = lower(@name::text);

-- name: ListLabelsByNames :many
SELECT * FROM labels WHERE repo_id = @repo_id AND lower(name) = ANY(@names::text[]);

-- name: InsertLabel :one
INSERT INTO labels (id, repo_id, name, color, description)
VALUES (@id, @repo_id, @name, @color, @description)
RETURNING *;

-- name: UpdateLabel :one
UPDATE labels SET name = @name, color = @color, description = @description, updated_at = now()
WHERE id = @id AND repo_id = @repo_id
RETURNING *;

-- name: DeleteLabel :execrows
DELETE FROM labels WHERE id = @id AND repo_id = @repo_id;

-- name: IssueLabelsOf :many
SELECT il.issue_id, l.id, l.name, l.color, l.description
FROM issue_labels il JOIN labels l ON l.id = il.label_id
WHERE il.issue_id = ANY(@issue_ids::uuid[])
ORDER BY lower(l.name);

-- name: AddIssueLabel :execrows
INSERT INTO issue_labels (issue_id, label_id) VALUES (@issue_id, @label_id) ON CONFLICT DO NOTHING;

-- name: RemoveIssueLabel :execrows
DELETE FROM issue_labels WHERE issue_id = @issue_id AND label_id = @label_id;

-- ---- milestones ----

-- name: ListMilestones :many
SELECT m.*,
  (SELECT count(*) FROM issues i WHERE i.milestone_id = m.id AND i.state = 'open')::bigint AS open_issues,
  (SELECT count(*) FROM issues i WHERE i.milestone_id = m.id AND i.state = 'closed')::bigint AS closed_issues
FROM milestones m
WHERE m.repo_id = @repo_id AND (@state::text = '' OR m.state = @state::text)
ORDER BY m.due_on ASC NULLS LAST, lower(m.title)
LIMIT @lim OFFSET @off;

-- name: GetMilestonesByIDs :many
SELECT m.*,
  (SELECT count(*) FROM issues i WHERE i.milestone_id = m.id AND i.state = 'open')::bigint AS open_issues,
  (SELECT count(*) FROM issues i WHERE i.milestone_id = m.id AND i.state = 'closed')::bigint AS closed_issues
FROM milestones m
WHERE m.repo_id = @repo_id AND m.id = ANY(@ids::uuid[]);

-- name: InsertMilestone :one
INSERT INTO milestones (id, repo_id, title, description, due_on)
VALUES (@id, @repo_id, @title, @description, @due_on)
RETURNING *;

-- name: UpdateMilestone :one
UPDATE milestones SET title = @title, description = @description, due_on = @due_on, state = @state,
  closed_at = CASE WHEN @state::text = 'closed' THEN coalesce(closed_at, now()) ELSE NULL END,
  updated_at = now()
WHERE id = @id AND repo_id = @repo_id
RETURNING *;

-- name: DeleteMilestone :execrows
DELETE FROM milestones WHERE id = @id AND repo_id = @repo_id;

-- ---- assignees and users ----

-- name: IssueAssigneesOf :many
SELECT a.issue_id, a.user_id, n.name
FROM issue_assignees a JOIN namespaces n ON n.id = a.user_id
WHERE a.issue_id = ANY(@issue_ids::uuid[])
ORDER BY n.name_lower;

-- name: AddIssueAssignee :execrows
INSERT INTO issue_assignees (issue_id, user_id) VALUES (@issue_id, @user_id) ON CONFLICT DO NOTHING;

-- name: RemoveIssueAssignee :execrows
DELETE FROM issue_assignees WHERE issue_id = @issue_id AND user_id = @user_id;

-- name: IssueUsersByNames :many
-- Active users among the given lowercase names.
SELECT n.id, n.name FROM namespaces n JOIN users u ON u.id = n.id
WHERE n.name_lower = ANY(@names::text[]) AND u.suspended_at IS NULL;

-- name: IssueUserNames :many
SELECT n.id, n.name FROM namespaces n WHERE n.id = ANY(@ids::uuid[]);

-- ---- comments and edit history ----

-- name: InsertIssueComment :one
INSERT INTO issue_comments (id, issue_id, repo_id, author_id, body)
VALUES (@id, @issue_id, @repo_id, @author_id, @body)
RETURNING *;

-- name: GetIssueComment :one
SELECT * FROM issue_comments WHERE id = @id AND repo_id = @repo_id;

-- name: GetIssueCommentsByIDs :many
SELECT * FROM issue_comments WHERE id = ANY(@ids::uuid[]);

-- name: ListIssueComments :many
SELECT * FROM issue_comments WHERE issue_id = @issue_id ORDER BY created_at, id LIMIT @lim OFFSET @off;

-- name: UpdateIssueCommentBody :one
UPDATE issue_comments SET body = @body, edited_at = now(), edited_by = @edited_by, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: DeleteIssueComment :execrows
DELETE FROM issue_comments WHERE id = @id AND repo_id = @repo_id;

-- name: InsertIssueEdit :exec
INSERT INTO issue_edits (id, issue_id, comment_id, editor_id, body)
VALUES (@id, @issue_id, @comment_id, @editor_id, @body);

-- name: ListIssueEdits :many
-- Previous bodies of an issue (comment_id NULL) or of a comment, newest first.
SELECT * FROM issue_edits
WHERE issue_id = @issue_id AND comment_id IS NOT DISTINCT FROM @comment_id
ORDER BY created_at DESC, id DESC
LIMIT @lim OFFSET @off;

-- ---- reactions ----

-- name: InsertIssueReaction :execrows
INSERT INTO issue_reactions (id, issue_id, comment_id, user_id, content)
VALUES (@id, @issue_id, @comment_id, @user_id, @content)
ON CONFLICT DO NOTHING;

-- name: DeleteIssueReaction :execrows
DELETE FROM issue_reactions
WHERE issue_id = @issue_id AND comment_id IS NOT DISTINCT FROM @comment_id AND user_id = @user_id AND content = @content;

-- name: ListIssueReactions :many
SELECT r.id, r.content, r.user_id, n.name, r.created_at
FROM issue_reactions r JOIN namespaces n ON n.id = r.user_id
WHERE r.issue_id = @issue_id AND r.comment_id IS NOT DISTINCT FROM @comment_id
ORDER BY r.created_at, r.id
LIMIT @lim OFFSET @off;

-- name: IssueReactionGroups :many
-- Reaction counts on issues (not on their comments).
SELECT r.issue_id, r.content, count(*)::bigint AS total, bool_or(r.user_id = @viewer::uuid) AS viewer_reacted
FROM issue_reactions r
WHERE r.issue_id = ANY(@issue_ids::uuid[]) AND r.comment_id IS NULL
GROUP BY r.issue_id, r.content;

-- name: CommentReactionGroups :many
SELECT r.comment_id::uuid AS comment_id, r.content, count(*)::bigint AS total, bool_or(r.user_id = @viewer::uuid) AS viewer_reacted
FROM issue_reactions r
WHERE r.comment_id = ANY(@comment_ids::uuid[])
GROUP BY r.comment_id, r.content;

-- ---- timeline ----

-- name: InsertIssueEvent :exec
INSERT INTO issue_events (id, repo_id, issue_id, actor_id, event, comment_id, data)
VALUES (@id, @repo_id, @issue_id, @actor_id, @event, @comment_id, @data);

-- name: ListIssueEvents :many
SELECT * FROM issue_events WHERE issue_id = @issue_id ORDER BY created_at, id LIMIT @lim OFFSET @off;

-- ---- cross-references ----

-- name: InsertIssueReference :execrows
INSERT INTO issue_references (id, source_key, source_repo_id, source_issue_id, source_comment_id, source_commit,
  target_kind, target_key, target_issue_id, target_user_id, target_commit, actor_id)
VALUES (@id, @source_key, @source_repo_id, @source_issue_id, @source_comment_id, @source_commit,
  @target_kind, @target_key, @target_issue_id, @target_user_id, @target_commit, @actor_id)
ON CONFLICT DO NOTHING;

-- name: ListIssueReferencesFrom :many
-- References written in an issue and its comments.
SELECT * FROM issue_references WHERE source_issue_id = @issue_id ORDER BY created_at, id;

-- ---- attachments ----

-- name: InsertIssueAttachment :one
INSERT INTO issue_attachments (id, repo_id, uploader_id, name, content_type, size, sha256, storage_policy, storage_key)
VALUES (@id, @repo_id, @uploader_id, @name, @content_type, @size, @sha256, @storage_policy, @storage_key)
RETURNING *;

-- name: GetIssueAttachment :one
SELECT * FROM issue_attachments WHERE id = @id AND repo_id = @repo_id;

-- name: DeleteIssueAttachment :one
DELETE FROM issue_attachments WHERE id = @id AND repo_id = @repo_id RETURNING *;

-- name: SumRepoIssueAttachmentBytes :one
-- Bytes held by the issue attachments of a repository (released from the
-- owner quota when the repository is deleted).
SELECT COALESCE(sum(size), 0)::bigint FROM issue_attachments WHERE repo_id = @repo_id;

-- name: ListOrphanIssueAttachmentObjects :many
-- Objects under "<policy>/attachments/" that no attachment row references
-- and that are older than "before" (an upload in progress has no row yet).
SELECT so.target, so.key, so.policy FROM storage_objects so
WHERE so.policy = ANY(@policies::text[])
  AND so.key LIKE so.policy || '/attachments/%'
  AND so.created_at < @before::timestamptz
  AND NOT EXISTS (SELECT 1 FROM issue_attachments a WHERE a.storage_policy || '/' || a.storage_key = so.key)
ORDER BY so.created_at
LIMIT @lim;
