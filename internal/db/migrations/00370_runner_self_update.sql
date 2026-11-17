-- +goose Up
-- Runner self-update from the admin panel: the version an admin asked the
-- runner to update to (empty when none is pending), the last failure the
-- runner reported and whether the runner can update itself at all.

ALTER TABLE actions_runners
    ADD COLUMN update_version      text        NOT NULL DEFAULT '',
    ADD COLUMN update_requested_at timestamptz,
    ADD COLUMN update_error        text        NOT NULL DEFAULT '',
    ADD COLUMN self_update         boolean     NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE actions_runners
    DROP COLUMN self_update, DROP COLUMN update_error, DROP COLUMN update_requested_at, DROP COLUMN update_version;
