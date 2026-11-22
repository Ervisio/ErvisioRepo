-- name: PinGitAuthorAlias :exec
INSERT INTO git_author_aliases (email_lower, user_id)
VALUES (sqlc.arg(email_lower), (
    SELECT u.id FROM users u
    JOIN namespaces n ON n.id = u.id
    WHERE n.name_lower = sqlc.arg(username)::text
))
ON CONFLICT (email_lower) DO NOTHING;

-- name: GetGitAuthorAlias :one
SELECT user_id FROM git_author_aliases WHERE email_lower = $1;
