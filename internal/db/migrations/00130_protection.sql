-- +goose Up
-- Branch protection rules and tag rules (PLAN §5.3). Rules are matched by a
-- glob pattern against the short ref name; when several rules match a ref,
-- the most specific one applies (internal/protection). Users and teams are
-- referenced by id in arrays: a deleted user or team simply stops matching.
CREATE TABLE branch_protections (
    id                         uuid        PRIMARY KEY,
    repo_id                    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    pattern                    text        NOT NULL CHECK (length(pattern) BETWEEN 1 AND 255),
    -- Pull request requirements (enforced on merge).
    required_approvals         integer     NOT NULL DEFAULT 0 CHECK (required_approvals BETWEEN 0 AND 10),
    require_code_owner_reviews boolean     NOT NULL DEFAULT false,
    dismiss_stale_reviews      boolean     NOT NULL DEFAULT false,
    required_status_checks     text[]      NOT NULL DEFAULT '{}',
    strict_status_checks       boolean     NOT NULL DEFAULT false,
    -- Push requirements (enforced in pre-receive for every transport).
    require_linear_history     boolean     NOT NULL DEFAULT false,
    allow_force_pushes         boolean     NOT NULL DEFAULT false,
    allow_deletions            boolean     NOT NULL DEFAULT false,
    require_signed_commits     boolean     NOT NULL DEFAULT false,
    restrict_pushes            boolean     NOT NULL DEFAULT false,
    push_user_ids              uuid[]      NOT NULL DEFAULT '{}',
    push_team_ids              uuid[]      NOT NULL DEFAULT '{}',
    -- Repository administrators bypass the rule only when this is set.
    admins_bypass              boolean     NOT NULL DEFAULT false,
    created_by                 uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repo_id, pattern)
);

-- Who may create, move and delete the tags matching a pattern. Each action
-- needs at least the given repository permission ('none' forbids it to
-- everybody but the listed users and teams, and admins when they bypass).
CREATE TABLE tag_rules (
    id               uuid        PRIMARY KEY,
    repo_id          uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    pattern          text        NOT NULL CHECK (length(pattern) BETWEEN 1 AND 255),
    create_min       text        NOT NULL DEFAULT 'write' CHECK (create_min IN ('write', 'maintain', 'admin', 'none')),
    update_min       text        NOT NULL DEFAULT 'none' CHECK (update_min IN ('write', 'maintain', 'admin', 'none')),
    delete_min       text        NOT NULL DEFAULT 'admin' CHECK (delete_min IN ('write', 'maintain', 'admin', 'none')),
    allowed_user_ids uuid[]      NOT NULL DEFAULT '{}',
    allowed_team_ids uuid[]      NOT NULL DEFAULT '{}',
    admins_bypass    boolean     NOT NULL DEFAULT false,
    created_by       uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repo_id, pattern)
);

-- +goose Down
DROP TABLE tag_rules;
DROP TABLE branch_protections;
