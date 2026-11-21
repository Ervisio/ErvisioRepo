-- +goose Up
-- Results of runs not tied to a release (artifacts and archived job logs)
-- are deleted after actions.unreleased_retention (default 24h). A run counts
-- as released when it was triggered by a release, ran on a tag, or a release
-- points at its commit. log_expired_at records that a job log was deleted
-- by that rule, so the UI can say so instead of showing an empty log.

ALTER TABLE actions_jobs ADD COLUMN log_expired_at timestamptz;

CREATE INDEX actions_jobs_log_retention ON actions_jobs (completed_at)
    WHERE status = 'completed' AND log_expired_at IS NULL;

CREATE INDEX actions_artifacts_created ON actions_artifacts (created_at) WHERE finalized;

CREATE INDEX releases_repo_commit ON releases (repo_id, commit_sha) WHERE commit_sha <> '';

-- +goose Down
DROP INDEX releases_repo_commit;
DROP INDEX actions_artifacts_created;
DROP INDEX actions_jobs_log_retention;
ALTER TABLE actions_jobs DROP COLUMN log_expired_at;
