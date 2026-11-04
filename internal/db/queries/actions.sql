-- Ervisio Actions (internal/actions, PLAN §6).

-- ---- runners ----

-- name: InsertActionsRunner :one
INSERT INTO actions_runners (id, name, scope, owner_id, repo_id, labels, raw_labels, version, ephemeral,
    credential_hash, online, last_seen_at, created_by)
VALUES (@id, @name, @scope, @owner_id, @repo_id, @labels, @raw_labels, @version, @ephemeral,
    @credential_hash, true, now(), @created_by)
RETURNING *;

-- name: GetActionsRunner :one
SELECT * FROM actions_runners WHERE id = @id;

-- name: GetActionsRunnerByCredential :one
SELECT * FROM actions_runners WHERE credential_hash = @credential_hash;

-- name: DeclareActionsRunner :one
UPDATE actions_runners
SET version = @version, labels = @labels, raw_labels = @raw_labels, online = true, last_seen_at = now(),
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: TouchActionsRunner :exec
UPDATE actions_runners SET online = true, last_seen_at = now() WHERE id = @id;

-- name: ListActionsRunners :many
-- The runners registered at one scope (owner_id and repo_id are NULL when
-- not part of the scope).
SELECT * FROM actions_runners
WHERE scope = @scope
  AND owner_id IS NOT DISTINCT FROM sqlc.narg('owner_id')::uuid
  AND repo_id IS NOT DISTINCT FROM sqlc.narg('repo_id')::uuid
ORDER BY name, id;

-- name: ListActionsRunnersForRepo :many
-- Every runner that can take jobs of a repository.
SELECT * FROM actions_runners
WHERE scope = 'instance' OR (scope = 'org' AND owner_id = @owner_id) OR (scope = 'repo' AND repo_id = @repo_id)
ORDER BY scope, name, id;

-- name: DeleteActionsRunner :execrows
DELETE FROM actions_runners WHERE id = @id;

-- name: MarkStaleActionsRunnersOffline :many
UPDATE actions_runners SET online = false
WHERE online AND (last_seen_at IS NULL OR last_seen_at < @before)
RETURNING id;

-- name: ActionsRunnerHasJobs :one
SELECT EXISTS (SELECT 1 FROM actions_jobs WHERE runner_id = @runner_id::uuid);

-- name: DeleteUsedEphemeralActionsRunners :execrows
-- Ephemeral runners whose single job ended and that went quiet.
DELETE FROM actions_runners rn
WHERE rn.ephemeral AND (rn.last_seen_at IS NULL OR rn.last_seen_at < @before)
  AND EXISTS (SELECT 1 FROM actions_jobs j WHERE j.runner_id = rn.id)
  AND NOT EXISTS (SELECT 1 FROM actions_jobs j WHERE j.runner_id = rn.id AND j.status = 'in_progress');

-- name: GetActionsNamespaceName :one
SELECT name FROM namespaces WHERE id = @id;

-- ---- registration tokens ----

-- name: InsertActionsRegistrationToken :one
INSERT INTO actions_registration_tokens (id, token_hash, scope, owner_id, repo_id, created_by, expires_at)
VALUES (@id, @token_hash, @scope, @owner_id, @repo_id, @created_by, @expires_at)
RETURNING *;

-- name: GetActionsRegistrationToken :one
SELECT * FROM actions_registration_tokens WHERE token_hash = @token_hash AND expires_at > now();

-- name: DeleteExpiredActionsRegistrationTokens :exec
DELETE FROM actions_registration_tokens WHERE expires_at <= now();

-- ---- runs ----

-- name: NextActionsRunNumber :one
INSERT INTO actions_run_numbers (repo_id, workflow_path, last) VALUES (@repo_id, @workflow_path, 1)
ON CONFLICT (repo_id, workflow_path) DO UPDATE SET last = actions_run_numbers.last + 1
RETURNING last;

-- name: InsertActionsRun :one
INSERT INTO actions_runs (id, repo_id, workflow_path, workflow_name, workflow, event, event_action, ref, sha, title,
    run_number, status, actor_id, trigger_actor_id, event_payload, concurrency_group, concurrency_cancel,
    head_repo_id, fork, needs_approval)
VALUES (@id, @repo_id, @workflow_path, @workflow_name, @workflow, @event, @event_action, @ref, @sha, @title,
    @run_number, @status, @actor_id, @trigger_actor_id, @event_payload, @concurrency_group, @concurrency_cancel,
    @head_repo_id, @fork, @needs_approval)
