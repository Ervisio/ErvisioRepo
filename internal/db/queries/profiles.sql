-- Public profile details, the contribution calendar, the activity of one
-- user and the weekly activity of repositories. Listings filter repositories
-- through repo_readable(repo, viewer, all_repos, internal) like the dashboard.

-- name: GetUserProfileDetails :one
-- The saved details; public_email is empty unless it is still verified.
SELECT p.bio, p.company, p.location, p.website, p.social_links, p.timezone, p.org_order,
       COALESCE(e.address, '')::text AS public_email
FROM user_profiles p
LEFT JOIN emails e ON e.id = p.public_email_id AND e.verified_at IS NOT NULL
WHERE p.user_id = $1;

-- name: UpsertUserProfileDetails :exec
INSERT INTO user_profiles (user_id, bio, company, location, website, social_links, timezone, public_email_id, org_order)
VALUES (@user_id, @bio, @company, @location, @website, @social_links, @timezone, sqlc.narg('public_email_id'), @org_order)
ON CONFLICT (user_id) DO UPDATE SET bio = EXCLUDED.bio, company = EXCLUDED.company, location = EXCLUDED.location,
    website = EXCLUDED.website, social_links = EXCLUDED.social_links, timezone = EXCLUDED.timezone,
    public_email_id = EXCLUDED.public_email_id, org_order = EXCLUDED.org_order, updated_at = now();

-- name: GetVerifiedEmailID :one
SELECT id FROM emails
WHERE user_id = $1 AND address_lower = lower(sqlc.arg(address)::text) AND verified_at IS NOT NULL;

-- name: ListVisibleUserOrgs :many
-- Organizations of a user that the viewer can see: the viewer is a member
-- too, or an instance admin (all_orgs).
SELECT n.name, o.display_name, o.avatar_url
FROM org_members m
JOIN orgs o ON o.id = m.org_id
JOIN namespaces n ON n.id = o.id
WHERE m.user_id = sqlc.arg(user_id)::uuid
  AND (sqlc.arg(all_orgs)::boolean
       OR EXISTS (SELECT 1 FROM org_members v WHERE v.org_id = m.org_id AND v.user_id = sqlc.arg(viewer)::uuid))
ORDER BY n.name_lower;

-- name: CountUserContributions :many
-- Events of one actor per UTC day in [since, until), readable repositories only.
SELECT (e.created_at AT TIME ZONE 'UTC')::date AS day, count(*) AS events
FROM activity_events e
WHERE e.actor_id = sqlc.arg(actor)::uuid
  AND e.created_at >= sqlc.arg(since)::timestamptz AND e.created_at < sqlc.arg(until)::timestamptz
  AND repo_readable(e.repo_id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
GROUP BY 1
ORDER BY 1;

-- name: CountUserActivityKinds :many
-- Events of one actor by kind and action in [since, until), readable repositories only.
SELECT e.kind, e.action, count(*) AS events
FROM activity_events e
WHERE e.actor_id = sqlc.arg(actor)::uuid
  AND e.created_at >= sqlc.arg(since)::timestamptz AND e.created_at < sqlc.arg(until)::timestamptz
  AND repo_readable(e.repo_id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
GROUP BY 1, 2;

-- name: CountUserActivityHours :many
-- Events of one actor since since per hour of the day in time zone tz, readable repositories only.
SELECT extract(hour FROM e.created_at AT TIME ZONE sqlc.arg(tz)::text)::int AS hour, count(*) AS events
FROM activity_events e
WHERE e.actor_id = sqlc.arg(actor)::uuid
  AND e.created_at >= sqlc.arg(since)::timestamptz
  AND repo_readable(e.repo_id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
GROUP BY 1
ORDER BY 1;

-- name: ListUserContributionYears :many
-- UTC years in which the actor has events in readable repositories, newest first.
SELECT DISTINCT extract(year FROM e.created_at AT TIME ZONE 'UTC')::int AS year
FROM activity_events e
WHERE e.actor_id = sqlc.arg(actor)::uuid
  AND repo_readable(e.repo_id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
ORDER BY 1 DESC;

-- name: ListActorActivity :many
-- Newest first events of one actor in the readable repositories.
SELECT e.id, e.kind, e.action, e.number, e.title, e.created_at,
       n.name AS owner_name, r.name AS repo_name
FROM activity_events e
JOIN repos r ON r.id = e.repo_id
JOIN namespaces n ON n.id = r.owner_id
WHERE e.actor_id = sqlc.arg(actor)::uuid
  AND repo_readable(r.id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
ORDER BY e.created_at DESC, e.id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountRepoWeeklyActivity :many
-- Events per repository and week since since (week 0 = the oldest), for
-- the readable repositories among ids.
SELECT e.repo_id, floor(extract(epoch FROM (e.created_at - sqlc.arg(since)::timestamptz)) / 604800)::int AS week,
       count(*) AS events
FROM activity_events e
WHERE e.repo_id = ANY(sqlc.arg(ids)::uuid[])
  AND e.created_at >= sqlc.arg(since)::timestamptz
  AND repo_readable(e.repo_id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean)
GROUP BY 1, 2;

-- name: ListReadableRepoIDs :many
SELECT r.id FROM repos r
WHERE r.id = ANY(sqlc.arg(ids)::uuid[])
  AND repo_readable(r.id, sqlc.arg(viewer)::uuid, sqlc.arg(all_repos)::boolean, sqlc.arg(internal)::boolean);
