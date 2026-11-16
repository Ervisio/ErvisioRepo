-- +goose Up
-- Runner device registration (curl …/runner/install.sh | sudo sh): the
-- installer requests a code, an admin approves it at /runner/approve and
-- the installer's poll receives a single-use registration token. Runners
-- also gain a pause flag (no new jobs) and report their capacity.

ALTER TABLE actions_runners
    ADD COLUMN paused   boolean NOT NULL DEFAULT false,
    ADD COLUMN capacity integer NOT NULL DEFAULT 0 CHECK (capacity BETWEEN 0 AND 1024);

-- Tokens minted by an approved device registration work only once.
ALTER TABLE actions_registration_tokens ADD COLUMN single_use boolean NOT NULL DEFAULT false;

CREATE TABLE actions_runner_devices (
    id               uuid        PRIMARY KEY,
    -- SHA-256 of the device code the installer polls with.
    device_code_hash bytea       NOT NULL UNIQUE,
    -- Eight letters, shown as XXXX-XXXX.
    user_code        text        NOT NULL UNIQUE,
    status           text        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'denied', 'consumed')),
    -- Machine facts sent by the installer, for the approval page.
    hostname         text        NOT NULL DEFAULT '' CHECK (length(hostname) <= 255),
    os               text        NOT NULL DEFAULT '' CHECK (length(os) <= 32),
    arch             text        NOT NULL DEFAULT '' CHECK (length(arch) <= 32),
    cpus             integer     NOT NULL DEFAULT 0,
    memory_bytes     bigint      NOT NULL DEFAULT 0,
    docker           boolean     NOT NULL DEFAULT false,
    version          text        NOT NULL DEFAULT '' CHECK (length(version) <= 255),
    -- Requested, then approved, runner settings.
    name             text        NOT NULL DEFAULT '' CHECK (length(name) <= 255),
    labels           text[]      NOT NULL DEFAULT '{}',
    capacity         integer     NOT NULL DEFAULT 0 CHECK (capacity BETWEEN 0 AND 64),
    request_ip       text        NOT NULL DEFAULT '',
    -- Scope chosen by the approver.
    scope            text        CHECK (scope IN ('instance', 'org', 'repo')),
    owner_id         uuid        REFERENCES namespaces (id) ON DELETE CASCADE,
    repo_id          uuid        REFERENCES repos (id) ON DELETE CASCADE,
    decided_by       uuid        REFERENCES users (id) ON DELETE SET NULL,
    interval_seconds integer     NOT NULL DEFAULT 5,
    last_poll_at     timestamptz,
    expires_at       timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE actions_runner_devices;
ALTER TABLE actions_registration_tokens DROP COLUMN single_use;
ALTER TABLE actions_runners DROP COLUMN capacity, DROP COLUMN paused;
