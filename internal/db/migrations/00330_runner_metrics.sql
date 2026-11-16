-- +goose Up
-- Latest resource usage reported by each runner (Declare/Heartbeat). Only
-- the last report is kept, no history.

ALTER TABLE actions_runners
    ADD COLUMN metrics    jsonb,
    ADD COLUMN metrics_at timestamptz;

-- +goose Down
ALTER TABLE actions_runners DROP COLUMN metrics_at, DROP COLUMN metrics;
