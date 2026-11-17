-- +goose Up
-- The admin overview counts completed jobs per hour over the last week.
CREATE INDEX actions_jobs_completed ON actions_jobs (completed_at) WHERE status = 'completed';
