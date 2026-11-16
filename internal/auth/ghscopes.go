package auth

import (
	"slices"
	"strings"
)

// toGitHubScopes maps the token scopes to the classic GitHub OAuth scopes
// that clients check in X-OAuth-Scopes (gh requires repo and read:org).
var toGitHubScopes = map[string][]string{
	ScopeWriteRepo:     {"repo"},
	ScopeReadUser:      {"read:user", "read:org"},
	ScopeWriteUser:     {"user", "read:org"},
	ScopeReadPackages:  {"read:packages"},
	ScopeWritePackages: {"write:packages"},
	ScopeWriteActions:  {"workflow"},
	ScopeAdmin:         {"admin:org"},
}

// fromGitHubScopes maps the classic GitHub OAuth scopes a client requests
// (device flow) to token scopes. Scopes without a counterpart (gist,
// codespace, project...) are ignored. No GitHub scope grants the instance
// admin scope: admin:org manages organizations, which needs user:write here.
var fromGitHubScopes = map[string][]string{
	"repo":               {ScopeWriteRepo},
	"public_repo":        {ScopeWriteRepo},
	"repo:status":        {ScopeWriteRepo},
	"repo_deployment":    {ScopeWriteRepo},
	"delete_repo":        {ScopeWriteRepo},
	"admin:repo_hook":    {ScopeWriteRepo},
	"write:repo_hook":    {ScopeWriteRepo},
	"read:repo_hook":     {ScopeReadRepo},
	"workflow":           {ScopeWriteActions},
	"read:org":           {ScopeReadUser},
	"read:user":          {ScopeReadUser},
	"user:email":         {ScopeReadUser},
	"user:follow":        {ScopeWriteUser},
	"read:public_key":    {ScopeReadUser},
	"read:gpg_key":       {ScopeReadUser},
	"read:discussion":    {ScopeReadUser},
	"user":               {ScopeWriteUser},
	"write:org":          {ScopeWriteUser},
	"admin:org":          {ScopeWriteUser},
	"write:public_key":   {ScopeWriteUser},
	"admin:public_key":   {ScopeWriteUser},
	"write:gpg_key":      {ScopeWriteUser},
	"admin:gpg_key":      {ScopeWriteUser},
	"notifications":      {ScopeWriteUser},
	"write:discussion":   {ScopeWriteUser},
	"read:packages":      {ScopeReadPackages},
	"write:packages":     {ScopeWritePackages},
	"delete:packages":    {ScopeWritePackages},
	"admin:org_hook":     {ScopeWriteUser},
	"admin:enterprise":   nil,
	"gist":               nil,
	"codespace":          nil,
	"project":            nil,
	"read:project":       nil,
	"read:audit_log":     nil,
	"manage_runners:org": nil,
}

// DefaultGitHubScopes are requested when a device flow client sends no
// scope: the defaults of gh auth login.
var DefaultGitHubScopes = []string{"repo", "read:org", "workflow"}

// GitHubScopes returns the classic GitHub scopes of a token, in order and
// without duplicates.
func GitHubScopes(scopes []string) []string {
	var out []string
	for _, sc := range scopes {
		for _, g := range toGitHubScopes[sc] {
			if !slices.Contains(out, g) {
				out = append(out, g)
			}
		}
	}
	return out
}

// ParseGitHubScopes splits an OAuth scope parameter (space or comma
// separated) into distinct, known GitHub scopes; unknown ones are dropped.
func ParseGitHubScopes(param string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(param, func(r rune) bool { return r == ' ' || r == ',' }) {
		if _, ok := fromGitHubScopes[f]; ok && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// ScopesFromGitHub maps GitHub scopes to sorted token scopes. user:read is
// always included: every client reads the account it signed in with.
// A write scope makes the matching read scope redundant and drops it.
func ScopesFromGitHub(gh []string) []string {
	out := []string{ScopeReadUser}
	for _, g := range gh {
		for _, sc := range fromGitHubScopes[g] {
			if !slices.Contains(out, sc) {
				out = append(out, sc)
			}
		}
	}
	kept := make([]string, 0, len(out))
	for _, sc := range out {
		if res, ok := strings.CutSuffix(sc, ":read"); ok && slices.Contains(out, res+":write") {
			continue
		}
		kept = append(kept, sc)
	}
	slices.Sort(kept)
	return kept
}
