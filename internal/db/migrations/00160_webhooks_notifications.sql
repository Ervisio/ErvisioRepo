-- +goose Up
-- Outgoing webhooks (PLAN §5.7) and notifications (PLAN §5.5).

-- A webhook of a repository or of an organization (fired for every
-- repository the organization owns). The secret is sealed with AES-256-GCM
-- under a key derived from the master key.
CREATE TABLE webhooks (
    id           uuid        PRIMARY KEY,
    repo_id      uuid        REFERENCES repos (id) ON DELETE CASCADE,
    org_id       uuid        REFERENCES orgs (id) ON DELETE CASCADE,
    url          text        NOT NULL CHECK (url ~ '^https?://' AND length(url) <= 2048),
    content_type text        NOT NULL DEFAULT 'json' CHECK (content_type IN ('json', 'form')),
    format       text        NOT NULL DEFAULT 'github' CHECK (format IN ('github', 'ervisio')),
    -- Sealed signature secret; NULL when the hook is not signed.
    secret       bytea,
    -- GitHub event names, or '*' for every event.
    events       text[]      NOT NULL DEFAULT '{push}',
    active       boolean     NOT NULL DEFAULT true,
    created_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CHECK ((repo_id IS NULL) <> (org_id IS NULL))
);

CREATE INDEX webhooks_repo ON webhooks (repo_id) WHERE repo_id IS NOT NULL;
CREATE INDEX webhooks_org ON webhooks (org_id) WHERE org_id IS NOT NULL;

-- One delivery of an event (or a ping) to a hook, with the last attempt.
-- The payload is kept whole (it is bounded when built) for redelivery;
-- the response body is truncated.
CREATE TABLE webhook_deliveries (
    id               uuid        PRIMARY KEY,
    webhook_id       uuid        NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    -- Outbox event; NULL for pings and redeliveries.
    event_id         uuid,
    -- X-GitHub-Delivery / X-Ervisio-Delivery.
    guid             uuid        NOT NULL,
    event            text        NOT NULL,
    action           text        NOT NULL DEFAULT '',
    redelivery       boolean     NOT NULL DEFAULT false,
    payload          bytea       NOT NULL,
    status           text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ok', 'failed')),
    attempts         integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    url              text        NOT NULL DEFAULT '',
    request_headers  jsonb       NOT NULL DEFAULT '{}',
    status_code      integer     NOT NULL DEFAULT 0,
    response_headers jsonb       NOT NULL DEFAULT '{}',
    response_body    text        NOT NULL DEFAULT '',
    duration_ms      integer     NOT NULL DEFAULT 0,
    error            text        NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    delivered_at     timestamptz
);

CREATE INDEX webhook_deliveries_hook ON webhook_deliveries (webhook_id, created_at DESC);
-- At-least-once delivery of the bus: one delivery per hook and event.
CREATE UNIQUE INDEX webhook_deliveries_event ON webhook_deliveries (webhook_id, event_id)
    WHERE event_id IS NOT NULL;

-- Notification threads: one per user and subject.
CREATE TABLE notification_threads (
    id            uuid        PRIMARY KEY,
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    repo_id       uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    subject_type  text        NOT NULL CHECK (subject_type IN ('issue', 'pull_request', 'release', 'commit')),
    -- issue:<id>, release:<id> or commit:<sha>.
    subject_key   text        NOT NULL,
    -- Issue or pull request number; 0 otherwise.
    number        bigint      NOT NULL DEFAULT 0,
    title         text        NOT NULL DEFAULT '',
    reason        text        NOT NULL CHECK (reason IN ('author', 'assigned', 'mentioned', 'review_requested',
                                                         'subscribed', 'comment', 'state_change')),
    -- One line describing the last update (shown in mail).
    summary       text        NOT NULL DEFAULT '',
    unread        boolean     NOT NULL DEFAULT true,
    done          boolean     NOT NULL DEFAULT false,
    last_event_id uuid,
    last_actor_id uuid        REFERENCES users (id) ON DELETE SET NULL,
    last_read_at  timestamptz,
    emailed_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, subject_key)
);

CREATE INDEX notification_threads_user ON notification_threads (user_id, updated_at DESC);
CREATE INDEX notification_threads_unread ON notification_threads (user_id) WHERE unread AND NOT done;

-- Watch level of a user on a repository; no row means participating.
CREATE TABLE repo_subscriptions (
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    level      text        NOT NULL CHECK (level IN ('watching', 'participating', 'ignoring')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, repo_id)
);

CREATE INDEX repo_subscriptions_repo ON repo_subscriptions (repo_id, level);

-- Explicit subscription (or unsubscription) of a user to a subject.
CREATE TABLE thread_subscriptions (
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    repo_id     uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    subject_key text        NOT NULL,
    subscribed  boolean     NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, subject_key)
);

CREATE INDEX thread_subscriptions_subject ON thread_subscriptions (subject_key);

-- Whether the user receives notifications by mail as well.
ALTER TABLE users ADD COLUMN email_notifications boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE users DROP COLUMN email_notifications;
DROP TABLE thread_subscriptions;
DROP TABLE repo_subscriptions;
DROP TABLE notification_threads;
DROP TABLE webhook_deliveries;
DROP TABLE webhooks;
