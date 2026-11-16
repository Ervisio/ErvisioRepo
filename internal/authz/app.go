package authz

import (
	"fmt"
	"maps"
	"slices"
)

// Permissions of Apps (APPS-PLAN A2), with the names and levels of GitHub
// Apps. An installation acts with a permission set instead of a role: the
// functions below are the single place that turns it into repository
// access, so that every check of a repository permission also works for
// installations and user-to-server tokens.

// AppPermissions maps a GitHub permission name to "read" or "write".
type AppPermissions map[string]string

// App permission levels.
const (
	AppRead  = "read"
	AppWrite = "write"
)

// Repository permissions of an App.
const (
	AppContents       = "contents"
	AppMetadata       = "metadata"
	AppIssues         = "issues"
	AppPullRequests   = "pull_requests"
	AppChecks         = "checks"
	AppStatuses       = "statuses"
	AppActions        = "actions"
	AppSecrets        = "secrets"
	AppVariables      = "variables"
	AppAdministration = "administration"
	AppWorkflows      = "workflows"
	AppDeployments    = "deployments"
)

// Organization and account permissions of an App.
const (
	AppMembers          = "members"
	AppOrgAdministation = "organization_administration"
	AppEmailAddresses   = "email_addresses"
)

// AppRepoPermissions, AppOrgPermissions and AppUserPermissions list the
// permissions an App can ask for, in display order.
var (
	AppRepoPermissions = []string{AppContents, AppMetadata, AppIssues, AppPullRequests, AppChecks, AppStatuses,
		AppActions, AppSecrets, AppVariables, AppAdministration, AppWorkflows, AppDeployments}
	AppOrgPermissions  = []string{AppMembers, AppOrgAdministation}
	AppUserPermissions = []string{AppEmailAddresses}
)

// appWriteOnly are permissions GitHub offers only at the write level.
var appWriteOnly = []string{AppWorkflows}

// KnownAppPermission reports whether name is a permission an App can hold.
func KnownAppPermission(name string) bool {
	return slices.Contains(AppRepoPermissions, name) || slices.Contains(AppOrgPermissions, name) ||
		slices.Contains(AppUserPermissions, name)
}

// level orders the permission levels: none < read < write.
func level(s string) int {
	switch s {
	case AppRead:
		return 1
	case AppWrite:
		return 2
	}
	return 0
}

// Has reports whether p holds name at least at level need ("read" or
// "write"). metadata is always readable.
func (p AppPermissions) Has(name, need string) bool {
	if name == AppMetadata && need == AppRead {
		return true
	}
	return level(p[name]) >= max(level(need), 1)
}

// Normalize validates p: known names, levels read or write, and metadata
// at least read. "none" entries are dropped.
func (p AppPermissions) Normalize() (AppPermissions, error) {
	out := AppPermissions{AppMetadata: AppRead}
	for k, v := range p {
		if k == "actions_variables" {
			k = AppVariables // older name of the permission
		}
		if !KnownAppPermission(k) {
			return nil, fmt.Errorf("unknown permission %q", k)
		}
		switch v {
		case "", "none":
			if k == AppMetadata {
				continue
			}
			delete(out, k)
		case AppRead:
			if slices.Contains(appWriteOnly, k) {
				return nil, fmt.Errorf("permission %q can only be write", k)
			}
			out[k] = v
		case AppWrite:
			out[k] = v
		default:
			return nil, fmt.Errorf("permission %q: level must be read or write", k)
		}
	}
	return out, nil
}

// Intersect returns the permissions held by both p and q at the lower of
// the two levels.
func (p AppPermissions) Intersect(q AppPermissions) AppPermissions {
	out := AppPermissions{}
	for k, v := range p {
		w, ok := q[k]
		if !ok {
			continue
		}
		if level(w) < level(v) {
			v = w
		}
		if level(v) > 0 {
			out[k] = v
		}
	}
	out[AppMetadata] = AppRead
	return out
}

// Covers reports whether p holds everything q holds.
func (p AppPermissions) Covers(q AppPermissions) bool {
	for k, v := range q {
		if !p.Has(k, v) {
			return false
		}
	}
	return true
}

// Equal reports whether p and q hold the same permissions.
func (p AppPermissions) Equal(q AppPermissions) bool { return p.Covers(q) && q.Covers(p) }

// Clone copies p.
func (p AppPermissions) Clone() AppPermissions { return maps.Clone(p) }

// AppRepoPermission is the repository permission an installation (or a
// user-to-server token) holds on a repository it covers. It is an upper
// bound: an App with contents:write can push and manage releases
// (maintain), one with administration:write manages the repository. The
// request routers that installations can reach check the permission of
// each endpoint on top of it (AppEndpointAllowed in ghapi, the contents
// permission in the git transports), so the bound never grants more than
// the permission set.
func AppRepoPermission(p AppPermissions) Permission {
	out := PermRead
	if p.Has(AppIssues, AppWrite) || p.Has(AppPullRequests, AppWrite) {
		out = PermTriage
	}
	if p.Has(AppContents, AppWrite) {
		out = PermMaintain
	}
	if p.Has(AppAdministration, AppWrite) {
		out = PermAdmin
	}
	return out
}

// AppActionAllowed reports whether an App with p may perform action on a
// repository it covers. It is the second gate of the repository service
// for installation callers.
func AppActionAllowed(p AppPermissions, action Action) bool {
	switch action {
	case ActRead:
		return true
	case ActTriage:
		return p.Has(AppIssues, AppWrite) || p.Has(AppPullRequests, AppWrite)
	case ActPush, ActMerge:
		return p.Has(AppContents, AppWrite)
	case ActManageBranches:
		return p.Has(AppContents, AppWrite) || p.Has(AppAdministration, AppWrite)
	case ActSettings, ActCollaborators, ActDelete:
		return p.Has(AppAdministration, AppWrite)
	}
	return false
}

// AppEventPermission is the permission an App needs to receive a webhook
// event ("" for events every App receives).
func AppEventPermission(event string) string {
	switch event {
	case "push", "create", "delete", "release":
		return AppContents
	case "issues", "issue_comment", "milestone":
		return AppIssues
	case "pull_request", "pull_request_review", "pull_request_review_comment":
		return AppPullRequests
	case "status":
		return AppStatuses
	case "check_run", "check_suite":
		return AppChecks
	case "label":
		return AppMetadata
	}
	return ""
}

// AppEvents lists the webhook events an App can subscribe to.
var AppEvents = []string{"push", "create", "delete", "release", "issues", "issue_comment", "label", "milestone",
	"pull_request", "pull_request_review", "pull_request_review_comment", "status", "check_run", "check_suite"}

// AppEventAllowed reports whether p allows receiving event. issue_comment
// and milestone also come with pull_requests, as on GitHub.
func AppEventAllowed(p AppPermissions, event string) bool {
	if !slices.Contains(AppEvents, event) {
		return false
	}
	need := AppEventPermission(event)
	if need == "" || p.Has(need, AppRead) {
		return true
	}
	return need == AppIssues && p.Has(AppPullRequests, AppRead)
}