RETURNING *;

-- name: GetActionsRun :one
SELECT * FROM actions_runs WHERE id = @id;

-- name: GetActionsRunForUpdate :one
SELECT * FROM actions_runs WHERE id = @id FOR UPDATE;

-- name: ListActionsRuns :many
-- Runs of a repository, newest first, optionally filtered.
SELECT * FROM actions_runs
WHERE repo_id = @repo_id
  AND (@workflow_path::text = '' OR workflow_path = @workflow_path::text)
  AND (@event::text = '' OR event = @event::text)
  AND (@status::text = '' OR status = @status::text OR conclusion = @status::text)
  AND (@ref::text = '' OR ref = @ref::text)
  AND (@sha::text = '' OR sha = @sha::text)
ORDER BY created_at DESC, id DESC
LIMIT @lim OFFSET @off;

-- name: CountActionsRuns :one
SELECT count(*) FROM actions_runs
WHERE repo_id = @repo_id
  AND (@workflow_path::text = '' OR workflow_path = @workflow_path::text)
  AND (@event::text = '' OR event = @event::text)
  AND (@status::text = '' OR status = @status::text OR conclusion = @status::text)
  AND (@ref::text = '' OR ref = @ref::text)
  AND (@sha::text = '' OR sha = @sha::text);

-- name: ListActionsWorkflows :many
-- The workflow files that ever ran in a repository, with their last name.
SELECT DISTINCT ON (workflow_path) workflow_path, workflow_name, created_at
FROM actions_runs WHERE repo_id = @repo_id
ORDER BY workflow_path, created_at DESC;

-- name: LatestActionsRun :one
-- The latest completed run of a workflow on a ref (status badges).
SELECT * FROM actions_runs
WHERE repo_id = @repo_id AND workflow_path = @workflow_path AND ref = @ref AND status = 'completed'
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: SetActionsRunStatus :exec
UPDATE actions_runs
SET status = @status, conclusion = @conclusion,
    started_at = CASE WHEN @status::text = 'in_progress' THEN COALESCE(started_at, now()) ELSE started_at END,
    completed_at = CASE WHEN @status::text = 'completed' THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = @id;

-- name: StartActionsRunAttempt :exec
-- A re-run: the run goes back to queued under a new attempt.
UPDATE actions_runs
SET attempt = @attempt, status = 'queued', conclusion = '', started_at = NULL, completed_at = NULL,
    trigger_actor_id = @trigger_actor_id, updated_at = now()
WHERE id = @id;

-- name: ApproveActionsRun :exec
UPDATE actions_runs SET needs_approval = false, approved_by = @approved_by, updated_at = now() WHERE id = @id;

-- name: ListActiveActionsRunsInGroup :many
-- Runs of a concurrency group that are not completed, oldest first.
SELECT * FROM actions_runs
WHERE repo_id = @repo_id AND concurrency_group = @concurrency_group AND status <> 'completed' AND id <> @id
ORDER BY created_at, id;

-- name: ListStuckActionsRuns :many
-- Runs not completed and not touched since before: the sweeper advances
-- them again in case an event was lost.
SELECT id FROM actions_runs WHERE status <> 'completed' AND updated_at < @before ORDER BY updated_at LIMIT 100;

-- name: TouchActionsRun :exec
UPDATE actions_runs SET updated_at = now() WHERE id = @id;

-- ---- jobs ----

-- name: InsertActionsJob :one
INSERT INTO actions_jobs (id, run_id, repo_id, attempt, job_key, name, needs, matrix, payload, if_expr, runs_on,
    timeout_minutes, permissions, inputs, environment, status, conclusion, concurrency_group, concurrency_cancel,
    fail_fast, max_parallel, outputs, log_length, log_key, started_at, completed_at)
VALUES (@id, @run_id, @repo_id, @attempt, @job_key, @name, @needs, @matrix, @payload, @if_expr, @runs_on,
    @timeout_minutes, @permissions, @inputs, @environment, @status, @conclusion, @concurrency_group,
    @concurrency_cancel, @fail_fast, @max_parallel, @outputs, @log_length, @log_key, @started_at, @completed_at)
RETURNING *;

-- name: GetActionsJob :one
SELECT * FROM actions_jobs WHERE id = @id;

-- name: GetActionsJobForUpdate :one
SELECT * FROM actions_jobs WHERE id = @id FOR UPDATE;

