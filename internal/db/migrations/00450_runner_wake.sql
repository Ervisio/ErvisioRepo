-- +goose Up
-- On-demand runners: a machine that is switched off while idle and started
-- by an HTTP call (e.g. an AWS Lambda function URL) when a queued job needs
-- it. wake_token is sealed with the master key; wake_next_at is the earliest
-- time of the next call (cooldown after a call, short retry after a failure).

ALTER TABLE actions_runners
    ADD COLUMN wake_enabled  boolean     NOT NULL DEFAULT false,
    ADD COLUMN wake_url      text        NOT NULL DEFAULT '' CHECK (length(wake_url) <= 2048),
    ADD COLUMN wake_header   text        NOT NULL DEFAULT '' CHECK (length(wake_header) <= 128),
    ADD COLUMN wake_token    bytea,
    ADD COLUMN wake_cooldown integer     NOT NULL DEFAULT 180 CHECK (wake_cooldown BETWEEN 30 AND 3600),
    ADD COLUMN wake_next_at  timestamptz,
    ADD COLUMN last_wake_at  timestamptz,
    ADD COLUMN wake_status   integer     NOT NULL DEFAULT 0,
    ADD COLUMN wake_error    text        NOT NULL DEFAULT '';

CREATE INDEX actions_runners_wake ON actions_runners (wake_next_at) WHERE wake_enabled;

-- +goose Down
DROP INDEX actions_runners_wake;
ALTER TABLE actions_runners
    DROP COLUMN wake_error, DROP COLUMN wake_status, DROP COLUMN last_wake_at, DROP COLUMN wake_next_at,
    DROP COLUMN wake_cooldown, DROP COLUMN wake_token, DROP COLUMN wake_header, DROP COLUMN wake_url,
    DROP COLUMN wake_enabled;
