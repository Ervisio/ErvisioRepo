// Package authz holds the authorization rules of organizations, teams and
// repositories (PLAN §5.3). The functions are pure: callers load the facts
// (roles, grants, visibility) and ask what they allow, so that every rule is
// covered by table and fuzz tests.
package authz

import "fmt"

// OrgRole is the role of a user in an organization.
type OrgRole string

// Organization roles. Owners can do everything, admins manage the
// organization except its owners, members get the base permission on its
// repositories, billing members see billing only.
const (
	OrgNone    OrgRole = ""
	OrgOwner   OrgRole = "owner"
	OrgAdmin   OrgRole = "admin"
	OrgMember  OrgRole = "member"
	OrgBilling OrgRole = "billing"
)

// ParseOrgRole parses an assignable organization role.
func ParseOrgRole(s string) (OrgRole, error) {
	switch r := OrgRole(s); r {
	case OrgOwner, OrgAdmin, OrgMember, OrgBilling:
		return r, nil
	}
	return OrgNone, fmt.Errorf("unknown organization role %q", s)
}

// Valid reports whether r is an assignable role.
func (r OrgRole) Valid() bool {
	_, err := ParseOrgRole(string(r))
	return err == nil
}

// Member reports whether r is any membership.
func (r OrgRole) Member() bool { return r.Valid() }

// Manager reports whether r can manage the organization (owner or admin).
func (r OrgRole) Manager() bool { return r == OrgOwner || r == OrgAdmin }

// Actor is the caller of an organization operation: its role in the
// organization (OrgNone if not a member) and whether it is an instance admin,
// which acts as an owner.
type Actor struct {
	Role          OrgRole
	InstanceAdmin bool
}

// effective is the role the actor acts with.
func (a Actor) effective() OrgRole {
	if a.InstanceAdmin {
		return OrgOwner
	}
	return a.Role
}

// CanView reports whether the actor can see the organization's members,
// teams and settings.
func (a Actor) CanView() bool { return a.effective().Member() }

// CanManage reports whether the actor can change settings, manage teams and
// invitations, and read the organization audit log.
func (a Actor) CanManage() bool { return a.effective().Manager() }

// CanDelete reports whether the actor can delete the organization.
func (a Actor) CanDelete() bool { return a.effective() == OrgOwner }

// CanAssignRole reports whether the actor can give newRole to a user whose
// current role is current (OrgNone for a new member or an invitation). Only
// owners grant or take away ownership; admins manage non-owners only.
// The last-owner rule needs the owner count and is enforced by the caller.
func (a Actor) CanAssignRole(current, newRole OrgRole) bool {
	if !newRole.Valid() || (current != OrgNone && !current.Valid()) {
		return false
	}
	switch a.effective() {
	case OrgOwner:
		return true
	case OrgAdmin:
		return current != OrgOwner && newRole != OrgOwner
	}
	return false
}

// CanRemoveMember reports whether the actor can remove a member with role
// target; self is true when the actor removes itself (leaving), which any
// member may do. The last-owner rule is enforced by the caller.
func (a Actor) CanRemoveMember(target OrgRole, self bool) bool {
	if !target.Valid() {
		return false
	}
	if self && a.Role.Member() {
		return true
	}
	switch a.effective() {
	case OrgOwner:
		return true
	case OrgAdmin:
		return target != OrgOwner
	}
	return false
}

// TeamRole is the role of a member in a team.
type TeamRole string

// Team roles: maintainers manage the team's members.
const (
	TeamNone       TeamRole = ""
	TeamMaintainer TeamRole = "maintainer"
	TeamMember     TeamRole = "member"
)

// ParseTeamRole parses a team role.
func ParseTeamRole(s string) (TeamRole, error) {
	switch r := TeamRole(s); r {
	case TeamMaintainer, TeamMember:
		return r, nil
	}
	return TeamNone, fmt.Errorf("unknown team role %q", s)
}

// CanManageTeamMembers reports whether the actor, with team role in the
// team, can add, change or remove the team's members.
func (a Actor) CanManageTeamMembers(team TeamRole) bool {
	return a.CanManage() || (a.Role.Member() && team == TeamMaintainer)
}