-- name: ListActionsJobs :many
SELECT * FROM actions_jobs WHERE run_id = @run_id AND attempt = @attempt ORDER BY created_at, id;

-- name: ListActionsJobsForUpdate :many
SELECT * FROM actions_jobs WHERE run_id = @run_id AND attempt = @attempt ORDER BY created_at, id FOR UPDATE;

-- name: SetActionsJobStatus :exec
UPDATE actions_jobs SET status = @status, updated_at = now() WHERE id = @id AND status <> 'completed';

-- name: CompleteActionsJob :exec
-- The job ends: its token stops working at once.
UPDATE actions_jobs
SET status = 'completed', conclusion = @conclusion, completed_at = now(), updated_at = now(),
    token_expires_at = CASE WHEN token_hash IS NULL THEN NULL ELSE now() END
WHERE id = @id AND status <> 'completed';

-- name: ClaimActionsJob :one
-- Takes the oldest queued job a runner can run: every runs-on label among
-- the runner labels, the repository inside the runner scope and the matrix
-- max-parallel not reached.
WITH candidate AS (
    SELECT j.id FROM actions_jobs j
    JOIN repos r ON r.id = j.repo_id
    WHERE j.status = 'queued'
      AND j.runs_on <@ @labels::text[]
      AND (@scope::text = 'instance'
           OR (@scope::text = 'org' AND r.owner_id = @owner_id::uuid)
           OR (@scope::text = 'repo' AND j.repo_id = @repo_id::uuid))
      AND (j.max_parallel = 0 OR (
          SELECT count(*) FROM actions_jobs s
          WHERE s.run_id = j.run_id AND s.attempt = j.attempt AND s.job_key = j.job_key AND s.status = 'in_progress'
      ) < j.max_parallel)
    ORDER BY j.created_at, j.id
    LIMIT 1
    FOR UPDATE OF j SKIP LOCKED
)
UPDATE actions_jobs j
SET status = 'in_progress', runner_id = @runner_id, label = COALESCE(j.runs_on[1], ''), started_at = now(),
    last_update_at = now(), updated_at = now(), token_hash = @token_hash,
    token_expires_at = now() + make_interval(mins => j.timeout_minutes + 30)
FROM candidate
WHERE j.id = candidate.id
RETURNING j.*;

-- name: ReadyActionsJob :execrows
-- A blocked job whose needs are met becomes concrete: the evaluated
-- matrix combination, runs-on, environment and the single-job payload.
UPDATE actions_jobs
SET status = @status, name = @name, matrix = @matrix, payload = @payload, runs_on = @runs_on,
    environment = @environment, concurrency_group = @concurrency_group, concurrency_cancel = @concurrency_cancel,
    timeout_minutes = @timeout_minutes, fail_fast = @fail_fast, max_parallel = @max_parallel, inputs = @inputs,
    updated_at = now()
WHERE id = @id AND status = 'blocked';

-- name: RequestActionsJobCancel :exec
UPDATE actions_jobs SET cancel_requested = true, updated_at = now() WHERE id = @id AND status <> 'completed';

-- name: ListActionsJobsToCancel :many
-- Of the jobs a runner reports as running, the ones it must stop.
SELECT id FROM actions_jobs
WHERE id = ANY(@ids::uuid[]) AND (runner_id IS DISTINCT FROM @runner_id OR cancel_requested OR status = 'completed');

-- name: MergeActionsJobOutputs :one
-- Adds outputs; the first value of each key is kept.
UPDATE actions_jobs SET outputs = @outputs::jsonb || outputs, last_update_at = now(), updated_at = now()
WHERE id = @id
RETURNING outputs;

-- name: TouchActionsJob :exec
UPDATE actions_jobs SET last_update_at = now() WHERE id = @id;

-- name: SetActionsJobLogLength :exec
UPDATE actions_jobs SET log_length = @log_length, last_update_at = now() WHERE id = @id;

-- name: SetActionsJobLogKey :exec
UPDATE actions_jobs SET log_key = @log_key WHERE id = @id;

-- name: SetActionsJobCheckRun :exec
UPDATE actions_jobs SET check_run_id = @check_run_id WHERE id = @id;

-- name: ApproveActionsJob :exec
UPDATE actions_jobs SET approved_by = @approved_by, approved_at = now(), updated_at = now() WHERE id = @id;

