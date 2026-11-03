package graphql

import (
	"errors"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/orgs"
	"github.com/Ervisio/ErvisioRepo/internal/releases"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

func esc(s string) string { return url.PathEscape(s) }

func (rq *request) base() string { return rq.s.d.PublicURL }

func (rq *request) repoHTML(r repos.Repo) string {
	return rq.base() + "/" + esc(r.Owner) + "/" + esc(r.Name)
}

// ---- users and organizations ----

// userV is a user known by name, and possibly by id.
type userV struct {
	name string
	id   uuid.UUID
}

// userObj is the User object of the user called name; nil for no name.
func userObj(name string) any {
	if name == "" {
		return nil
	}
	return obj{"User", &userV{name: name}}
}

// ownerObj is the RepositoryOwner object of a namespace.
func ownerObj(id uuid.UUID, kind, name string) any {
	if kind == "org" {
		return obj{"Organization", &orgV{name: name, id: id}}
	}
	return obj{"User", &userV{name: name, id: id}}
}

// ownerByName is the RepositoryOwner called name, nil when unknown.
func (rq *request) ownerByName(name string) (any, error) {
	ns, err := rq.namespace(name)
	if err != nil || ns == nil {
		return nil, err
	}
	return ownerObj(ns.ID, ns.Kind, ns.Name), nil
}

func (rq *request) userID(u *userV) (uuid.UUID, error) {
	if u.id != uuid.Nil {
		return u.id, nil
	}
	ns, err := rq.namespace(u.name)
	if err != nil || ns == nil {
		return uuid.Nil, err
	}
	u.id = ns.ID
	return u.id, nil
}

// profile returns a field of the profile of u ("" when unknown).
func (rq *request) profile(u *userV, f func(p userProfile) any) (any, error) {
	id, err := rq.userID(u)
	if err != nil || id == uuid.Nil {
		return nil, err
	}
	p, err := rq.identityUser(id)
	if err != nil || p == nil {
		return nil, err
	}
	return f(userProfile{name: p.DisplayName, avatar: p.AvatarURL, email: p.Email, created: p.CreatedAt,
		admin: p.Role == auth.RoleAdmin, self: id == rq.who.UserID}), nil
}

type userProfile struct {
	name, avatar, email string
	created             any
	admin, self         bool
}

func optString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func userFields() map[string]resolver {
	u := func(src any) *userV { return src.(*userV) }
	return map[string]resolver{
		"id": func(rq *request, src any, a args) (any, error) {
			id, err := rq.userID(u(src))
			if err != nil || id == uuid.Nil {
				return nil, err
			}
			return nodeID("User", id), nil
		},
		"databaseId": func(rq *request, src any, a args) (any, error) {
			id, err := rq.userID(u(src))
			if err != nil {
				return nil, err
			}
			return rq.numID(id)
		},
		"login": func(rq *request, src any, a args) (any, error) { return u(src).name, nil },
		"name": func(rq *request, src any, a args) (any, error) {
			return rq.profile(u(src), func(p userProfile) any { return optString(p.name) })
		},
		"avatarUrl": func(rq *request, src any, a args) (any, error) {
			return rq.profile(u(src), func(p userProfile) any { return p.avatar })
		},
		"email": func(rq *request, src any, a args) (any, error) {
			return rq.profile(u(src), func(p userProfile) any {
				if p.self && rq.who.HasScope(auth.ScopeReadUser) {
					return p.email
				}
				return ""
			})
		},
		"createdAt": func(rq *request, src any, a args) (any, error) {
			return rq.profile(u(src), func(p userProfile) any { return p.created })
		},
		"isSiteAdmin": func(rq *request, src any, a args) (any, error) {
			v, err := rq.profile(u(src), func(p userProfile) any { return p.admin })
			if v == nil {
				v = false
			}
			return v, err
		},
		"isViewer": func(rq *request, src any, a args) (any, error) {
			return strings.EqualFold(u(src).name, rq.who.Username), nil
		},
		"url":          func(rq *request, src any, a args) (any, error) { return rq.base() + "/" + esc(u(src).name), nil },
		"resourcePath": func(rq *request, src any, a args) (any, error) { return "/" + esc(u(src).name), nil },
		"bio":          constant(""),
		"company":      constant(nil),
		"location":     constant(nil),
		"websiteUrl":   constant(nil),
		"repositories": func(rq *request, src any, a args) (any, error) {
			return rq.ownerRepos(u(src).name, a)
		},
		"organizations": func(rq *request, src any, a args) (any, error) {
			if !strings.EqualFold(u(src).name, rq.who.Username) {
				return obj{"OrganizationConnection", staticConn("Organization", a, nil)}, nil
			}
			if err := rq.scope(auth.ScopeReadUser); err != nil {
				return nil, err
			}
			list, err := rq.s.d.Orgs.ListMyOrgs(rq.ctx, rq.who)
			if err != nil {
				return nil, err
			}
			items := make([]any, len(list))
			for i, o := range list {
				items[i] = obj{"Organization", &orgV{name: o.Name, id: o.ID, org: &list[i]}}
			}
			return obj{"OrganizationConnection", staticConn("Organization", a, items)}, nil
		},
		"followers":           emptyConn("User"),
		"following":           emptyConn("User"),
		"starredRepositories": emptyConn("Repository"),
	}
}

// orgV is an organization known by name.
type orgV struct {
	name string
	id   uuid.UUID
	org  *orgs.Org
}

func (rq *request) loadOrg(o *orgV) (*orgs.Org, error) {
	if o.org != nil {
		return o.org, nil
	}
	org, err := rq.s.d.Orgs.GetOrg(rq.ctx, rq.who, o.name)
	if err != nil {
		return nil, err
	}
	o.org, o.id = &org, org.ID
	return o.org, nil
}

func orgFields() map[string]resolver {
	o := func(src any) *orgV { return src.(*orgV) }
	field := func(f func(org *orgs.Org) any) resolver {
		return func(rq *request, src any, a args) (any, error) {
			org, err := rq.loadOrg(o(src))
			if err != nil {
				return nil, err
			}
			return f(org), nil
		}
	}
	return map[string]resolver{
		"id": func(rq *request, src any, a args) (any, error) {
			if o(src).id == uuid.Nil {
				if _, err := rq.loadOrg(o(src)); err != nil {
					return nil, err
				}
			}
			return nodeID("Organization", o(src).id), nil
		},
		"databaseId": func(rq *request, src any, a args) (any, error) {
			if o(src).id == uuid.Nil {
				if _, err := rq.loadOrg(o(src)); err != nil {
					return nil, err
				}
			}
			return rq.numID(o(src).id)
		},
		"login":           func(rq *request, src any, a args) (any, error) { return o(src).name, nil },
		"name":            field(func(org *orgs.Org) any { return optString(org.DisplayName) }),
		"description":     field(func(org *orgs.Org) any { return optString(org.Description) }),
		"createdAt":       field(func(org *orgs.Org) any { return org.CreatedAt }),
		"isVerified":      field(func(org *orgs.Org) any { return org.Verified }),
		"viewerIsAMember": field(func(org *orgs.Org) any { return org.Role.Member() }),
		"viewerCanAdminister": field(func(org *orgs.Org) any {
			return org.Role.Manager()
		}),
		"avatarUrl":    constant(""),
		"url":          func(rq *request, src any, a args) (any, error) { return rq.base() + "/" + esc(o(src).name), nil },
		"resourcePath": func(rq *request, src any, a args) (any, error) { return "/" + esc(o(src).name), nil },
		"repositories": func(rq *request, src any, a args) (any, error) {
			return rq.ownerRepos(o(src).name, a)
		},
	}
}

// ownerRepos is the repositories connection of an owner. Forks and archived
// repositories do not exist, so isFork/isArchived true is empty.
func (rq *request) ownerRepos(owner string, a args) (any, error) {
	if err := rq.read(); err != nil {
		return nil, err
	}
	fetch := func(off, lim int) ([]any, bool, error) {
		list, err := rq.s.d.Repos.ListOwner(rq.ctx, rq.who, owner, off, lim)
		if err != nil {
			return nil, false, err
		}
		return repoItems(rq, list), len(list) == lim, nil
	}
	return obj{"RepositoryConnection", newConn("Repository", a, filtered(fetch, repoFilter(a)))}, nil
}

func repoItems(rq *request, list []repos.Repo) []any {
	items := make([]any, len(list))
	for i, r := range list {
		rq.repos[strings.ToLower(r.Owner+"/"+r.Name)] = r
		items[i] = obj{"Repository", r}
	}
	return items
}

// repoFilter applies the privacy, isFork and isArchived arguments.
func repoFilter(a args) func(any) bool {
	privacy := strings.ToUpper(a.str("privacy"))
	fork, forkSet := a.boolean("isFork")
	archived, archSet := a.boolean("isArchived")
	return func(it any) bool {
		r := it.(obj).v.(repos.Repo)
		if (forkSet && fork) || (archSet && archived) {
			return false
		}
		switch privacy {
		case "PUBLIC":
			return r.Visibility == authz.Public
		case "PRIVATE":
			return r.Visibility != authz.Public
		}
		return true
	}
}

// ---- labels, milestones, releases, reactions ----

type labelV struct {
	r repos.Repo
	l issues.Label
}

func labelFields() map[string]resolver {
	l := func(src any) *labelV { return src.(*labelV) }
	return map[string]resolver{
		"id":          func(rq *request, src any, a args) (any, error) { return nodeID("Label", l(src).l.ID), nil },
		"name":        func(rq *request, src any, a args) (any, error) { return l(src).l.Name, nil },
		"color":       func(rq *request, src any, a args) (any, error) { return l(src).l.Color, nil },
		"description": func(rq *request, src any, a args) (any, error) { return optString(l(src).l.Description), nil },
		"isDefault":   constant(false),
		"url": func(rq *request, src any, a args) (any, error) {
			return rq.repoHTML(l(src).r) + "/labels/" + esc(l(src).l.Name), nil
		},
		"repository": func(rq *request, src any, a args) (any, error) { return obj{"Repository", l(src).r}, nil },
	}
}

func labelObj(r repos.Repo, l issues.Label) obj { return obj{"Label", &labelV{r: r, l: l}} }

type milestoneV struct {
	r repos.Repo
	m issues.Milestone
}

func milestoneFields() map[string]resolver {
	m := func(src any) *milestoneV { return src.(*milestoneV) }
	return map[string]resolver{
		"id":     func(rq *request, src any, a args) (any, error) { return nodeID("Milestone", m(src).m.ID), nil },
		"number": func(rq *request, src any, a args) (any, error) { return rq.numID(m(src).m.ID) },
		"title":  func(rq *request, src any, a args) (any, error) { return m(src).m.Title, nil },
		"description": func(rq *request, src any, a args) (any, error) {
			return optString(m(src).m.Description), nil
		},
		"state":     func(rq *request, src any, a args) (any, error) { return strings.ToUpper(m(src).m.State), nil },
		"closed":    func(rq *request, src any, a args) (any, error) { return m(src).m.State == "closed", nil },
		"dueOn":     func(rq *request, src any, a args) (any, error) { return m(src).m.DueOn, nil },
		"createdAt": func(rq *request, src any, a args) (any, error) { return m(src).m.CreatedAt, nil },
		"updatedAt": func(rq *request, src any, a args) (any, error) { return m(src).m.UpdatedAt, nil },
		"closedAt":  func(rq *request, src any, a args) (any, error) { return m(src).m.ClosedAt, nil },
		"progressPercentage": func(rq *request, src any, a args) (any, error) {
			t := m(src).m.OpenIssues + m(src).m.ClosedIssues
			if t == 0 {
				return 0.0, nil
			}
			return float64(m(src).m.ClosedIssues) * 100 / float64(t), nil
		},
		"url": func(rq *request, src any, a args) (any, error) {
			n, err := rq.numID(m(src).m.ID)
			if err != nil {
				return nil, err
			}
			return rq.repoHTML(m(src).r) + "/milestone/" + toString(n), nil
		},
	}
}

func toString(v any) string { return args{"v": v}.str("v") }

type releaseV struct {
	r   repos.Repo
	rel releases.Release
}

func releaseFields() map[string]resolver {
	r := func(src any) *releaseV { return src.(*releaseV) }
	return map[string]resolver{
		"id":           func(rq *request, src any, a args) (any, error) { return nodeID("Release", r(src).rel.ID), nil },
		"databaseId":   func(rq *request, src any, a args) (any, error) { return rq.numID(r(src).rel.ID) },
		"name":         func(rq *request, src any, a args) (any, error) { return optString(r(src).rel.Name), nil },
		"tagName":      func(rq *request, src any, a args) (any, error) { return r(src).rel.Tag, nil },
		"description":  func(rq *request, src any, a args) (any, error) { return r(src).rel.Body, nil },
		"isDraft":      func(rq *request, src any, a args) (any, error) { return r(src).rel.Draft, nil },
		"isPrerelease": func(rq *request, src any, a args) (any, error) { return r(src).rel.Prerelease, nil },
		"isLatest":     func(rq *request, src any, a args) (any, error) { return r(src).rel.Latest, nil },
		"createdAt":    func(rq *request, src any, a args) (any, error) { return r(src).rel.CreatedAt, nil },
		"publishedAt":  func(rq *request, src any, a args) (any, error) { return r(src).rel.PublishedAt, nil },
		"author":       func(rq *request, src any, a args) (any, error) { return userObj(r(src).rel.Author), nil },
		"url": func(rq *request, src any, a args) (any, error) {
			return rq.repoHTML(r(src).r) + "/releases/tag/" + esc(r(src).rel.Tag), nil
		},
		"tagCommit": func(rq *request, src any, a args) (any, error) {
			if r(src).rel.Commit == "" {
				return nil, nil
			}
			return commitObj(r(src).r, r(src).rel.Commit, nil), nil
		},
		"releaseAssets": func(rq *request, src any, a args) (any, error) {
			items := make([]any, len(r(src).rel.Assets))
			for i, as := range r(src).rel.Assets {
				items[i] = obj{"ReleaseAsset", &assetV{r: r(src), a: as}}
			}
			return obj{"ReleaseAssetConnection", staticConn("ReleaseAsset", a, items)}, nil
		},
	}
}

type assetV struct {
	r *releaseV
	a releases.Asset
}

func assetFields() map[string]resolver {
	as := func(src any) *assetV { return src.(*assetV) }
	return map[string]resolver{
		"id":            func(rq *request, src any, a args) (any, error) { return nodeID("ReleaseAsset", as(src).a.ID), nil },
		"name":          func(rq *request, src any, a args) (any, error) { return as(src).a.Name, nil },
		"contentType":   func(rq *request, src any, a args) (any, error) { return as(src).a.ContentType, nil },
		"size":          func(rq *request, src any, a args) (any, error) { return as(src).a.Size, nil },
		"downloadCount": func(rq *request, src any, a args) (any, error) { return as(src).a.DownloadCount, nil },
		"createdAt":     func(rq *request, src any, a args) (any, error) { return as(src).a.CreatedAt, nil },
		"downloadUrl": func(rq *request, src any, a args) (any, error) {
			v := as(src)
			return rq.repoHTML(v.r.r) + "/releases/download/" + esc(v.r.rel.Tag) + "/" + esc(v.a.Name), nil
		},
	}
}

// reactionContent maps a reaction content of the services to its enum.
func reactionContent(c string) string {
	switch c {
	case "+1":
		return "THUMBS_UP"
	case "-1":
		return "THUMBS_DOWN"
	}
	return strings.ToUpper(c)
}

func reactionGroups(list []issues.ReactionGroup) []any {
	out := make([]any, 0, len(list))
	for _, g := range list {
		if g.Count > 0 {
			out = append(out, obj{"ReactionGroup", g})
		}
	}
	return out
}

func reactionGroupFields() map[string]resolver {
	g := func(src any) issues.ReactionGroup { return src.(issues.ReactionGroup) }
	return map[string]resolver{
		"content":          func(rq *request, src any, a args) (any, error) { return reactionContent(g(src).Content), nil },
		"viewerHasReacted": func(rq *request, src any, a args) (any, error) { return g(src).ViewerReacted, nil },
		"users": func(rq *request, src any, a args) (any, error) {
			n := int(g(src).Count)
			c := staticConn("User", a, nil)
			c.total = func() (int, error) { return n, nil }
			return obj{"ReactingUserConnection", c}, nil
		},
		"reactors": func(rq *request, src any, a args) (any, error) {
			n := int(g(src).Count)
			c := staticConn("Reactor", a, nil)
			c.total = func() (int, error) { return n, nil }
			return obj{"ReactorConnection", c}, nil
		},
	}
}

// ---- repositories ----

// permissionEnum maps a permission to RepositoryPermission.
func permissionEnum(p authz.Permission) any {
	switch p {
	case authz.PermAdmin:
		return "ADMIN"
	case authz.PermMaintain:
		return "MAINTAIN"
	case authz.PermWrite:
		return "WRITE"
	case authz.PermTriage:
		return "TRIAGE"
	case authz.PermRead:
		return "READ"
	}
	return nil
}

func (rq *request) sshURL(r repos.Repo) string {
	return "git@" + rq.s.host + ":" + r.Owner + "/" + r.Name + ".git"
}

func repositoryFields() map[string]resolver {
	r := func(src any) repos.Repo { return src.(repos.Repo) }
	f := func(fn func(rq *request, r repos.Repo) any) resolver {
		return func(rq *request, src any, a args) (any, error) { return fn(rq, r(src)), nil }
	}
	return map[string]resolver{
		"id":                            f(func(rq *request, r repos.Repo) any { return nodeID("Repository", r.ID) }),
		"databaseId":                    func(rq *request, src any, a args) (any, error) { return rq.numID(r(src).ID) },
		"name":                          f(func(rq *request, r repos.Repo) any { return r.Name }),
		"nameWithOwner":                 f(func(rq *request, r repos.Repo) any { return r.Owner + "/" + r.Name }),
		"owner":                         f(func(rq *request, r repos.Repo) any { return ownerObj(r.OwnerID, r.OwnerKind, r.Owner) }),
		"description":                   f(func(rq *request, r repos.Repo) any { return optString(r.Description) }),
		"url":                           f(func(rq *request, r repos.Repo) any { return rq.repoHTML(r) }),
		"resourcePath":                  f(func(rq *request, r repos.Repo) any { return "/" + esc(r.Owner) + "/" + esc(r.Name) }),
		"sshUrl":                        f(func(rq *request, r repos.Repo) any { return rq.sshURL(r) }),
		"homepageUrl":                   constant(nil),
		"mirrorUrl":                     constant(nil),
		"openGraphImageUrl":             constant(nil),
		"usesCustomOpenGraphImage":      constant(false),
		"isPrivate":                     f(func(rq *request, r repos.Repo) any { return r.Visibility != authz.Public }),
		"visibility":                    f(func(rq *request, r repos.Repo) any { return strings.ToUpper(string(r.Visibility)) }),
		"isFork":                        constant(false),
		"isArchived":                    constant(false),
		"isTemplate":                    constant(false),
		"isMirror":                      constant(false),
		"isLocked":                      constant(false),
		"isDisabled":                    constant(false),
		"isEmpty":                       f(func(rq *request, r repos.Repo) any { return r.SizeBytes == 0 }),
		"isInOrganization":              f(func(rq *request, r repos.Repo) any { return r.OwnerKind == "org" }),
		"isSecurityPolicyEnabled":       constant(false),
		"isUserConfigurationRepository": constant(false),
		"hasIssuesEnabled":              constant(true),
		"hasWikiEnabled":                constant(false),
		"hasProjectsEnabled":            constant(false),
		"hasDiscussionsEnabled":         constant(false),
		"forkingAllowed":                constant(false),
		"mergeCommitAllowed":            constant(true),
		"squashMergeAllowed":            constant(true),
		"rebaseMergeAllowed":            constant(true),
		"deleteBranchOnMerge":           constant(false),
		"viewerDefaultMergeMethod":      constant("MERGE"),
		"viewerDefaultCommitEmail":      constant(nil),
		"viewerPermission":              f(func(rq *request, r repos.Repo) any { return permissionEnum(r.Permission) }),
		"viewerCanAdminister":           f(func(rq *request, r repos.Repo) any { return r.Permission >= authz.PermAdmin }),
		"viewerCanUpdateTopics": f(func(rq *request, r repos.Repo) any {
			return r.Permission >= authz.PermMaintain
		}),
		"viewerHasStarred":   constant(false),
		"viewerSubscription": constant("UNSUBSCRIBED"),
		"stargazerCount":     constant(0),
		"forkCount":          constant(0),
		"diskUsage":          f(func(rq *request, r repos.Repo) any { return r.SizeBytes / 1024 }),
		"createdAt":          f(func(rq *request, r repos.Repo) any { return r.CreatedAt }),
		"updatedAt":          f(func(rq *request, r repos.Repo) any { return r.UpdatedAt }),
		"pushedAt":           f(func(rq *request, r repos.Repo) any { return r.UpdatedAt }),
		"parent":             constant(nil),
		"templateRepository": constant(nil),
		"licenseInfo":        constant(nil),
		"primaryLanguage":    constant(nil),
		"securityPolicyUrl":  constant(nil),
		"codeOfConduct":      constant(nil),
		"fundingLinks":       constant([]any{}),
		"defaultBranchRef": f(func(rq *request, r repos.Repo) any {
			if r.DefaultBranch == "" {
				return nil
			}
			return obj{"Ref", &refV{r: r, name: r.DefaultBranch}}
		}),
		"stargazers":       emptyConn("User"),
		"watchers":         emptyConn("User"),
		"forks":            emptyConn("Repository"),
		"languages":        emptyConn("Language"),
		"repositoryTopics": emptyConn("RepositoryTopic"),
		"projects":         emptyConn("Project"),
		"projectsV2":       emptyConn("ProjectV2"),
		"issues":           func(rq *request, src any, a args) (any, error) { return rq.repoIssues(r(src), a) },
		"pullRequests":     func(rq *request, src any, a args) (any, error) { return rq.repoPulls(r(src), a) },
		"issue": func(rq *request, src any, a args) (any, error) {
			n, _ := a.int64("number")
			return rq.issueByNumber(r(src), n, false)
		},
		"pullRequest": func(rq *request, src any, a args) (any, error) {
			n, _ := a.int64("number")
			return rq.pullByNumber(r(src), n)
		},
		"issueOrPullRequest": func(rq *request, src any, a args) (any, error) {
			n, _ := a.int64("number")
			return rq.issueByNumber(r(src), n, true)
		},
		"labels":           func(rq *request, src any, a args) (any, error) { return rq.repoLabels(r(src), a) },
		"label":            func(rq *request, src any, a args) (any, error) { return rq.repoLabel(r(src), a.str("name")) },
		"milestones":       func(rq *request, src any, a args) (any, error) { return rq.repoMilestones(r(src), a) },
		"milestone":        func(rq *request, src any, a args) (any, error) { return rq.repoMilestone(r(src), a) },
		"assignableUsers":  func(rq *request, src any, a args) (any, error) { return rq.repoUsers(r(src), a) },
		"mentionableUsers": func(rq *request, src any, a args) (any, error) { return rq.repoUsers(r(src), a) },
		"collaborators":    func(rq *request, src any, a args) (any, error) { return rq.repoUsers(r(src), a) },
		"releases":         func(rq *request, src any, a args) (any, error) { return rq.repoReleases(r(src), a) },
		"release": func(rq *request, src any, a args) (any, error) {
			list, err := rq.s.d.Releases.List(rq.ctx, rq.who, r(src).Owner, r(src).Name, a.str("tagName"), 0, 1)
			if err != nil || len(list) == 0 {
				return nil, err
			}
			return obj{"Release", &releaseV{r: r(src), rel: list[0]}}, nil
		},
		"latestRelease": func(rq *request, src any, a args) (any, error) {
			rel, err := rq.s.d.Releases.Latest(rq.ctx, rq.who, r(src).Owner, r(src).Name)
			if isNotFound(rq, err) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return obj{"Release", &releaseV{r: r(src), rel: rel}}, nil
		},
		"ref": func(rq *request, src any, a args) (any, error) {
			name := strings.TrimPrefix(a.str("qualifiedName"), "refs/heads/")
			if name == "" {
				return nil, nil
			}
			return obj{"Ref", &refV{r: r(src), name: name}}, nil
		},
	}
}

// isNotFound reports whether err is a not-found error of a service.
func isNotFound(rq *request, err error) bool {
	if err == nil {
		return false
	}
	status, _ := rq.s.d.Classify(err)
	return status == 404
}

// refV is a branch of a repository; sha is filled when known.
type refV struct {
	r    repos.Repo
	name string
	sha  string
}

func refFields() map[string]resolver {
	ref := func(src any) *refV { return src.(*refV) }
	return map[string]resolver{
		"id": func(rq *request, src any, a args) (any, error) {
			return nodeIDString("Ref", ref(src).r.ID.String()+":"+ref(src).name), nil
		},
		"name":          func(rq *request, src any, a args) (any, error) { return ref(src).name, nil },
		"prefix":        constant("refs/heads/"),
		"qualifiedName": func(rq *request, src any, a args) (any, error) { return "refs/heads/" + ref(src).name, nil },
		"repository":    func(rq *request, src any, a args) (any, error) { return obj{"Repository", ref(src).r}, nil },
		"target": func(rq *request, src any, a args) (any, error) {
			if ref(src).sha == "" {
				return nil, nil
			}
			return commitObj(ref(src).r, ref(src).sha, nil), nil
		},
	}
}

func (rq *request) repoLabels(r repos.Repo, a args) (any, error) {
	query := strings.ToLower(a.str("query"))
	fetch := func(off, lim int) ([]any, bool, error) {
		list, err := rq.s.d.Issues.ListLabels(rq.ctx, rq.who, r.Owner, r.Name, off, lim)
		if err != nil {
			return nil, false, err
		}
		items := make([]any, len(list))
		for i, l := range list {
			items[i] = labelObj(r, l)
		}
		return items, len(list) == lim, nil
	}
	if query != "" {
		fetch = filtered(fetch, func(it any) bool {
			l := it.(obj).v.(*labelV).l
			return strings.Contains(strings.ToLower(l.Name), query) || strings.Contains(strings.ToLower(l.Description), query)
		})
	}
	return obj{"LabelConnection", newConn("Label", a, fetch)}, nil
}

func (rq *request) repoLabel(r repos.Repo, name string) (any, error) {
	for off := 0; off < maxScan; off += maxPage {
		list, err := rq.s.d.Issues.ListLabels(rq.ctx, rq.who, r.Owner, r.Name, off, maxPage)
		if err != nil {
			return nil, err
		}
		for _, l := range list {
			if strings.EqualFold(l.Name, name) {
				return labelObj(r, l), nil
			}
		}
		if len(list) < maxPage {
			break
		}
	}
	return nil, nil
}

// milestoneState maps the states argument to the milestone filter.
func milestoneState(a args) string {
	st := enumList(a, "states")
	switch {
	case len(st) == 1 && st[0] == "OPEN":
		return "open"
	case len(st) == 1 && st[0] == "CLOSED":
		return "closed"
	}
	return "all"
}

func (rq *request) milestoneFetch(r repos.Repo, state string) fetchFn {
	return func(off, lim int) ([]any, bool, error) {
		list, err := rq.s.d.Issues.ListMilestones(rq.ctx, rq.who, r.Owner, r.Name, state, off, lim)
		if err != nil {
			return nil, false, err
		}
		items := make([]any, len(list))
		for i, m := range list {
			items[i] = obj{"Milestone", &milestoneV{r: r, m: m}}
		}
		return items, len(list) == lim, nil
	}
}

func (rq *request) repoMilestones(r repos.Repo, a args) (any, error) {
	fetch := rq.milestoneFetch(r, milestoneState(a))
	if q := strings.ToLower(a.str("query")); q != "" {
		fetch = filtered(fetch, func(it any) bool {
			return strings.Contains(strings.ToLower(it.(obj).v.(*milestoneV).m.Title), q)
		})
	}
	return obj{"MilestoneConnection", newConn("Milestone", a, fetch)}, nil
}

func (rq *request) repoMilestone(r repos.Repo, a args) (any, error) {
	n, _ := a.int64("number")
	items, _, err := filtered(rq.milestoneFetch(r, "all"), func(it any) bool {
		id, err := rq.numID(it.(obj).v.(*milestoneV).m.ID)
		return err == nil && id == n
	})(0, 1)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return items[0], nil
}

// milestoneByTitle returns the milestone of r called title.
func (rq *request) milestoneByTitle(r repos.Repo, title string) (*issues.Milestone, error) {
	items, _, err := filtered(rq.milestoneFetch(r, "all"), func(it any) bool {
		return strings.EqualFold(it.(obj).v.(*milestoneV).m.Title, title)
	})(0, 1)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0].(obj).v.(*milestoneV).m, nil
}

