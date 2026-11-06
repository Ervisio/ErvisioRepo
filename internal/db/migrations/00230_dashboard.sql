-- +goose Up
-- Home dashboard and explore page: the activity feed, pinned repositories
-- and per-user UI preferences, plus the SQL predicate that decides whether
-- a viewer can read a repository (the same rules as the repository
-- listings: public, internal for signed-in unrestricted users, owner,
-- collaborator, team, organization base permission, instance admin).

-- +goose StatementBegin
CREATE FUNCTION repo_readable(p_repo uuid, p_viewer uuid, p_all boolean, p_internal boolean) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT p_all OR EXISTS (
        SELECT 1 FROM repos r
        WHERE r.id = p_repo
          AND (r.visibility = 'public'
               OR (p_internal AND r.visibility = 'internal')
               OR r.owner_id = p_viewer
               OR EXISTS (SELECT 1 FROM repo_collaborators c WHERE c.repo_id = r.id AND c.user_id = p_viewer)
               OR EXISTS (SELECT 1 FROM team_repos tr JOIN team_members tm ON tm.team_id = tr.team_id
                          WHERE tr.repo_id = r.id AND tm.user_id = p_viewer)
               OR EXISTS (SELECT 1 FROM org_members m JOIN orgs o ON o.id = m.org_id
                          WHERE m.org_id = r.owner_id AND m.user_id = p_viewer
                            AND (m.role IN ('owner', 'admin') OR (m.role = 'member' AND o.base_permission <> 'none')))))
$$;
-- +goose StatementEnd

-- One row per notable event (push, issue/pull request opened or closed or
-- merged, release, comment), copied from the event bus. id is the id of the
-- outbox event, so a redelivery inserts nothing.
CREATE TABLE activity_events (
    id         uuid        PRIMARY KEY,
    repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    actor_id   uuid        REFERENCES users (id) ON DELETE SET NULL,
    kind       text        NOT NULL CHECK (kind IN ('push', 'issue', 'pull_request', 'release', 'comment')),
    action     text        NOT NULL CHECK (length(action) <= 64),
    -- Issue or pull request number, 0 when not applicable.
    number     bigint      NOT NULL DEFAULT 0,
    -- Issue/pull request title, release tag or name, pushed ref.
    title      text        NOT NULL DEFAULT '' CHECK (length(title) <= 1024),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX activity_events_created ON activity_events (created_at DESC, id DESC);
CREATE INDEX activity_events_repo ON activity_events (repo_id, created_at DESC);

-- Repositories a user pinned to the top of the dashboard (at most 12,
-- enforced by the service), ordered by position.
CREATE TABLE user_pinned_repos (
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    position   integer     NOT NULL CHECK (position >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, repo_id)
);

CREATE INDEX user_pinned_repos_order ON user_pinned_repos (user_id, position);

-- UI preferences. layout '' = the instance default.
CREATE TABLE user_preferences (
    user_id    uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    theme      text        NOT NULL DEFAULT 'system' CHECK (theme IN ('system', 'light', 'dark')),
    layout     text        NOT NULL DEFAULT '' CHECK (layout IN ('', 'console', 'foglio', 'corsie')),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE user_preferences;
DROP TABLE user_pinned_repos;
DROP TABLE activity_events;
DROP FUNCTION repo_readable(uuid, uuid, boolean, boolean);
