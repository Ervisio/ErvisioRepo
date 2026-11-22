-- +goose Up
-- Display-only bindings, separate from verified account addresses. Keep a
-- deleted binding so the alias cannot be reassigned by reusing a username.
CREATE TABLE git_author_aliases (
    email_lower text PRIMARY KEY CHECK (email_lower = lower(email_lower)),
    user_id uuid REFERENCES users (id) ON DELETE SET NULL
);