// repoUsers lists the users with access to r: its owner (for a personal
// repository) and its collaborators.
func (rq *request) repoUsers(r repos.Repo, a args) (any, error) {
	list, err := rq.s.d.Repos.ListCollaborators(rq.ctx, rq.who, r.Owner, r.Name)
	if errors.Is(err, repos.ErrForbidden) {
		list, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []any
	seen := map[uuid.UUID]bool{}
	if r.OwnerKind == "user" {
		seen[r.OwnerID] = true
		items = append(items, obj{"User", &userV{name: r.Owner, id: r.OwnerID}})
	}
	q := strings.ToLower(a.str("query"))
	for _, c := range list {
		if seen[c.UserID] {
			continue
		}
		seen[c.UserID] = true
		if q != "" && !strings.Contains(strings.ToLower(c.Username), q) && !strings.Contains(strings.ToLower(c.DisplayName), q) {
			continue
		}
		items = append(items, obj{"User", &userV{name: c.Username, id: c.UserID}})
	}
	return obj{"UserConnection", staticConn("User", a, items)}, nil
}

func (rq *request) repoReleases(r repos.Repo, a args) (any, error) {
	fetch := func(off, lim int) ([]any, bool, error) {
		list, err := rq.s.d.Releases.List(rq.ctx, rq.who, r.Owner, r.Name, "", off, lim)
		if err != nil {
			return nil, false, err
		}
		items := make([]any, len(list))
		for i, rel := range list {
			items[i] = obj{"Release", &releaseV{r: r, rel: rel}}
		}
		return items, len(list) == lim, nil
	}
	return obj{"ReleaseConnection", newConn("Release", a, fetch)}, nil
}

// containsFold reports whether list contains s, ignoring case.
func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, s) })
}
