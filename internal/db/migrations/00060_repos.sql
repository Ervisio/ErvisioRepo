-- +goose Up
-- Repositories, their old names, their collaborators and the foreign key of
-- team grants (PLAN §4.3, §5.2, §5.3).

-- A repository belongs to a user or an organization namespace. The public
-- path owner/name lives only here: on disk and in the remote store the
-- repository is known by its id. A namespace with repositories cannot be
-- deleted (ON DELETE RESTRICT): they must be deleted or transferred first.
CREATE TABLE repos (
    id             uuid        PRIMARY KEY,
    owner_id       uuid        NOT NULL REFERENCES namespaces (id) ON DELETE RESTRICT,
    name           text        NOT NULL CHECK (name ~ '^[A-Za-z0-9_][A-Za-z0-9._-]*$'
                                   AND length(name) <= 100 AND lower(name) !~ '\.git$'),
    name_lower     text        GENERATED ALWAYS AS (lower(name)) STORED,
    description    text        NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
    visibility     text        NOT NULL DEFAULT 'private' CHECK (visibility IN ('public', 'internal', 'private')),
    default_branch text        NOT NULL DEFAULT 'main' CHECK (length(default_branch) BETWEEN 1 AND 255),
    -- Tiering (§4.3): hot = bare repository on the local disk; cold = only in
    -- the remote store; hydrating/evicting = transition in progress (the
    -- tier.Acquire lock is held); degraded = the hot copy is unusable and the
    -- remote copy could not be restored.
    storage_state  text        NOT NULL DEFAULT 'hot'
                               CHECK (storage_state IN ('hot', 'cold', 'hydrating', 'evicting', 'degraded')),
    state_changed_at timestamptz NOT NULL DEFAULT now(),
    pinned         boolean     NOT NULL DEFAULT false,
    size_bytes     bigint      NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    last_access_at timestamptz NOT NULL DEFAULT now(),
    -- Sequence of the last segment recorded in the remote manifest.
    manifest_seq   bigint      NOT NULL DEFAULT 0 CHECK (manifest_seq >= 0),
    created_by     uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_id, name_lower)
);

-- Eviction candidates: least recently used hot repositories.
CREATE INDEX repos_lru ON repos (last_access_at) WHERE storage_state = 'hot' AND NOT pinned;
CREATE INDEX repos_state ON repos (storage_state) WHERE storage_state <> 'hot';

-- Old paths of renamed or transferred repositories. A new repository with
-- the same path replaces the redirect.
CREATE TABLE repo_redirects (
    owner_id   uuid        NOT NULL REFERENCES namespaces (id) ON DELETE CASCADE,
    name_lower text        NOT NULL,
    repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, name_lower)
);

CREATE INDEX repo_redirects_repo ON repo_redirects (repo_id);

-- Users granted a permission on a repository directly.
CREATE TABLE repo_collaborators (
    repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    permission text        NOT NULL CHECK (permission IN ('read', 'triage', 'write', 'maintain', 'admin')),
    added_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, user_id)
);

CREATE INDEX repo_collaborators_user ON repo_collaborators (user_id);

-- Team grants now reference real repositories (00050 had no repos table).
DELETE FROM team_repos tr WHERE NOT EXISTS (SELECT 1 FROM repos r WHERE r.id = tr.repo_id);
ALTER TABLE team_repos ADD CONSTRAINT team_repos_repo_id_fkey
    FOREIGN KEY (repo_id) REFERENCES repos (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE team_repos DROP CONSTRAINT team_repos_repo_id_fkey;
DROP TABLE repo_collaborators;
DROP TABLE repo_redirects;
DROP TABLE repos;
