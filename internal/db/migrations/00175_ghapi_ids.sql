-- +goose Up
-- Numeric ids of the GitHub compatible API (/api/v3, PLAN §5.7). GitHub
-- clients expect integer ids (and per-repository milestone numbers) where
-- ErvisioRepo uses UUIDs: each UUID served by /api/v3 gets a stable integer
-- the first time it is shown, so that it can be passed back in a URL. The
-- ids stay below 2^53 so that JavaScript clients read them exactly. Rows of
-- deleted objects are harmless and never reused.
CREATE TABLE ghapi_ids (
    uuid uuid   PRIMARY KEY,
    id   bigint NOT NULL GENERATED ALWAYS AS IDENTITY (MAXVALUE 9007199254740991) UNIQUE
);

-- +goose Down
DROP TABLE ghapi_ids;
