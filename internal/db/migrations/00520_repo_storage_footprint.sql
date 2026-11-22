-- +goose Up
-- Archived logs are looked up by their logical key across primary target changes.
CREATE INDEX storage_objects_actions_key ON storage_objects (key) INCLUDE (size)
    WHERE policy = 'actions';
