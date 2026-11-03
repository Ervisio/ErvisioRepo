-- +goose Up
-- Content-addressed objects written through the storage tier (PLAN §4.2, §4.5).
-- A row exists only once the object is confirmed on its primary target, so a
-- row never points at a partial or unverified object.
CREATE TABLE storage_objects (
    target      text        NOT NULL,                 -- primary target name
    key         text        NOT NULL,                 -- full key on the target, "<policy>/<...>"
    policy      text        NOT NULL,                 -- data type: git, lfs, registry, actions, backup
    size        bigint      NOT NULL CHECK (size >= 0),
    sha256      bytea       NOT NULL CHECK (length(sha256) = 32),
    replicas_ok boolean     NOT NULL DEFAULT false,   -- every replica holds a verified copy
    verified_at timestamptz,                          -- last successful sha256 check on the primary
    last_error  text,                                 -- last replication or verification failure
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (target, key)
);

CREATE INDEX storage_objects_pending_replicas ON storage_objects (created_at) WHERE NOT replicas_ok;
CREATE INDEX storage_objects_verified_at ON storage_objects (verified_at NULLS FIRST);
