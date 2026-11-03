-- +goose Up
-- Issues, labels, milestones, comments, reactions, timeline, cross-references,
-- attachments (PLAN §4.3, §5) and the transactional event outbox.
--
-- Numbering. Issues and pull requests share ONE per-repository sequence, as on
-- GitHub: #12 is either an issue or a pull request, never both. Every number is
-- an "issues" row; kind tells which. A later pull_requests table holds only
-- the PR-specific columns and points at its issues row
-- (pull_requests.issue_id REFERENCES issues (id), kind = 'pull'), so comments,
-- labels, assignees, milestones, reactions, the timeline and cross-references
-- are reused unchanged by pull requests. Numbers come from issue_counters
-- (issues.NextNumber), incremented in the transaction that inserts the row:
-- they are gap-free unless that transaction rolls back.
CREATE TABLE issue_counters (
    repo_id     uuid   PRIMARY KEY REFERENCES repos (id) ON DELETE CASCADE,
    last_number bigint NOT NULL DEFAULT 0 CHECK (last_number >= 0)
);

CREATE TABLE milestones (
    id          uuid        PRIMARY KEY,
    repo_id     uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    title       text        NOT NULL CHECK (length(title) BETWEEN 1 AND 255),
    description text        NOT NULL DEFAULT '' CHECK (length(description) <= 10000),
    state       text        NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    due_on      timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    closed_at   timestamptz
);

CREATE UNIQUE INDEX milestones_repo_title ON milestones (repo_id, lower(title));

CREATE TABLE labels (
    id          uuid        PRIMARY KEY,
    repo_id     uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    name        text        NOT NULL CHECK (length(name) BETWEEN 1 AND 50),
    -- Six lowercase hex digits, no leading '#'.
    color       text        NOT NULL CHECK (color ~ '^[0-9a-f]{6}$'),
    description text        NOT NULL DEFAULT '' CHECK (length(description) <= 100),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX labels_repo_name ON labels (repo_id, lower(name));

CREATE TABLE issues (
    id            uuid        PRIMARY KEY,
    repo_id       uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    number        bigint      NOT NULL CHECK (number > 0),
    kind          text        NOT NULL DEFAULT 'issue' CHECK (kind IN ('issue', 'pull')),
    title         text        NOT NULL CHECK (length(title) BETWEEN 1 AND 256),
    body          text        NOT NULL DEFAULT '' CHECK (length(body) <= 65536),
    state         text        NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    state_reason  text        NOT NULL DEFAULT '' CHECK (state_reason IN ('', 'completed', 'not_planned', 'reopened', 'duplicate')),
    locked        boolean     NOT NULL DEFAULT false,
    lock_reason   text        NOT NULL DEFAULT '' CHECK (lock_reason IN ('', 'off-topic', 'too heated', 'resolved', 'spam')),
    author_id     uuid        REFERENCES users (id) ON DELETE SET NULL,
    milestone_id  uuid        REFERENCES milestones (id) ON DELETE SET NULL,
    comment_count integer     NOT NULL DEFAULT 0 CHECK (comment_count >= 0),
    closed_at     timestamptz,
    closed_by     uuid        REFERENCES users (id) ON DELETE SET NULL,
    edited_at     timestamptz,
    edited_by     uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repo_id, number)
);

CREATE INDEX issues_repo_state ON issues (repo_id, kind, state, created_at DESC);
CREATE INDEX issues_repo_updated ON issues (repo_id, updated_at DESC);
CREATE INDEX issues_author ON issues (author_id);
CREATE INDEX issues_milestone ON issues (milestone_id) WHERE milestone_id IS NOT NULL;
-- Full-text search over title (weight A) and body (B), and over comment
-- bodies (issue_comments_search). 'simple' keeps it language-neutral (en/it).
-- Expression indexes: queries must use the same functions to hit them.
-- +goose StatementBegin
CREATE FUNCTION issue_search_vector(title text, body text) RETURNS tsvector
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT setweight(to_tsvector('simple'::regconfig, title), 'A') || setweight(to_tsvector('simple'::regconfig, body), 'B')
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION issue_comment_search_vector(body text) RETURNS tsvector
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('simple'::regconfig, body)
$$;
-- +goose StatementEnd
CREATE INDEX issues_search ON issues USING gin (issue_search_vector(title, body));

CREATE TABLE issue_labels (
    issue_id   uuid        NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    label_id   uuid        NOT NULL REFERENCES labels (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, label_id)
);

CREATE INDEX issue_labels_label ON issue_labels (label_id);

CREATE TABLE issue_assignees (
    issue_id   uuid        NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, user_id)
);

CREATE INDEX issue_assignees_user ON issue_assignees (user_id);

CREATE TABLE issue_comments (
    id         uuid        PRIMARY KEY,
    issue_id   uuid        NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    author_id  uuid        REFERENCES users (id) ON DELETE SET NULL,
    body       text        NOT NULL CHECK (length(body) BETWEEN 1 AND 65536),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- Last edit; the previous bodies are in issue_edits.
    edited_at  timestamptz,
    edited_by  uuid        REFERENCES users (id) ON DELETE SET NULL
);

CREATE INDEX issue_comments_issue ON issue_comments (issue_id, created_at, id);
CREATE INDEX issue_comments_repo ON issue_comments (repo_id);
CREATE INDEX issue_comments_search ON issue_comments USING gin (issue_comment_search_vector(body));

