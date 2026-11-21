-- +goose Up
-- Metadata cache of the code pages, so a repository that is not on the
-- local disk (cold) can show its file list and README at once while it is
-- hydrated in the background. Rows are keyed by git object id, which names
-- the content: they never go stale. Filled while the repository is hot and
-- from GitHub for linked repositories; rows older than 30 days are pruned.

-- One tree listing: [{"n":name,"m":mode,"t":type,"o":oid,"s":size}].
CREATE TABLE git_browse_trees (
    repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    oid        text        NOT NULL CHECK (oid ~ '^[0-9a-f]{40,64}$'),
    entries    jsonb       NOT NULL,
    truncated  boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, oid)
);

-- A commit (or an annotated tag pointing at one) and its root tree.
CREATE TABLE git_browse_commits (
    repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    oid        text        NOT NULL CHECK (oid ~ '^[0-9a-f]{40,64}$'),
    commit_sha text        NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40,64}$'),
    tree_oid   text        NOT NULL CHECK (tree_oid ~ '^[0-9a-f]{40,64}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, oid)
);

-- README contents only (bounded size), for the repository home page.
CREATE TABLE git_browse_blobs (
    repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    oid        text        NOT NULL CHECK (oid ~ '^[0-9a-f]{40,64}$'),
    content    bytea       NOT NULL CHECK (length(content) <= 262144),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, oid)
);

CREATE INDEX git_browse_trees_created ON git_browse_trees (created_at);
CREATE INDEX git_browse_commits_created ON git_browse_commits (created_at);
CREATE INDEX git_browse_blobs_created ON git_browse_blobs (created_at);

-- +goose Down
DROP TABLE git_browse_blobs;
DROP TABLE git_browse_commits;
DROP TABLE git_browse_trees;
