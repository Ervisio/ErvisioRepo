-- +goose Up
-- Public profile details of a user account, shown on the profile page and
-- edited in the account settings. A row exists only once the user saved
-- them. public_email_id points at one of the user's verified addresses.

CREATE TABLE user_profiles (
    user_id         uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    bio             text        NOT NULL DEFAULT '' CHECK (length(bio) <= 640),
    company         text        NOT NULL DEFAULT '' CHECK (length(company) <= 100),
    location        text        NOT NULL DEFAULT '' CHECK (length(location) <= 100),
    website         text        NOT NULL DEFAULT '' CHECK (length(website) <= 255),
    social_links    text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(social_links) <= 4),
    -- IANA time zone shown as local time; empty hides it.
    timezone        text        NOT NULL DEFAULT '' CHECK (length(timezone) <= 64),
    public_email_id uuid        REFERENCES emails (id) ON DELETE SET NULL,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- Contribution calendar and profile activity read the events of one actor.
CREATE INDEX activity_events_actor ON activity_events (actor_id, created_at DESC);

-- +goose Down
DROP INDEX activity_events_actor;
DROP TABLE user_profiles;
