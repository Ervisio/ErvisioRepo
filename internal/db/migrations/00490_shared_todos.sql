-- +goose Up
CREATE TABLE shared_todos (
    repo_id uuid REFERENCES repos(id) ON DELETE CASCADE,
    org_id uuid REFERENCES orgs(id) ON DELETE CASCADE,
    revision bigint NOT NULL CHECK (revision > 0),
    categories jsonb NOT NULL,
    items jsonb NOT NULL,
    CHECK (num_nonnulls(repo_id, org_id) = 1),
    UNIQUE NULLS NOT DISTINCT (repo_id, org_id)
);

CREATE INDEX shared_todos_org_idx ON shared_todos (org_id) WHERE org_id IS NOT NULL;

CREATE TABLE shared_todo_preferences (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    repo_id uuid REFERENCES repos(id) ON DELETE CASCADE,
    org_id uuid REFERENCES orgs(id) ON DELETE CASCADE,
    collapsed boolean NOT NULL,
    CHECK (num_nonnulls(repo_id, org_id) = 1),
    UNIQUE NULLS NOT DISTINCT (repo_id, org_id, user_id)
);

CREATE INDEX shared_todo_preferences_org_idx ON shared_todo_preferences (org_id) WHERE org_id IS NOT NULL;
CREATE INDEX shared_todo_preferences_user_idx ON shared_todo_preferences (user_id);
