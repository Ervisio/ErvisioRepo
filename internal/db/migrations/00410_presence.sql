-- +goose Up
-- Presence and file locks (per repository, off by default): who works on
-- the repository, on which branch, with which uncommitted tracked files.
ALTER TABLE repos ADD COLUMN presence_enabled boolean NOT NULL DEFAULT false;

-- One row per (repository, user, client device), upserted by every
-- heartbeat. A row is live while updated_at is within the TTL; expired rows
-- are deleted by a sweeper. Only paths are stored, never contents.
CREATE TABLE repo_presence (
    repo_id     uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id   uuid        NOT NULL,
    branch      text        NOT NULL DEFAULT '' CHECK (length(branch) <= 255),
    head        text        NOT NULL DEFAULT '' CHECK (length(head) <= 64),
    dirty       jsonb       NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(dirty) = 'array'),
    truncated   boolean     NOT NULL DEFAULT false,
    ahead       integer     NOT NULL DEFAULT 0 CHECK (ahead >= 0),
    unpublished boolean     NOT NULL DEFAULT false,
    active      boolean     NOT NULL DEFAULT false,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, user_id, client_id)
);

CREATE INDEX repo_presence_updated ON repo_presence (updated_at);

-- Locks hold per branch: a lock without a ref belongs to the default
-- branch, and a path may be locked once per ref.
UPDATE lfs_locks l SET refname = 'refs/heads/' || r.default_branch
FROM repos r WHERE r.id = l.repo_id AND l.refname = '';
ALTER TABLE lfs_locks DROP CONSTRAINT lfs_locks_repo_id_path_key;
ALTER TABLE lfs_locks DROP CONSTRAINT lfs_locks_refname_check;
ALTER TABLE lfs_locks ADD CONSTRAINT lfs_locks_refname_check CHECK (length(refname) BETWEEN 1 AND 300);
ALTER TABLE lfs_locks ADD CONSTRAINT lfs_locks_repo_ref_path_key UNIQUE (repo_id, refname, path);
