-- Numeric ids of the GitHub compatible API (internal/ghapi).

-- name: AssignGitHubAPIIDs :exec
-- Gives an id to each UUID that has none yet.
INSERT INTO ghapi_ids (uuid)
SELECT u FROM unnest(@uuids::uuid[]) AS t (u)
ORDER BY u
ON CONFLICT (uuid) DO NOTHING;

-- name: ListGitHubAPIIDs :many
SELECT uuid, id FROM ghapi_ids WHERE uuid = ANY(@uuids::uuid[]);

-- name: GetGitHubAPIUUID :one
SELECT uuid FROM ghapi_ids WHERE id = $1;

-- name: ListNamespacesByName :many
-- Users and organizations by name, for the user objects of /api/v3.
SELECT id, kind, name FROM namespaces WHERE name_lower = ANY(@names::text[]);

-- name: GetGitHubAPIAssetRelease :one
-- The release of an asset: the asset URLs of /api/v3 carry no release id.
-- The release service still checks access with the repository.
SELECT release_id FROM release_assets WHERE id = $1;
