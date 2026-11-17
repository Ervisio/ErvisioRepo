-- +goose Up
-- Order of the organizations shown on the profile, by name; organizations
-- not listed follow in alphabetical order.
ALTER TABLE user_profiles ADD COLUMN org_order text[] NOT NULL DEFAULT '{}' CHECK (cardinality(org_order) <= 100);