-- name: GetActionsJobByToken :one
-- The running job of a job token, with what the token principal needs.
SELECT j.id, j.run_id, j.repo_id, j.permissions, j.token_expires_at, r.actor_id, r.trigger_actor_id, r.fork,
    COALESCE(n.name, '')::text AS actor_name
FROM actions_jobs j
JOIN actions_runs r ON r.id = j.run_id
LEFT JOIN namespaces n ON n.id = COALESCE(r.trigger_actor_id, r.actor_id)
WHERE j.token_hash = @token_hash AND j.status = 'in_progress' AND j.token_expires_at > now();

-- name: ListVanishedActionsJobs :many
-- Running jobs whose runner is gone or silent since before.
SELECT j.id, j.run_id FROM actions_jobs j
LEFT JOIN actions_runners rn ON rn.id = j.runner_id
WHERE j.status = 'in_progress'
  AND (rn.id IS NULL OR NOT rn.online OR COALESCE(j.last_update_at, j.started_at) < @before)
LIMIT 200;

-- name: ListTimedOutActionsJobs :many
SELECT id, run_id FROM actions_jobs
WHERE status = 'in_progress' AND started_at + make_interval(mins => timeout_minutes) < now()
LIMIT 200;

-- name: ListUnarchivedActionsJobs :many
-- Completed jobs whose log is still in Postgres.
SELECT j.id FROM actions_jobs j
WHERE j.status = 'completed' AND j.log_key = '' AND j.completed_at < @before
  AND EXISTS (SELECT 1 FROM actions_log_rows l WHERE l.job_id = j.id)
LIMIT 100;

-- name: ListActiveActionsJobsInGroup :many
-- Jobs of a concurrency group that hold or wait for it.
SELECT * FROM actions_jobs
WHERE repo_id = @repo_id AND concurrency_group = @concurrency_group AND status IN ('waiting', 'queued', 'in_progress')
  AND id <> @id
ORDER BY created_at, id;

-- name: ListWaitingActionsJobsInGroup :many
SELECT id, run_id FROM actions_jobs
WHERE repo_id = @repo_id AND concurrency_group = @concurrency_group AND status = 'waiting'
ORDER BY created_at, id;

-- name: ListRunningActionsJobsOfRunner :many
SELECT id, run_id FROM actions_jobs WHERE runner_id = @runner_id AND status = 'in_progress';

-- ---- steps ----

-- name: UpsertActionsStep :exec
INSERT INTO actions_steps (job_id, idx, name, status, conclusion, log_index, log_length, started_at, completed_at)
VALUES (@job_id, @idx, @name, @status, @conclusion, @log_index, @log_length, @started_at, @completed_at)
ON CONFLICT (job_id, idx) DO UPDATE
SET name = EXCLUDED.name, status = EXCLUDED.status, conclusion = EXCLUDED.conclusion,
    log_index = EXCLUDED.log_index, log_length = EXCLUDED.log_length,
    started_at = EXCLUDED.started_at, completed_at = EXCLUDED.completed_at;

-- name: ListActionsSteps :many
SELECT * FROM actions_steps WHERE job_id = @job_id ORDER BY idx;

-- name: CompleteOpenActionsSteps :exec
-- Steps left open when a job ends are closed with the job.
UPDATE actions_steps
SET status = 'completed', conclusion = CASE WHEN status = 'queued' THEN 'skipped' ELSE @conclusion END,
    completed_at = COALESCE(completed_at, now())
WHERE job_id = @job_id AND status <> 'completed';

-- ---- logs ----

-- name: InsertActionsLogRows :exec
INSERT INTO actions_log_rows (job_id, idx, at, content)
SELECT @job_id::uuid, unnest(@idxs::bigint[]), unnest(@ats::timestamptz[]), unnest(@contents::text[])
ON CONFLICT (job_id, idx) DO NOTHING;

-- name: ListActionsLogRows :many
SELECT idx, at, content FROM actions_log_rows
WHERE job_id = @job_id AND idx >= @from_idx
ORDER BY idx
LIMIT @lim;

-- name: DeleteActionsLogRows :exec
DELETE FROM actions_log_rows WHERE job_id = @job_id;

-- name: NotifyActions :exec
SELECT pg_notify(@channel::text, @payload::text);

-- name: BumpActionsTasksVersion :one
UPDATE actions_tasks_version SET version = version + 1 WHERE id RETURNING version;

-- name: GetActionsTasksVersion :one
SELECT version FROM actions_tasks_version WHERE id;

-- ---- secrets and variables ----

