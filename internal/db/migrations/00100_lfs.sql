-- +goose Up
-- Git LFS (PLAN §4.4).
--
-- Object bytes are content-addressed and deduplicated across the instance:
-- the tiered storage class "lfs" holds each object once under
-- sha256/<oid[0:2]>/<oid[2:4]>/<oid> (storage_objects). A row here grants a
-- repository access to an object and charges its size to a namespace quota;
-- a repository can only download objects it has a row for.
--
-- repo_id has no foreign key on purpose: when a repository is deleted its
-- rows stay until the LFS GC job releases the charged quota and removes
-- them (and the object, once no repository references it).
CREATE TABLE lfs_objects (
    repo_id     uuid        NOT NULL,
    oid         text        NOT NULL CHECK (oid ~ '^[0-9a-f]{64}$'),
    size        bigint      NOT NULL CHECK (size >= 0),
    -- Namespace whose storage quota the size is charged to (the repository
    -- owner at upload time; moved by the GC job after a transfer).
    charged_to  uuid        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    -- Last time the GC found the object referenced by the repository.
    seen_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, oid)
);

CREATE INDEX lfs_objects_oid ON lfs_objects (oid);

-- File locks of the Git LFS Locks API. Paths are unique per repository.
CREATE TABLE lfs_locks (
    id          uuid        PRIMARY KEY,
    repo_id     uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    path        text        NOT NULL CHECK (length(path) BETWEEN 1 AND 4096),
    refname     text        NOT NULL DEFAULT '' CHECK (length(refname) <= 255),
    owner_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repo_id, path)
);

CREATE INDEX lfs_locks_repo_created ON lfs_locks (repo_id, created_at, id);

-- Short-lived tokens handed out by git-lfs-authenticate (SSH) and embedded in
-- batch action headers. Only the SHA-256 of the token is stored; a token is
-- bound to one repository and one operation.
CREATE TABLE lfs_tokens (
    id          uuid        PRIMARY KEY,
    token_hash  bytea       NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    repo_id     uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    operation   text        NOT NULL CHECK (operation IN ('download', 'upload')),
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX lfs_tokens_expires ON lfs_tokens (expires_at);

-- +goose Down
DROP TABLE lfs_tokens;
DROP TABLE lfs_locks;
DROP TABLE lfs_objects;
