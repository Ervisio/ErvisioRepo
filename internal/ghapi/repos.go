package ghapi

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// ---- users ----

// ghUser is a simple user (or organization) object.
type ghUser struct {
	Login             string `json:"login"`
	ID                int64  `json:"id"`
	NodeID            string `json:"node_id"`
	AvatarURL         string `json:"avatar_url"`
	GravatarID        string `json:"gravatar_id"`
	URL               string `json:"url"`
	HTMLURL           string `json:"html_url"`
	FollowersURL      string `json:"followers_url"`
	FollowingURL      string `json:"following_url"`
	GistsURL          string `json:"gists_url"`
	StarredURL        string `json:"starred_url"`
	SubscriptionsURL  string `json:"subscriptions_url"`
	OrganizationsURL  string `json:"organizations_url"`
	ReposURL          string `json:"repos_url"`
	EventsURL         string `json:"events_url"`
	ReceivedEventsURL string `json:"received_events_url"`
	Type              string `json:"type"`
	SiteAdmin         bool   `json:"site_admin"`
}

// userObj builds the user object of the namespace id/kind/name.
func (c *call) userObj(id uuid.UUID, kind, name string) *ghUser {
	typ, node, api := "User", "User", c.s.api+"/users/"+esc(name)
	if kind == "org" {
		typ, node = "Organization", "Organization"
	}
	return &ghUser{
		Login: name, ID: c.ids.of(id), NodeID: nodeID(node, id),
		URL: api, HTMLURL: c.s.base + "/" + esc(name),
		FollowersURL: api + "/followers", FollowingURL: api + "/following{/other_user}",
		GistsURL: api + "/gists{/gist_id}", StarredURL: api + "/starred{/owner}{/repo}",
		SubscriptionsURL: api + "/subscriptions", OrganizationsURL: api + "/orgs",
		ReposURL: api + "/repos", EventsURL: api + "/events{/privacy}",
		ReceivedEventsURL: api + "/received_events", Type: typ,
	}
}

// userByName builds the user object of a user known by name only: nil
// for an empty name, id 0 for a name that no longer exists.
func (c *call) userByName(name string) *ghUser {
	if name == "" {
		return nil
	}
	if ns := c.ids.user(name); ns != nil {
		return c.userObj(ns.ID, ns.Kind, ns.Name)
	}
	return c.userObj(uuid.Nil, "user", name)
}

func (c *call) usersByName(names []string) []*ghUser {
	c.ids.prefetchUsers(names...)
	out := make([]*ghUser, 0, len(names))
	for _, n := range names {
		if u := c.userByName(n); u != nil {
			out = append(out, u)
		}
	}
	return out
}

