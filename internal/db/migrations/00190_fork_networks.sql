-- +goose Up
-- Fork networks (PLAN §5.4): the repositories of one fork family share a
-- local, append-only object pool (<hot>/pools/<aa>/<network>.git) that the
-- forks borrow from through objects/info/alternates. The remote journal of
-- every repository stays self-contained; the pool is a hot-tier cache only.
CREATE TABLE fork_networks (
    id           uuid        PRIMARY KEY,
    -- Topmost repository when the network was created (informational).
    root_repo_id uuid        REFERENCES repos (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE fork_network_members (
    repo_id    uuid        PRIMARY KEY REFERENCES repos (id) ON DELETE CASCADE,
    network_id uuid        NOT NULL REFERENCES fork_networks (id) ON DELETE CASCADE,
    -- The hot copy was created with an alternates link to the pool.
    pooled     boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX fork_network_members_network ON fork_network_members (network_id);

-- +goose Down
DROP TABLE fork_network_members;
DROP TABLE fork_networks;
