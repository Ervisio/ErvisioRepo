-- +goose Up
-- Profile avatar, and removal of the unused organization role "billing".

ALTER TABLE users ADD COLUMN avatar_url text NOT NULL DEFAULT '' CHECK (length(avatar_url) <= 2048);

UPDATE org_members SET role = 'member' WHERE role = 'billing';
UPDATE org_invites SET role = 'member' WHERE role = 'billing';

ALTER TABLE org_members DROP CONSTRAINT org_members_role_check;
ALTER TABLE org_members ADD CONSTRAINT org_members_role_check CHECK (role IN ('owner', 'admin', 'member'));

ALTER TABLE org_invites DROP CONSTRAINT org_invites_role_check;
ALTER TABLE org_invites ADD CONSTRAINT org_invites_role_check CHECK (role IN ('owner', 'admin', 'member'));

-- +goose Down
ALTER TABLE org_invites DROP CONSTRAINT org_invites_role_check;
ALTER TABLE org_invites ADD CONSTRAINT org_invites_role_check CHECK (role IN ('owner', 'admin', 'member', 'billing'));
ALTER TABLE org_members DROP CONSTRAINT org_members_role_check;
ALTER TABLE org_members ADD CONSTRAINT org_members_role_check CHECK (role IN ('owner', 'admin', 'member', 'billing'));
ALTER TABLE users DROP COLUMN avatar_url;