// ghFullUser is the full user object of /user and /users/{username}.
type ghFullUser struct {
	*ghUser
	Name        *string `json:"name"`
	Company     *string `json:"company"`
	Blog        string  `json:"blog"`
	Location    *string `json:"location"`
	Email       *string `json:"email"`
	Hireable    *bool   `json:"hireable"`
	Bio         *string `json:"bio"`
	PublicRepos int     `json:"public_repos"`
	PublicGists int     `json:"public_gists"`
	Followers   int     `json:"followers"`
	Following   int     `json:"following"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (c *call) currentUser() error {
	if c.who.UserID == uuid.Nil {
		return errAuth
	}
	u, err := c.s.d.Identity.User(c.ctx, c.who.UserID)
	if err != nil {
		return err
	}
	out := ghFullUser{ghUser: c.userObj(u.ID, "user", u.Username), Name: strp(u.DisplayName),
		CreatedAt: ts(u.CreatedAt), UpdatedAt: ts(u.CreatedAt)}
	out.AvatarURL = u.AvatarURL
	out.SiteAdmin = u.Role == auth.RoleAdmin
	if c.who.HasScope(auth.ScopeReadUser) {
		out.Email = strp(u.Email)
	}
	return c.ok(http.StatusOK, out)
}

func (c *call) getUser() error {
	// Require a caller: user names are not listed to anonymous clients.
	if c.who.UserID == uuid.Nil {
		return errAuth
	}
	name := chi.URLParam(c.r, "username")
	ns := c.ids.user(name)
	if c.ids.err != nil {
		return c.ids.err
	}
	if ns == nil {
		return errNotFound
	}
	if ns.Kind == "org" {
		return c.orgObj(ns.Name)
	}
	u, err := c.s.d.Identity.User(c.ctx, ns.ID)
	if err != nil {
		return err
	}
	out := ghFullUser{ghUser: c.userObj(u.ID, "user", u.Username), Name: strp(u.DisplayName),
		CreatedAt: ts(u.CreatedAt), UpdatedAt: ts(u.CreatedAt)}
	out.AvatarURL = u.AvatarURL
	out.SiteAdmin = u.Role == auth.RoleAdmin
	return c.ok(http.StatusOK, out)
}

// ---- organizations ----

type ghOrg struct {
	Login            string  `json:"login"`
	ID               int64   `json:"id"`
	NodeID           string  `json:"node_id"`
	URL              string  `json:"url"`
	ReposURL         string  `json:"repos_url"`
	EventsURL        string  `json:"events_url"`
	HooksURL         string  `json:"hooks_url"`
	IssuesURL        string  `json:"issues_url"`
	MembersURL       string  `json:"members_url"`
	PublicMembersURL string  `json:"public_members_url"`
	AvatarURL        string  `json:"avatar_url"`
	Description      *string `json:"description"`
	Name             *string `json:"name,omitempty"`
	HTMLURL          string  `json:"html_url,omitempty"`
	Type             string  `json:"type,omitempty"`
	IsVerified       bool    `json:"is_verified"`
	CreatedAt        string  `json:"created_at,omitempty"`
	UpdatedAt        string  `json:"updated_at,omitempty"`
}

func (c *call) org(id uuid.UUID, name, description string) ghOrg {
	api := c.s.api + "/orgs/" + esc(name)
	return ghOrg{Login: name, ID: c.ids.of(id), NodeID: nodeID("Organization", id), URL: api,
		ReposURL: api + "/repos", EventsURL: api + "/events", HooksURL: api + "/hooks",
		IssuesURL: api + "/issues", MembersURL: api + "/members{/member}",
		PublicMembersURL: api + "/public_members{/member}", Description: &description}
}

func (c *call) orgObj(name string) error {
	if err := c.scope(auth.ScopeReadUser); err != nil {
		return err
	}
	o, err := c.s.d.Orgs.GetOrg(c.ctx, c.who, name)
	if err != nil {
		return err
	}
	out := c.org(o.ID, o.Name, o.Description)
	out.Name, out.HTMLURL, out.Type = strp(o.DisplayName), c.s.base+"/"+esc(o.Name), "Organization"
	out.IsVerified, out.CreatedAt, out.UpdatedAt = o.Verified, ts(o.CreatedAt), ts(o.UpdatedAt)
	return c.ok(http.StatusOK, out)
}

func (c *call) getOrg() error { return c.orgObj(chi.URLParam(c.r, "org")) }

func (c *call) myOrgs() error {
	if c.who.UserID == uuid.Nil {
		return errAuth
	}
	if err := c.scope(auth.ScopeReadUser); err != nil {
		return err
	}
	list, err := c.s.d.Orgs.ListMyOrgs(c.ctx, c.who)
	if err != nil {
		return err
	}
	us := make([]uuid.UUID, len(list))
	for i, o := range list {
		us[i] = o.ID
	}
	c.ids.prefetch(us...)
	p := c.pager()
	page, more := slice(p, list)
	out := make([]ghOrg, len(page))
	for i, o := range page {
		out[i] = c.org(o.ID, o.Name, o.Description)
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

// ---- repositories ----

type ghPermissions struct {
	Admin    bool `json:"admin"`
	Maintain bool `json:"maintain"`
	Push     bool `json:"push"`
	Triage   bool `json:"triage"`
	Pull     bool `json:"pull"`
}

type ghRepo struct {
	ID               int64          `json:"id"`
	NodeID           string         `json:"node_id"`
	Name             string         `json:"name"`
	FullName         string         `json:"full_name"`
	Owner            *ghUser        `json:"owner"`
	Private          bool           `json:"private"`
	HTMLURL          string         `json:"html_url"`
	Description      *string        `json:"description"`
	Fork             bool           `json:"fork"`
	URL              string         `json:"url"`
	ArchiveURL       string         `json:"archive_url"`
	AssigneesURL     string         `json:"assignees_url"`
	BranchesURL      string         `json:"branches_url"`
	CollaboratorsURL string         `json:"collaborators_url"`
	CommentsURL      string         `json:"comments_url"`
	CommitsURL       string         `json:"commits_url"`
	ContentsURL      string         `json:"contents_url"`
	GitRefsURL       string         `json:"git_refs_url"`
	IssuesURL        string         `json:"issues_url"`
	LabelsURL        string         `json:"labels_url"`
	MilestonesURL    string         `json:"milestones_url"`
	PullsURL         string         `json:"pulls_url"`
	ReleasesURL      string         `json:"releases_url"`
	StatusesURL      string         `json:"statuses_url"`
	TagsURL          string         `json:"tags_url"`
	CloneURL         string         `json:"clone_url"`
	GitURL           string         `json:"git_url"`
	SSHURL           string         `json:"ssh_url"`
	Homepage         *string        `json:"homepage"`
	Language         *string        `json:"language"`
	Size             int64          `json:"size"`
	DefaultBranch    string         `json:"default_branch"`
	Visibility       string         `json:"visibility"`
	Archived         bool           `json:"archived"`
	Disabled         bool           `json:"disabled"`
	IsTemplate       bool           `json:"is_template"`
	HasIssues        bool           `json:"has_issues"`
	HasProjects      bool           `json:"has_projects"`
	HasWiki          bool           `json:"has_wiki"`
	HasPages         bool           `json:"has_pages"`
	HasDownloads     bool           `json:"has_downloads"`
	HasDiscussions   bool           `json:"has_discussions"`
	ForksCount       int            `json:"forks_count"`
	StargazersCount  int            `json:"stargazers_count"`
	WatchersCount    int            `json:"watchers_count"`
	OpenIssuesCount  int            `json:"open_issues_count"`
	Topics           []string       `json:"topics"`
	PushedAt         string         `json:"pushed_at"`
	CreatedAt        string         `json:"created_at"`
	UpdatedAt        string         `json:"updated_at"`
	Permissions      *ghPermissions `json:"permissions,omitempty"`
	AllowMergeCommit bool           `json:"allow_merge_commit"`
	AllowSquashMerge bool           `json:"allow_squash_merge"`
	AllowRebaseMerge bool           `json:"allow_rebase_merge"`
}

func (c *call) repoObj(r repos.Repo) *ghRepo {
	if g, ok := c.repos[r.ID]; ok {
		return g
	}
	api, html := c.s.repoAPI(r.Owner, r.Name), c.s.repoHTML(r.Owner, r.Name)
	host := c.s.base
	if u, err := url.Parse(c.s.base); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	g := &ghRepo{
		ID: c.ids.of(r.ID), NodeID: nodeID("Repository", r.ID), Name: r.Name, FullName: r.Owner + "/" + r.Name,
		Owner: c.userObj(r.OwnerID, r.OwnerKind, r.Owner), Private: r.Visibility != authz.Public,
		HTMLURL: html, Description: strp(r.Description), URL: api,
		ArchiveURL: api + "/{archive_format}{/ref}", AssigneesURL: api + "/assignees{/user}",
		BranchesURL: api + "/branches{/branch}", CollaboratorsURL: api + "/collaborators{/collaborator}",
		CommentsURL: api + "/comments{/number}", CommitsURL: api + "/commits{/sha}",
		ContentsURL: api + "/contents/{+path}", GitRefsURL: api + "/git/refs{/sha}",
		IssuesURL: api + "/issues{/number}", LabelsURL: api + "/labels{/name}",
		MilestonesURL: api + "/milestones{/number}", PullsURL: api + "/pulls{/number}",
		ReleasesURL: api + "/releases{/id}", StatusesURL: api + "/statuses/{sha}", TagsURL: api + "/tags",
		CloneURL: html + ".git", GitURL: html + ".git",
		SSHURL: "git@" + host + ":" + r.Owner + "/" + r.Name + ".git",
		Size:   r.SizeBytes / 1024, DefaultBranch: r.DefaultBranch, Visibility: string(r.Visibility),
		HasIssues: true, HasDownloads: true, Topics: []string{},
		PushedAt: ts(r.UpdatedAt), CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
		AllowMergeCommit: true, AllowSquashMerge: true, AllowRebaseMerge: true,
	}
	if c.who.UserID != uuid.Nil {
		p := r.Permission
		g.Permissions = &ghPermissions{Admin: p >= authz.PermAdmin, Maintain: p >= authz.PermMaintain,
			Push: p >= authz.PermWrite, Triage: p >= authz.PermTriage, Pull: p >= authz.PermRead}
	}
	c.repos[r.ID] = g
	return g
}

// loadRepo returns the repository of the /repos/{owner}/{repo} path after
// checking the read scope of the token.
func (c *call) loadRepo() (repos.Repo, error) {
	if err := c.read(); err != nil {
		return repos.Repo{}, err
	}
	return c.s.d.Repos.Get(c.ctx, c.who, c.owner, c.repo)
}

// loadRepoWrite is loadRepo for a write.
func (c *call) loadRepoWrite() (repos.Repo, error) {
	if err := c.write(); err != nil {
		return repos.Repo{}, err
	}
	return c.s.d.Repos.Get(c.ctx, c.who, c.owner, c.repo)
}

// browseRepo is the repository as the git browsing service sees it.
func browseRepo(r repos.Repo) gitbrowse.Repo {
	return gitbrowse.Repo{ID: r.ID, OwnerID: r.OwnerID, Name: r.Name, DefaultBranch: r.DefaultBranch}
}

func (c *call) getRepo() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.repoObj(r))
}

func (c *call) repoList(list []repos.Repo, p pager) error {
	page, more := cut(p, list)
	us := make([]uuid.UUID, 0, 2*len(page))
	for _, r := range page {
		us = append(us, r.ID, r.OwnerID)
	}
	c.ids.prefetch(us...)
	out := make([]*ghRepo, len(page))
	for i, r := range page {
		out[i] = c.repoObj(r)
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

func (c *call) myRepos() error {
	if c.who.UserID == uuid.Nil {
		return errAuth
	}
	if err := c.read(); err != nil {
		return err
	}
	p := c.pager()
	list, err := c.s.d.Repos.ListMine(c.ctx, c.who, p.offset(), p.limit())
	if err != nil {
		return err
	}
	return c.repoList(list, p)
}

func (s *Server) ownerRepos(param string) func(c *call) error {
	return func(c *call) error {
		if err := c.read(); err != nil {
			return err
		}
		p := c.pager()
		list, err := c.s.d.Repos.ListOwner(c.ctx, c.who, chi.URLParam(c.r, param), p.offset(), p.limit())
		if err != nil {
			return err
		}
		return c.repoList(list, p)
	}
}

// repoBody is the body of a repository create or update. Settings that
// have no equivalent (has_wiki, allow_*...) are accepted and ignored.
type repoBody struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Private       *bool   `json:"private"`
	Visibility    *string `json:"visibility"`
	DefaultBranch *string `json:"default_branch"`
}

func (b repoBody) visibility() *string {
	if b.Visibility != nil {
		return b.Visibility
	}
	if b.Private != nil {
		v := string(authz.Public)
		if *b.Private {
			v = string(authz.Private)
		}
		return &v
	}
	return nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (c *call) createRepo() error {
	if err := c.write(); err != nil {
		return err
	}
	var b repoBody
	if err := c.decode(&b); err != nil {
		return err
	}
	if deref(b.Name) == "" {
		return errf(http.StatusUnprocessableEntity, "Validation Failed: name is required")
	}
	in := repos.CreateInput{Owner: chi.URLParam(c.r, "org"), Name: *b.Name, Description: deref(b.Description),
		Visibility: deref(b.visibility()), DefaultBranch: deref(b.DefaultBranch)}
	r, err := c.s.d.Repos.Create(c.ctx, c.who, in, c.client())
	if err != nil {
		return err
	}
	c.w.Header().Set("Location", c.s.repoAPI(r.Owner, r.Name))
	return c.ok(http.StatusCreated, c.repoObj(r))
}

func (c *call) updateRepo() error {
	if err := c.write(); err != nil {
		return err
	}
	var b repoBody
	if err := c.decode(&b); err != nil {
		return err
	}
	in := repos.UpdateInput{Name: b.Name, Description: b.Description, Visibility: b.visibility(),
		DefaultBranch: b.DefaultBranch}
	r, err := c.s.d.Repos.Update(c.ctx, c.who, c.owner, c.repo, in, c.client())
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.repoObj(r))
}

func (c *call) deleteRepo() error {
	if err := c.write(); err != nil {
		return err
	}
	if err := c.s.d.Repos.Delete(c.ctx, c.who, c.owner, c.repo, c.client()); err != nil {
		return err
	}
	return c.noContent()
}

// ---- service endpoints ----

func (c *call) root() error {
	a := c.s.api
	return c.ok(http.StatusOK, map[string]string{
		"current_user_url":                 a + "/user",
		"current_user_repositories_url":    a + "/user/repos{?type,page,per_page,sort}",
		"organization_url":                 a + "/orgs/{org}",
		"organization_repositories_url":    a + "/orgs/{org}/repos{?type,page,per_page,sort}",
		"rate_limit_url":                   a + "/rate_limit",
		"repository_url":                   a + "/repos/{owner}/{repo}",
		"user_url":                         a + "/users/{user}",
		"user_repositories_url":            a + "/users/{user}/repos{?type,page,per_page,sort}",
		"current_user_authorizations_html": c.s.base + "/settings/tokens",
	})
}

func (c *call) meta() error {
	return c.ok(http.StatusOK, map[string]any{
		"verifiable_password_authentication": false,
		"installed_version":                  c.s.d.Version,
		"api":                                []string{},
		"git":                                []string{},
		"web":                                []string{},
	})
}

// rateLimit reports a fixed budget: the server does not rate limit the
// API by request count.
func (c *call) rateLimit() error {
	reset := time.Now().Add(time.Hour).Unix()
	res := map[string]int64{"limit": 5000, "remaining": 5000, "reset": reset, "used": 0}
	c.w.Header().Set("X-RateLimit-Limit", "5000")
	c.w.Header().Set("X-RateLimit-Remaining", "5000")
	c.w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
	return c.ok(http.StatusOK, map[string]any{
		"resources": map[string]any{"core": res, "search": res, "graphql": res},
		"rate":      res,
	})
}