-- Edit history: one row per edit of an issue body (comment_id NULL) or of a
-- comment, holding the body as it was BEFORE the edit.
CREATE TABLE issue_edits (
    id         uuid        PRIMARY KEY,
    issue_id   uuid        NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    comment_id uuid        REFERENCES issue_comments (id) ON DELETE CASCADE,
    editor_id  uuid        REFERENCES users (id) ON DELETE SET NULL,
    body       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX issue_edits_subject ON issue_edits (issue_id, comment_id, created_at DESC);

-- Reactions on an issue (comment_id NULL) or on one of its comments, with
-- the eight GitHub reaction kinds.
CREATE TABLE issue_reactions (
    id         uuid        PRIMARY KEY,
    issue_id   uuid        NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    comment_id uuid        REFERENCES issue_comments (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    content    text        NOT NULL CHECK (content IN ('+1', '-1', 'laugh', 'confused', 'heart', 'hooray', 'rocket', 'eyes')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX issue_reactions_issue_once ON issue_reactions (issue_id, user_id, content) WHERE comment_id IS NULL;
CREATE UNIQUE INDEX issue_reactions_comment_once ON issue_reactions (comment_id, user_id, content) WHERE comment_id IS NOT NULL;

-- Timeline. event is one of: commented, labeled, unlabeled, assigned,
-- unassigned, milestoned, demilestoned, renamed, closed, reopened, locked,
-- unlocked, referenced (from a commit), cross-referenced (from another issue
-- or comment), mentioned. data holds the event details (label name/color,
-- assignee id, titles, commit, source of a reference...).
CREATE TABLE issue_events (
    id         uuid        PRIMARY KEY,
    repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    issue_id   uuid        NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    actor_id   uuid        REFERENCES users (id) ON DELETE SET NULL,
    event      text        NOT NULL CHECK (length(event) BETWEEN 1 AND 40),
    comment_id uuid        REFERENCES issue_comments (id) ON DELETE CASCADE,
    data       jsonb       NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX issue_events_issue ON issue_events (issue_id, created_at, id);

-- Markdown cross-references found in a source (an issue body, a comment or a
-- commit message): "#12", "owner/repo#12" (target_kind issue), commit ids
-- (commit) and "@user" (user). source_key is "issue:<id>", "comment:<id>"
-- or "commit:<repo id>:<sha>"; target_key the issue id, sha or user id.
CREATE TABLE issue_references (
    id                uuid        PRIMARY KEY,
    source_key        text        NOT NULL,
    source_repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    source_issue_id   uuid        REFERENCES issues (id) ON DELETE CASCADE,
    source_comment_id uuid        REFERENCES issue_comments (id) ON DELETE CASCADE,
    source_commit     text        NOT NULL DEFAULT '' CHECK (source_commit = '' OR source_commit ~ '^[0-9a-f]{40,64}$'),
    target_kind       text        NOT NULL CHECK (target_kind IN ('issue', 'commit', 'user')),
    target_key        text        NOT NULL,
    target_issue_id   uuid        REFERENCES issues (id) ON DELETE CASCADE,
    target_user_id    uuid        REFERENCES users (id) ON DELETE CASCADE,
    -- Commit id as written (abbreviated ids are not resolved).
    target_commit     text        NOT NULL DEFAULT '',
    actor_id          uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_key, target_kind, target_key)
);

CREATE INDEX issue_references_target_issue ON issue_references (target_issue_id) WHERE target_issue_id IS NOT NULL;
CREATE INDEX issue_references_target_user ON issue_references (target_user_id) WHERE target_user_id IS NOT NULL;
CREATE INDEX issue_references_source_issue ON issue_references (source_issue_id) WHERE source_issue_id IS NOT NULL;

-- Files uploaded for issue and comment bodies (linked from markdown). The
-- bytes live in the tiered storage under the "attachments" data type ("lfs"
-- as fallback) and are charged to the quota of the repository owner.
CREATE TABLE issue_attachments (
    id             uuid        PRIMARY KEY,
    repo_id        uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    uploader_id    uuid        REFERENCES users (id) ON DELETE SET NULL,
    name           text        NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    content_type   text        NOT NULL DEFAULT 'application/octet-stream' CHECK (length(content_type) <= 255),
    size           bigint      NOT NULL CHECK (size >= 0),
    sha256         bytea       NOT NULL CHECK (length(sha256) = 32),
    storage_policy text        NOT NULL,
    storage_key    text        NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX issue_attachments_repo ON issue_attachments (repo_id);

-- Transactional outbox of domain events (internal/events). A row is written
-- in the same transaction as the change it describes; a River job claims
-- the undispatched rows and enqueues one delivery job per registered handler.
-- repo_id has no foreign key so events about deleted repositories survive.
CREATE TABLE events_outbox (
    id            uuid        PRIMARY KEY,
    kind          text        NOT NULL CHECK (length(kind) BETWEEN 1 AND 64),
    action        text        NOT NULL DEFAULT '' CHECK (length(action) <= 64),
    repo_id       uuid,
    actor_id      uuid,
    subject_id    uuid,
    payload       jsonb       NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now(),
    dispatched_at timestamptz
);

CREATE INDEX events_outbox_pending ON events_outbox (created_at, id) WHERE dispatched_at IS NULL;
CREATE INDEX events_outbox_dispatched ON events_outbox (dispatched_at) WHERE dispatched_at IS NOT NULL;

-- +goose Down
DROP TABLE events_outbox;
DROP TABLE issue_attachments;
DROP TABLE issue_references;
DROP TABLE issue_events;
DROP TABLE issue_reactions;
DROP TABLE issue_edits;
DROP TABLE issue_comments;
DROP TABLE issue_assignees;
DROP TABLE issue_labels;
DROP TABLE issues;
DROP TABLE labels;
DROP TABLE milestones;
DROP TABLE issue_counters;
DROP FUNCTION issue_comment_search_vector(text);
DROP FUNCTION issue_search_vector(text, text);
