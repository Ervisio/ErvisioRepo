-- +goose Up
-- Bot accounts (APPS-PLAN A0). A bot is a users row whose namespace kind is
-- 'bot': its login ends in "[bot]" (a suffix normal names cannot have), and
-- it has no password, email, session or second factor, so nobody can sign
-- in as it. The bots row holds what only bots have; app_id links the bot
-- of an App (phase A2, foreign key added with the apps table).
ALTER TABLE namespaces DROP CONSTRAINT namespaces_kind_check;
ALTER TABLE namespaces ADD CONSTRAINT namespaces_kind_check CHECK (kind IN ('user', 'org', 'bot'));

ALTER TABLE namespaces DROP CONSTRAINT namespaces_name_check;
ALTER TABLE namespaces ADD CONSTRAINT namespaces_name_check CHECK (
    (kind <> 'bot' AND name ~ '^[A-Za-z0-9](-?[A-Za-z0-9])*$' AND length(name) <= 39)
    OR (kind = 'bot' AND name ~ '^[A-Za-z0-9](-?[A-Za-z0-9])*\[bot\]$' AND length(name) <= 44)
);

CREATE TABLE bots (
    user_id     uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    app_id      uuid,
    description text        NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX bots_app ON bots (app_id) WHERE app_id IS NOT NULL;

-- System bots; their ids are fixed (internal/bots).
INSERT INTO namespaces (id, kind, name) VALUES
    ('00000000-0000-7000-8000-00000000b001', 'bot', 'ervisio-sync[bot]'),
    ('00000000-0000-7000-8000-00000000b002', 'bot', 'ervisio-actions[bot]');

INSERT INTO users (id, display_name, role, locale) VALUES
    ('00000000-0000-7000-8000-00000000b001', 'Ervisio Sync', 'user', 'en'),
    ('00000000-0000-7000-8000-00000000b002', 'Ervisio Actions', 'user', 'en');

INSERT INTO bots (user_id, description) VALUES
    ('00000000-0000-7000-8000-00000000b001', 'Writes the pushes and imports of the GitHub synchronization.'),
    ('00000000-0000-7000-8000-00000000b002', 'Acts for Ervisio Actions workflows that use their job token.');
