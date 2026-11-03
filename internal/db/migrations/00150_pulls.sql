-- +goose Up
-- Forks and pull requests (PLAN §5.4).
--
-- A pull request is an issues row of kind 'pull' (shared numbering, comments,
-- labels, assignees, milestone, timeline) plus one pull_requests row with the
-- PR-specific columns. A merged pull request is a closed issue whose
-- pull_requests.merged_at is set.

-- repo_forks records the network of a fork. The fork is a full copy of its
-- parent's refs at fork time (see internal/pulls/fork.go).
CREATE TABLE repo_forks (
    repo_id    uuid        PRIMARY KEY REFERENCES repos (id) ON DELETE CASCADE,
    parent_id  uuid        REFERENCES repos (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX repo_forks_parent ON repo_forks (parent_id);

CREATE TABLE pull_requests (
    issue_id           uuid        PRIMARY KEY REFERENCES issues (id) ON DELETE CASCADE,
    repo_id            uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    number             bigint      NOT NULL CHECK (number > 0),
    -- The head lives in head_repo_id (the base repository or a fork of it);
    -- NULL when the head repository was deleted.
    head_repo_id       uuid        REFERENCES repos (id) ON DELETE SET NULL,
    head_ref           text        NOT NULL CHECK (length(head_ref) BETWEEN 1 AND 255),
    head_sha           text        NOT NULL CHECK (head_sha ~ '^[0-9a-f]{40,64}$'),
    base_ref           text        NOT NULL CHECK (length(base_ref) BETWEEN 1 AND 255),
    base_sha           text        NOT NULL CHECK (base_sha ~ '^[0-9a-f]{40,64}$'),
    merge_base_sha     text        NOT NULL DEFAULT '' CHECK (merge_base_sha = '' OR merge_base_sha ~ '^[0-9a-f]{40,64}$'),
    draft              boolean     NOT NULL DEFAULT false,
    -- Test merge of head into base (refs/pull/<n>/merge).
    mergeable_state    text        NOT NULL DEFAULT 'unknown'
                                   CHECK (mergeable_state IN ('unknown', 'clean', 'conflict', 'error')),
    merge_sha          text        NOT NULL DEFAULT '' CHECK (merge_sha = '' OR merge_sha ~ '^[0-9a-f]{40,64}$'),
    conflict_files     text[]      NOT NULL DEFAULT '{}',
    merged_at          timestamptz,
    merged_by          uuid        REFERENCES users (id) ON DELETE SET NULL,
    merge_commit_sha   text        NOT NULL DEFAULT '' CHECK (merge_commit_sha = '' OR merge_commit_sha ~ '^[0-9a-f]{40,64}$'),
    merge_method       text        NOT NULL DEFAULT '' CHECK (merge_method IN ('', 'merge', 'squash', 'rebase')),
    -- Auto-merge: merged with these settings once every requirement is met.
    auto_merge_method  text        NOT NULL DEFAULT '' CHECK (auto_merge_method IN ('', 'merge', 'squash', 'rebase')),
    auto_merge_by      uuid        REFERENCES users (id) ON DELETE SET NULL,
    auto_merge_title   text        NOT NULL DEFAULT '' CHECK (length(auto_merge_title) <= 256),
    auto_merge_message text        NOT NULL DEFAULT '' CHECK (length(auto_merge_message) <= 65536),
    auto_merge_delete  boolean     NOT NULL DEFAULT false,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repo_id, number)
);

CREATE INDEX pull_requests_head ON pull_requests (head_repo_id, head_ref) WHERE merged_at IS NULL;
CREATE INDEX pull_requests_base ON pull_requests (repo_id, base_ref) WHERE merged_at IS NULL;

CREATE TABLE pull_reviews (
    id           uuid        PRIMARY KEY,
    pull_id      uuid        NOT NULL REFERENCES pull_requests (issue_id) ON DELETE CASCADE,
    repo_id      uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    reviewer_id  uuid        REFERENCES users (id) ON DELETE SET NULL,
    state        text        NOT NULL DEFAULT 'pending'
                             CHECK (state IN ('pending', 'commented', 'approved', 'changes_requested', 'dismissed')),
    body         text        NOT NULL DEFAULT '' CHECK (length(body) <= 65536),
    commit_sha   text        NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40,64}$'),
    submitted_at timestamptz,
    dismissed_at timestamptz,
    dismissed_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    dismiss_message text     NOT NULL DEFAULT '' CHECK (length(dismiss_message) <= 1000),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX pull_reviews_pull ON pull_reviews (pull_id, created_at, id);
-- At most one pending (draft) review per reviewer and pull request.
CREATE UNIQUE INDEX pull_reviews_pending ON pull_reviews (pull_id, reviewer_id) WHERE state = 'pending';

-- Inline comments on the diff. line/start_line address the current diff
-- (side RIGHT = new file, LEFT = old file); original_* keep where the comment
-- was written. outdated is set when a later head no longer has the line.
CREATE TABLE pull_review_comments (
    id                  uuid        PRIMARY KEY,
    pull_id             uuid        NOT NULL REFERENCES pull_requests (issue_id) ON DELETE CASCADE,
    repo_id             uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    review_id           uuid        REFERENCES pull_reviews (id) ON DELETE CASCADE,
    author_id           uuid        REFERENCES users (id) ON DELETE SET NULL,
    -- Replies point at the first comment of the thread and copy its position.
    in_reply_to         uuid        REFERENCES pull_review_comments (id) ON DELETE CASCADE,
    path                text        NOT NULL CHECK (length(path) BETWEEN 1 AND 4096),
    side                text        NOT NULL DEFAULT 'RIGHT' CHECK (side IN ('LEFT', 'RIGHT')),
    line                integer     CHECK (line > 0),
    start_line          integer     CHECK (start_line > 0),
    original_line       integer     NOT NULL CHECK (original_line > 0),
    original_start_line integer     CHECK (original_start_line > 0),
    original_commit_sha text        NOT NULL CHECK (original_commit_sha ~ '^[0-9a-f]{40,64}$'),
    commit_sha          text        NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40,64}$'),
    diff_hunk           text        NOT NULL DEFAULT '' CHECK (length(diff_hunk) <= 65536),
    body                text        NOT NULL CHECK (length(body) BETWEEN 1 AND 65536),
    outdated            boolean     NOT NULL DEFAULT false,
    resolved_at         timestamptz,
    resolved_by         uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX pull_review_comments_pull ON pull_review_comments (pull_id, created_at, id);
CREATE INDEX pull_review_comments_review ON pull_review_comments (review_id);
CREATE INDEX pull_review_comments_reply ON pull_review_comments (in_reply_to);

CREATE TABLE pull_review_requests (
    pull_id     uuid        NOT NULL REFERENCES pull_requests (issue_id) ON DELETE CASCADE,
    reviewer_id uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (pull_id, reviewer_id)
);

-- +goose Down
DROP TABLE pull_review_requests;
DROP TABLE pull_review_comments;
DROP TABLE pull_reviews;
DROP TABLE pull_requests;
DROP TABLE repo_forks;
