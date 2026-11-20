-- +goose Up
ALTER TABLE tokens ADD COLUMN remote_session_metadata jsonb NOT NULL DEFAULT '[]'::jsonb;
