-- +goose Up
-- Explicit "latest release" choice (GitHub make_latest). NULL: automatic, the
-- newest published release that is neither a draft nor a prerelease; true:
-- this release is the latest; false: never the automatic latest.
ALTER TABLE releases ADD COLUMN make_latest boolean;

CREATE UNIQUE INDEX releases_repo_latest ON releases (repo_id) WHERE make_latest IS TRUE;
