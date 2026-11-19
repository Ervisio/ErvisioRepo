-- +goose Up
INSERT INTO oauth_apps (id, client_id, is_system, name, short_name, device_flow)
VALUES ('00000000-0000-7000-8000-00000000a003', 'ErvisioCodeRemote', true, 'ErvisioCode', 'ErvisioCode', true);

ALTER TABLE tokens ADD COLUMN is_ervisiocode_device boolean NOT NULL DEFAULT false;
ALTER TABLE tokens ADD CONSTRAINT tokens_ervisiocode_device_scope CHECK (
    NOT is_ervisiocode_device OR (
        scopes = ARRAY['ervisiocode:device']::text[] AND grant_id IS NULL AND expires_at IS NOT NULL
    )
);
