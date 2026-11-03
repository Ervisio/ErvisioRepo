-- +goose Up
-- Releases tied to git tags and their downloadable assets (PLAN §4.2, §5).
-- Asset bytes live in the tiered storage (storage_objects); a row here owns
-- exactly one object, whose key is derived from (repo, release, asset).
CREATE TABLE releases (
    id          uuid        PRIMARY KEY,
    repo_id     uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    tag         text        NOT NULL CHECK (length(tag) BETWEEN 1 AND 255),
    name        text        NOT NULL DEFAULT '' CHECK (length(name) <= 255),
    body        text        NOT NULL DEFAULT '' CHECK (length(body) <= 125000),
    draft       boolean     NOT NULL DEFAULT false,
    prerelease  boolean     NOT NULL DEFAULT false,
    -- Branch or commit the tag is created from when it does not exist yet.
    target      text        NOT NULL DEFAULT '' CHECK (length(target) <= 255),
    -- Commit the tag pointed to when the release was published.
    commit_sha  text        NOT NULL DEFAULT '' CHECK (commit_sha = '' OR commit_sha ~ '^[0-9a-f]{40,64}$'),
    author_id   uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);

-- One release per tag, drafts included.
CREATE UNIQUE INDEX releases_repo_tag ON releases (repo_id, tag);
CREATE INDEX releases_repo_published ON releases (repo_id, published_at DESC NULLS LAST, created_at DESC);

CREATE TABLE release_assets (
    id            uuid        PRIMARY KEY,
    release_id    uuid        NOT NULL REFERENCES releases (id) ON DELETE CASCADE,
    repo_id       uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    name          text        NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    label         text        NOT NULL DEFAULT '' CHECK (length(label) <= 255),
    content_type  text        NOT NULL DEFAULT 'application/octet-stream' CHECK (length(content_type) <= 255),
    size          bigint      NOT NULL CHECK (size >= 0),
    sha256        bytea       NOT NULL CHECK (length(sha256) = 32),
    -- Storage data type (policy) and relative key of the object.
    storage_policy text       NOT NULL,
    storage_key   text        NOT NULL,
    download_count bigint     NOT NULL DEFAULT 0 CHECK (download_count >= 0),
    uploader_id   uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (release_id, name)
);

CREATE INDEX release_assets_repo ON release_assets (repo_id);

-- +goose Down
DROP TABLE release_assets;
DROP TABLE releases;