-- name: UpsertActionsSecret :one
INSERT INTO actions_secrets (id, scope, owner_id, repo_id, environment, name, sealed, created_by)
VALUES (@id, @scope, @owner_id, @repo_id, @environment, @name, @sealed, @created_by)
ON CONFLICT (scope, scope_id, environment, name) DO UPDATE
SET sealed = EXCLUDED.sealed, updated_at = now()
RETURNING id, scope, owner_id, repo_id, environment, name, created_at, updated_at;

-- name: ListActionsSecretNames :many
SELECT id, scope, owner_id, repo_id, environment, name, created_at, updated_at FROM actions_secrets
WHERE scope = @scope AND scope_id = @scope_id AND environment = @environment
ORDER BY name;

-- name: DeleteActionsSecret :execrows
DELETE FROM actions_secrets
WHERE scope = @scope AND scope_id = @scope_id AND environment = @environment AND name = @name;

-- name: ListActionsSecretsForJob :many
-- Every secret visible to a job; the caller resolves the precedence.
SELECT * FROM actions_secrets
WHERE scope = 'instance'
   OR (scope = 'org' AND owner_id = @owner_id)
   OR (scope = 'repo' AND repo_id = @repo_id)
   OR (scope = 'environment' AND repo_id = @repo_id AND environment = @environment);

-- name: UpsertActionsVariable :one
INSERT INTO actions_variables (id, scope, owner_id, repo_id, environment, name, value, created_by)
VALUES (@id, @scope, @owner_id, @repo_id, @environment, @name, @value, @created_by)
ON CONFLICT (scope, scope_id, environment, name) DO UPDATE
SET value = EXCLUDED.value, updated_at = now()
RETURNING *;

-- name: ListActionsVariables :many
SELECT * FROM actions_variables
WHERE scope = @scope AND scope_id = @scope_id AND environment = @environment
ORDER BY name;

-- name: DeleteActionsVariable :execrows
DELETE FROM actions_variables
WHERE scope = @scope AND scope_id = @scope_id AND environment = @environment AND name = @name;

-- name: ListActionsVariablesForJob :many
SELECT * FROM actions_variables
WHERE scope = 'instance'
   OR (scope = 'org' AND owner_id = @owner_id)
   OR (scope = 'repo' AND repo_id = @repo_id)
   OR (scope = 'environment' AND repo_id = @repo_id AND environment = @environment);

-- ---- environments ----

-- name: UpsertActionsEnvironment :one
INSERT INTO actions_environments (id, repo_id, name, require_approval, reviewers)
VALUES (@id, @repo_id, @name, @require_approval, @reviewers)
ON CONFLICT (repo_id, name) DO UPDATE
SET require_approval = EXCLUDED.require_approval, reviewers = EXCLUDED.reviewers, updated_at = now()
RETURNING *;

-- name: GetActionsEnvironment :one
SELECT * FROM actions_environments WHERE repo_id = @repo_id AND name = @name;

-- name: ListActionsEnvironments :many
SELECT * FROM actions_environments WHERE repo_id = @repo_id ORDER BY name;

-- name: DeleteActionsEnvironment :execrows
DELETE FROM actions_environments WHERE repo_id = @repo_id AND name = @name;

-- ---- schedules ----

-- name: DeleteActionsSchedules :exec
DELETE FROM actions_schedules WHERE repo_id = @repo_id;

-- name: InsertActionsSchedule :exec
INSERT INTO actions_schedules (id, repo_id, workflow_path, cron, next_at)
VALUES (@id, @repo_id, @workflow_path, @cron, @next_at)
ON CONFLICT (repo_id, workflow_path, cron) DO NOTHING;

-- name: ClaimDueActionsSchedules :many
SELECT * FROM actions_schedules WHERE next_at <= now() ORDER BY next_at LIMIT 100 FOR UPDATE SKIP LOCKED;

-- name: SetActionsScheduleNext :exec
UPDATE actions_schedules SET next_at = @next_at WHERE id = @id;

-- name: LockActionsGroup :exec
-- Serializes the concurrency decisions of one group (or one repository's
-- schedules) for the rest of the transaction.
SELECT pg_advisory_xact_lock(hashtextextended(@key::text, 0));

-- name: ListenActionsTasks :exec
-- Runners long-polling FetchTask wait on this channel.
LISTEN ervisio_actions_tasks;

-- name: ListenActionsLogs :exec
-- Log followers wait on this channel ("<job id>:<length>").
LISTEN ervisio_actions_logs;
