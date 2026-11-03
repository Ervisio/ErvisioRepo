package authz

import (
	"fmt"
	"slices"
)

// Permission is a level of access to a repository; higher levels include the
// lower ones.
type Permission int

// Repository permissions, in increasing order.
const (
	PermNone Permission = iota
	PermRead
	PermTriage
	PermWrite
	PermMaintain
	PermAdmin
)

var permNames = [...]string{"none", "read", "triage", "write", "maintain", "admin"}

// String returns the name of p as stored and shown by the API.
func (p Permission) String() string {
	if p < PermNone || p > PermAdmin {
		return fmt.Sprintf("Permission(%d)", int(p))
	}
	return permNames[p]
}

// ParsePermission parses a permission name, "none" included.
func ParsePermission(s string) (Permission, error) {
	if i := slices.Index(permNames[:], s); i >= 0 {
		return Permission(i), nil
	}
	return PermNone, fmt.Errorf("unknown permission %q", s)
}

// ParseGrant parses a permission that can be granted to a team or a
// collaborator: "none" is not one.
func ParseGrant(s string) (Permission, error) {
	p, err := ParsePermission(s)
	if err == nil && p == PermNone {
		err = fmt.Errorf("permission %q cannot be granted", s)
	}
	return p, err
}

// Visibility of a repository.
type Visibility string

// Repository visibilities: public repositories are readable by anyone,
// internal ones by every signed-in user who is not restricted.
const (
	Public   Visibility = "public"
	Internal Visibility = "internal"
	Private  Visibility = "private"
)

// RepoFacts are what the effective permission of a user on a repository
// depends on. A zero value is an anonymous caller on a private repository.
type RepoFacts struct {
	// Signed is false for anonymous callers.
	Signed bool
	// InstanceAdmin, Restricted and Suspended describe the caller's account.
	InstanceAdmin bool
	Restricted    bool
	Suspended     bool

	Visibility Visibility
	// PersonalOwner is true when the repository belongs to the caller's
	// user namespace.
	PersonalOwner bool
	// OrgRole is the caller's role in the owning organization, if any.
	OrgRole OrgRole
	// BasePermission is the owning organization's base permission.
	BasePermission Permission
	// Teams and Collaborator are the explicit grants of the caller.
	Teams        []Permission
	Collaborator Permission
}

// RepoPermission returns the effective permission: the highest of all the
// grants that apply.
func RepoPermission(f RepoFacts) Permission {
	if f.Suspended {
		return PermNone
	}
	if !f.Signed {
		if f.Visibility == Public {
			return PermRead
		}
		return PermNone
	}
	if f.InstanceAdmin || f.PersonalOwner || f.OrgRole.Manager() {
		return PermAdmin
	}
	p := PermNone
	switch f.Visibility {
	case Public:
		p = PermRead
	case Internal:
		if !f.Restricted {
			p = PermRead
		}
	}
	if f.OrgRole == OrgMember {
		p = max(p, clamp(f.BasePermission))
	}
	for _, t := range f.Teams {
		p = max(p, clamp(t))
	}
	return max(p, clamp(f.Collaborator))
}

func clamp(p Permission) Permission { return min(max(p, PermNone), PermAdmin) }

// Action is an operation on a repository.
type Action string

// Repository actions and the permission they need.
const (
	ActRead           Action = "read"            // clone, fetch, browse
	ActTriage         Action = "triage"          // label, assign, close issues
	ActPush           Action = "push"            // push to unprotected branches
	ActMerge          Action = "merge"           // merge pull requests
	ActManageBranches Action = "manage_branches" // protections, releases, webhooks
	ActSettings       Action = "settings"        // repository settings
	ActCollaborators  Action = "collaborators"   // access management
	ActDelete         Action = "delete"          // delete, transfer, visibility
)

var actionPerm = map[Action]Permission{
	ActRead:           PermRead,
	ActTriage:         PermTriage,
	ActPush:           PermWrite,
	ActMerge:          PermWrite,
	ActManageBranches: PermMaintain,
	ActSettings:       PermMaintain,
	ActCollaborators:  PermAdmin,
	ActDelete:         PermAdmin,
}

// Required returns the minimum permission of action; unknown actions need
// more than any permission.
func Required(action Action) Permission {
	if p, ok := actionPerm[action]; ok {
		return p
	}
	return PermAdmin + 1
}

// Allowed reports whether permission p allows action.
func Allowed(p Permission, action Action) bool { return p >= Required(action) }
