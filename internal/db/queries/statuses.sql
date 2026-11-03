-- Commit statuses, check suites and check runs (PLAN §5).

-- name: InsertCommitStatus :one
INSERT INTO commit_statuses (id, repo_id, sha, state, context, description, target_url, creator_id)
VALUES (@id, @repo_id, @sha, @state, @context, @description, @target_url, @creator_id)
RETURNING *;

-- name: CountCommitStatuses :one
SELECT count(*) FROM commit_statuses WHERE repo_id = @repo_id AND sha = @sha AND context = @context;

-- name: ListCommitStatuses :many
-- Every status of a commit, newest first, with the creator's name.
SELECT s.*, COALESCE(n.name, '')::text AS creator_name
FROM commit_statuses s
LEFT JOIN namespaces n ON n.id = s.creator_id
WHERE s.repo_id = @repo_id AND s.sha = @sha
ORDER BY s.created_at DESC, s.id DESC
LIMIT @lim OFFSET @off;

-- name: LatestCommitStatuses :many
-- The latest status of each context of a commit.
SELECT DISTINCT ON (s.context) s.*, COALESCE(n.name, '')::text AS creator_name
FROM commit_statuses s
LEFT JOIN namespaces n ON n.id = s.creator_id
WHERE s.repo_id = @repo_id AND s.sha = @sha
ORDER BY s.context, s.created_at DESC, s.id DESC;

-- name: UpsertCheckSuite :one
-- The suite of (commit, app), created queued when missing.
INSERT INTO check_suites (id, repo_id, head_sha, head_branch, app)
VALUES (@id, @repo_id, @head_sha, @head_branch, @app)
ON CONFLICT (repo_id, head_sha, app) DO UPDATE
    SET head_branch = CASE WHEN check_suites.head_branch = '' THEN EXCLUDED.head_branch ELSE check_suites.head_branch END
RETURNING *;

-- name: GetCheckSuite :one
SELECT * FROM check_suites WHERE id = @id AND repo_id = @repo_id;

-- name: GetCheckSuiteForUpdate :one
SELECT * FROM check_suites WHERE id = @id FOR UPDATE;

-- name: ListCheckSuites :many
SELECT * FROM check_suites
WHERE repo_id = @repo_id AND head_sha = @head_sha
  AND (@app::text = '' OR app = @app::text)
ORDER BY created_at, id
LIMIT @lim OFFSET @off;

-- name: CountCheckSuites :one
SELECT count(*) FROM check_suites
WHERE repo_id = @repo_id AND head_sha = @head_sha
  AND (@app::text = '' OR app = @app::text);

-- name: UpdateCheckSuiteState :exec
UPDATE check_suites SET status = @status, conclusion = @conclusion, updated_at = now()
WHERE id = @id;

-- name: InsertCheckRun :one
INSERT INTO check_runs (id, suite_id, repo_id, head_sha, name, status, conclusion, external_id, details_url,
    output_title, output_summary, output_text, annotations, creator_id, started_at, completed_at)
VALUES (@id, @suite_id, @repo_id, @head_sha, @name, @status, @conclusion, @external_id, @details_url,
    @output_title, @output_summary, @output_text, @annotations, @creator_id, @started_at, @completed_at)
RETURNING id;

-- name: GetCheckRun :one
SELECT r.*, s.app, s.head_branch
FROM check_runs r JOIN check_suites s ON s.id = r.suite_id
WHERE r.id = @id AND r.repo_id = @repo_id;

-- name: GetCheckRunForUpdate :one
SELECT * FROM check_runs WHERE id = @id AND repo_id = @repo_id FOR UPDATE;

-- name: UpdateCheckRun :exec
UPDATE check_runs SET name = @name, status = @status, conclusion = @conclusion, external_id = @external_id,
    details_url = @details_url, output_title = @output_title, output_summary = @output_summary,
    output_text = @output_text, annotations = @annotations, started_at = @started_at,
    completed_at = @completed_at, updated_at = now()
WHERE id = @id;

-- name: ListCheckRuns :many
-- The check runs of a commit (or of a suite), filtered by name and status.
-- latest_only keeps the newest run of each name in its suite.
SELECT r.*, s.app, s.head_branch
FROM check_runs r JOIN check_suites s ON s.id = r.suite_id
WHERE r.repo_id = @repo_id
  AND (@head_sha::text = '' OR r.head_sha = @head_sha::text)
  AND (NOT @by_suite::bool OR r.suite_id = @suite_id::uuid)
  AND (@name::text = '' OR r.name = @name::text)
  AND (@status::text = '' OR r.status = @status::text)
  AND (@app::text = '' OR s.app = @app::text)
  AND (NOT @latest_only::bool OR NOT EXISTS (
      SELECT 1 FROM check_runs o
      WHERE o.suite_id = r.suite_id AND o.name = r.name
        AND (o.created_at, o.id) > (r.created_at, r.id)))
ORDER BY r.created_at, r.id
LIMIT @lim OFFSET @off;

-- name: CountCheckRuns :one
SELECT count(*)
FROM check_runs r JOIN check_suites s ON s.id = r.suite_id
WHERE r.repo_id = @repo_id
  AND (@head_sha::text = '' OR r.head_sha = @head_sha::text)
  AND (NOT @by_suite::bool OR r.suite_id = @suite_id::uuid)
  AND (@name::text = '' OR r.name = @name::text)
  AND (@status::text = '' OR r.status = @status::text)
  AND (@app::text = '' OR s.app = @app::text)
  AND (NOT @latest_only::bool OR NOT EXISTS (
      SELECT 1 FROM check_runs o
      WHERE o.suite_id = r.suite_id AND o.name = r.name
        AND (o.created_at, o.id) > (r.created_at, r.id)));

-- name: SuiteRunStates :many
-- Status and conclusion of the latest run of each name in a suite.
SELECT DISTINCT ON (name) status, conclusion
FROM check_runs WHERE suite_id = @suite_id
ORDER BY name, created_at DESC, id DESC;

-- name: CountSuiteRuns :many
-- Number of distinct run names of each suite.
SELECT suite_id, count(DISTINCT name)::bigint AS runs
FROM check_runs WHERE suite_id = ANY(@suite_ids::uuid[])
GROUP BY suite_id;

-- name: LatestCheckRunStates :many
-- The latest run of each name on a commit, across suites, for protection.
SELECT DISTINCT ON (name) name, status, conclusion
FROM check_runs WHERE repo_id = @repo_id AND head_sha = @head_sha
ORDER BY name, created_at DESC, id DESC;
