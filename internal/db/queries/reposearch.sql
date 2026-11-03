-- name: SearchRepos :many
-- Repositories whose name contains pattern (already lower-cased and with
-- LIKE metacharacters escaped), visible to a viewer: all_repos = instance
-- admin; internal = the viewer is signed in and not restricted. Exact and
-- prefix matches come first.
SELECT sqlc.embed(r), n.name AS owner_name, n.kind AS owner_kind
FROM repos r
JOIN namespaces n ON n.id = r.owner_id
WHERE r.name_lower LIKE '%' || sqlc.arg(pattern)::text || '%' ESCAPE '\'
  AND (sqlc.arg(all_repos)::boolean
       OR r.visibility = 'public'
       OR (sqlc.arg(internal)::boolean AND r.visibility = 'internal')
       OR r.owner_id = sqlc.arg(viewer)::uuid
       OR EXISTS (SELECT 1 FROM repo_collaborators c WHERE c.repo_id = r.id AND c.user_id = sqlc.arg(viewer)::uuid)
       OR EXISTS (SELECT 1 FROM team_repos tr JOIN team_members tm ON tm.team_id = tr.team_id
                  WHERE tr.repo_id = r.id AND tm.user_id = sqlc.arg(viewer)::uuid)
       OR EXISTS (SELECT 1 FROM org_members m JOIN orgs o ON o.id = m.org_id
                  WHERE m.org_id = r.owner_id AND m.user_id = sqlc.arg(viewer)::uuid
                    AND (m.role IN ('owner', 'admin') OR (m.role = 'member' AND o.base_permission <> 'none'))))
ORDER BY (r.name_lower = sqlc.arg(pattern)::text) DESC,
         (r.name_lower LIKE sqlc.arg(pattern)::text || '%' ESCAPE '\') DESC,
         n.name_lower, r.name_lower
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);
