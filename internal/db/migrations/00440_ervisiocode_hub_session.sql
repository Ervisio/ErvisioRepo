-- +goose Up
ALTER TABLE tokens
    ADD COLUMN hub_session_hash bytea UNIQUE,
    ADD COLUMN hub_session_expires_at timestamptz,
    ADD CONSTRAINT tokens_hub_session_device CHECK (
        (hub_session_hash IS NULL AND hub_session_expires_at IS NULL)
        OR (is_ervisiocode_device AND hub_session_hash IS NOT NULL AND hub_session_expires_at IS NOT NULL)
    );
