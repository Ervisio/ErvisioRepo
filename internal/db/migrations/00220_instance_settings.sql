-- +goose Up
-- Instance settings changed from the web admin. config.toml stays the
-- default: a row here overrides one editable key ("section.key") at the next
-- start, unless the key is locked by [settings] locked or an EREPO_ variable.
CREATE TABLE instance_settings (
    key        text        PRIMARY KEY CHECK (length(key) BETWEEN 1 AND 128),
    value      jsonb       NOT NULL,
    updated_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE instance_settings;
