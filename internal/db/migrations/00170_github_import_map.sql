-- +goose Up
-- Import of the non-git data of a GitHub repository (PLAN §5.6): which
-- kinds to import, and the GitHub id -> local id map that makes every
-- importer idempotent across retries of the import job.
ALTER TABLE github_imports ADD COLUMN include text[] NOT NULL DEFAULT '{}';

CREATE TABLE github_import_map (
    repo_id   uuid   NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    -- label, milestone, issue (issues and pull requests), comment, review,
    -- review_comment, release, asset.
    kind      text   NOT NULL CHECK (length(kind) BETWEEN 1 AND 40),
    github_id bigint NOT NULL,
    local_id  uuid   NOT NULL,
    PRIMARY KEY (repo_id, kind, github_id)
);

-- +goose Down
DROP TABLE github_import_map;
ALTER TABLE github_imports DROP COLUMN include;
