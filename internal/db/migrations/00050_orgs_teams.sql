-- +goose Up
-- Organizations, teams, organization invitations, quotas and usage, and the
-- organization scope of the audit log (PLAN §5.3, §8.2, §10.2).

-- Organization roles: owner > admin > member; billing sees billing only.
ALTER TABLE org_members DROP CONSTRAINT org_members_role_check;
ALTER TABLE org_members ADD CONSTRAINT org_members_role_check
    CHECK (role IN ('owner', 'admin', 'member', 'billing'));
CREATE INDEX org_members_owners ON org_members (org_id) WHERE role = 'owner';

-- Base permission every member has on every repository of the organization.
ALTER TABLE orgs ADD COLUMN base_permission text NOT NULL DEFAULT 'read'
    CHECK (base_permission IN ('none', 'read', 'triage', 'write', 'maintain', 'admin'));

CREATE TABLE teams (
    id          uuid        PRIMARY KEY,
    org_id      uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name        text        NOT NULL CHECK (name ~ '^[A-Za-z0-9](-?[A-Za-z0-9])*$' AND length(name) <= 39),
    name_lower  text        GENERATED ALWAYS AS (lower(name)) STORED,
    description text        NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name_lower)
);

-- A team member must be a member of the team's organization: the composite
-- foreign key removes team memberships when the user leaves the org.
CREATE TABLE team_members (
    team_id    uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    org_id     uuid        NOT NULL,
    user_id    uuid        NOT NULL,
    role       text        NOT NULL DEFAULT 'member' CHECK (role IN ('maintainer', 'member')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id),
    FOREIGN KEY (org_id, user_id) REFERENCES org_members (org_id, user_id) ON DELETE CASCADE
);

CREATE INDEX team_members_user ON team_members (user_id);

-- Repository permissions granted to a team. repos arrives in phase 3, which
-- adds the foreign key on repo_id.
CREATE TABLE team_repos (
    team_id    uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    repo_id    uuid        NOT NULL,
    permission text        NOT NULL CHECK (permission IN ('read', 'triage', 'write', 'maintain', 'admin')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, repo_id)
);

CREATE INDEX team_repos_repo ON team_repos (repo_id);

-- Invitations to join an organization. The token is created by the mail
-- worker right before sending (or at creation when mail is disabled), so
-- token_hash is NULL until then; sending again replaces it.
CREATE TABLE org_invites (
    id          uuid        PRIMARY KEY,
    org_id      uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    email       text        NOT NULL CHECK (length(email) BETWEEN 3 AND 254),
    email_lower text        GENERATED ALWAYS AS (lower(email)) STORED,
    role        text        NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'billing')),
    team_ids    uuid[]      NOT NULL DEFAULT '{}',
    token_hash  bytea       UNIQUE CHECK (token_hash IS NULL OR length(token_hash) = 32),
    invited_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    expires_at  timestamptz NOT NULL,
    accepted_at timestamptz,
    accepted_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- At most one pending invitation per address and organization.
CREATE UNIQUE INDEX org_invites_pending ON org_invites (org_id, email_lower) WHERE accepted_at IS NULL;
CREATE INDEX org_invites_expires ON org_invites (expires_at) WHERE accepted_at IS NULL;

-- Instance-wide default limits per namespace kind; NULL means unlimited.
CREATE TABLE quota_defaults (
    kind              text        PRIMARY KEY CHECK (kind IN ('user', 'org')),
    max_repos         integer     CHECK (max_repos >= 0),
    max_storage_bytes bigint      CHECK (max_storage_bytes >= 0),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

INSERT INTO quota_defaults (kind) VALUES ('user'), ('org');

-- Per-namespace overrides; a NULL column falls back to quota_defaults.
CREATE TABLE quotas (
    namespace_id      uuid        PRIMARY KEY REFERENCES namespaces (id) ON DELETE CASCADE,
    max_repos         integer     CHECK (max_repos >= 0),
    max_storage_bytes bigint      CHECK (max_storage_bytes >= 0),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- Current usage per namespace, kept up to date by the code that creates and
-- deletes repositories and stores data (phase 3 onwards).
CREATE TABLE usage_counters (
    namespace_id  uuid        PRIMARY KEY REFERENCES namespaces (id) ON DELETE CASCADE,
    repos         integer     NOT NULL DEFAULT 0 CHECK (repos >= 0),
    storage_bytes bigint      NOT NULL DEFAULT 0 CHECK (storage_bytes >= 0),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Organization scope of audit entries, for the organization audit log. No
-- foreign key: entries outlive deleted organizations.
ALTER TABLE audit_log ADD COLUMN org_id uuid;
CREATE INDEX audit_log_org ON audit_log (org_id, id) WHERE org_id IS NOT NULL;
