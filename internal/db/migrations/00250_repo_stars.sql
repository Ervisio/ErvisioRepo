-- +goose Up
-- Repository stars. repos.stars_count is kept by a trigger on repo_stars so
-- that it stays right when stars cascade away with a deleted user.

CREATE TABLE repo_stars (
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    repo_id    uuid        NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, repo_id)
);

CREATE INDEX repo_stars_repo ON repo_stars (repo_id, created_at DESC);
CREATE INDEX repo_stars_user ON repo_stars (user_id, created_at DESC);

ALTER TABLE repos ADD COLUMN stars_count integer NOT NULL DEFAULT 0 CHECK (stars_count >= 0);

-- +goose StatementBegin
CREATE FUNCTION repo_stars_count() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE repos SET stars_count = stars_count + 1 WHERE id = NEW.repo_id;
    ELSE
        UPDATE repos SET stars_count = greatest(stars_count - 1, 0) WHERE id = OLD.repo_id;
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER repo_stars_count AFTER INSERT OR DELETE ON repo_stars
    FOR EACH ROW EXECUTE FUNCTION repo_stars_count();

-- +goose Down
DROP TRIGGER repo_stars_count ON repo_stars;
DROP FUNCTION repo_stars_count();
ALTER TABLE repos DROP COLUMN stars_count;
DROP TABLE repo_stars;
