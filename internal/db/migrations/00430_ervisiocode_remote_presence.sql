-- +goose Up
ALTER TABLE tokens
    ADD COLUMN remote_last_seen_at timestamptz,
    ADD COLUMN remote_active boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT tokens_remote_presence_device CHECK (
        (is_ervisiocode_device OR remote_last_seen_at IS NULL)
        AND (NOT remote_active OR remote_last_seen_at IS NOT NULL)
    );
