-- +goose Up
-- Avatar images of organizations and repositories: the URL of an image stored
-- by internal/avatars (content-addressed, immutable), empty when unset.
ALTER TABLE orgs ADD COLUMN avatar_url text NOT NULL DEFAULT '' CHECK (length(avatar_url) <= 2048);
ALTER TABLE repos ADD COLUMN avatar_url text NOT NULL DEFAULT '' CHECK (length(avatar_url) <= 2048);
