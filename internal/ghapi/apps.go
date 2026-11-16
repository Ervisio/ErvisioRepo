package ghapi

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/apps"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// GitHub Apps (APPS-PLAN A2): the endpoints of an App authenticated with
// its JWT (/app…, /repos/{owner}/{repo}/installation…), of installation
// tokens (/installation/…) and of user tokens (/user/installations…), plus
// the route gate that bounds installation and user-to-server tokens to the
// permissions of their App.

const errIntegration = "Resource not accessible by integration"

func (s *Server) appRoutes(r chi.Router) {
	r.Get("/app", s.h((*call).getApp))
	r.Get("/app/installations", s.h((*call).listAppInstallations))
	r.Get("/app/installations/{installation_id}", s.h((*call).getAppInstallation))
	r.Delete("/app/installations/{installation_id}", s.h((*call).deleteAppInstallation))
	r.Post("/app/installations/{installation_id}/access_tokens", s.h((*call).createInstallationToken))
	r.Put("/app/installations/{installation_id}/suspended", s.h(suspendInstallation(true)))
	r.Delete("/app/installations/{installation_id}/suspended", s.h(suspendInstallation(false)))
	r.Get("/orgs/{org}/installation", s.h(accountInstallation("org", apps.OwnerOrg)))
	r.Get("/users/{username}/installation", s.h(accountInstallation("username", apps.OwnerUser)))
	r.Get("/installation/repositories", s.h((*call).installationRepos))
	r.Delete("/installation/token", s.h((*call).revokeInstallationToken))
	r.Get("/user/installations", s.h((*call).userInstallations))
	r.Get("/user/installations/{installation_id}/repositories", s.h((*call).userInstallationRepos))
	r.Get("/apps/{slug}", s.h((*call).publicApp))
}

// ---- route gate ----

// jwtRoutes are the routes an App reaches with its JWT; the JWT works
// nowhere else, and they take nothing else (but /apps/{slug}).
var jwtRoutes = []string{"/app", "/app/installations", "/app/installations/{installation_id}",
	"/app/installations/{installation_id}/access_tokens", "/app/installations/{installation_id}/suspended",
	"/repos/{owner}/{repo}/installation", "/orgs/{org}/installation", "/users/{username}/installation",
	"/apps/{slug}"}

// routeOf is the chi pattern of the request, relative to the API prefix.
func routeOf(r *http.Request) string {
	rc := chi.RouteContext(r.Context())
	if rc == nil {
		return ""
	}
	p := rc.RoutePattern()
	for _, prefix := range []string{Prefix, "/api/uploads", "/api"} {
		if rest, ok := strings.CutPrefix(p, prefix); ok {
			return strings.TrimSuffix(rest, "/")
		}
	}
	return p
}

// appGate bounds the callers tied to an App: the App's JWT reaches the
// jwtRoutes only; installation and user-to-server tokens reach the routes
// their permissions cover (appNeed) and nothing else.
func (c *call) appGate() error {
	route := routeOf(c.r)
	isJWT := slices.Contains(jwtRoutes, route)
	switch {
	case c.who.App != nil:
		if isJWT {
			return nil
		}
		return errf(http.StatusForbidden, "%s: an App JWT only reaches the /app endpoints", errIntegration)
	case isJWT && route != "/apps/{slug}":
		return errf(http.StatusUnauthorized, "A JSON web token could not be decoded")
	}
	perms, bounded := c.who.AppPermissions()
	if !bounded {
		return nil
	}
	need, ok := appNeed(c.r.Method, route, c.who.Installation != nil)
	if !ok {
		return errf(http.StatusForbidden, errIntegration)
	}
	if len(need) == 0 {
		return nil
	}
	level := authz.AppRead
	if c.r.Method != http.MethodGet && c.r.Method != http.MethodHead {
		level = authz.AppWrite
	}
	for _, p := range need {
		if perms.Has(p, level) {
			return nil
		}
	}
	return errf(http.StatusForbidden, errIntegration)
}

var (
	open     = []string{}
	metadata = []string{authz.AppMetadata}
)

// appNeed is what an App-bound token needs for method on route: any of
// the permissions returned, at read level for GET and write otherwise
// (empty: nothing). ok is false for a route such tokens never reach.
func appNeed(method, route string, installation bool) (need []string, ok bool) {
	get := method == http.MethodGet || method == http.MethodHead
	if rest, isRepo := strings.CutPrefix(route, "/repos/{owner}/{repo}"); isRepo {
		return repoNeed(method, rest)
	}
	switch route {
	case "", "/meta", "/rate_limit":
		return open, true
	case "/installation/repositories", "/installation/token":
		return open, installation
	case "/user/installations", "/user/installations/{installation_id}/repositories":
		return open, !installation
	case "/user", "/user/repos", "/user/orgs":
		return open, get && !installation
	case "/users/{username}", "/users/{username}/repos", "/users/{username}/starred", "/orgs/{org}",
		"/orgs/{org}/repos":
		return open, get
	}
	for _, p := range []string{"/gitignore/", "/licenses", "/search/"} {
		if strings.HasPrefix(route, p) {
			return open, get
		}
	}
	return nil, false
}

// repoNeed maps the /repos/{owner}/{repo} routes to App permissions.
func repoNeed(method, rest string) ([]string, bool) {
	get := method == http.MethodGet || method == http.MethodHead
	has := func(p string) bool { return strings.HasPrefix(rest, p) }
	switch {
	case rest == "":
		if get {
			return metadata, true
		}
		return []string{authz.AppAdministration}, true
	case rest == "/stargazers", rest == "/forks" && get:
		return metadata, true
	case rest == "/forks":
		return nil, false
	case rest == "/commits/*" && get:
		// A commit, its combined status or its checks.
		return []string{authz.AppContents, authz.AppStatuses, authz.AppChecks}, true
	case has("/statuses"):
		return []string{authz.AppStatuses}, true
	case has("/check-runs"), has("/check-suites"):
		return []string{authz.AppChecks}, true
	case has("/contents"), has("/readme"), has("/branches"), has("/tags"), has("/git/"), has("/commits"),
		has("/releases"), rest == "/merge-upstream":
		return []string{authz.AppContents}, true
	case has("/issues"), has("/labels"), has("/milestones"), has("/assignees"):
		return []string{authz.AppIssues, authz.AppPullRequests}, true
	case has("/pulls"):
		return []string{authz.AppPullRequests}, true
	case has("/actions/secrets"):
		return []string{authz.AppSecrets}, true
	case has("/actions/variables"):
		return []string{authz.AppVariables}, true
	case has("/actions/"):
		return []string{authz.AppActions}, true
	}
	return nil, false
}

// noApps refuses App JWTs and installation tokens on GraphQL, whose
// resolvers have no per-permission gate.
func noApps(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if who, ok := auth.PrincipalFrom(r.Context()); ok && (who.App != nil || who.Installation != nil) {
			writeError(w, http.StatusForbidden, errIntegration+": use the REST API with an installation token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- objects ----

// ghFullApp is the App object of /app and /apps/{slug}.
type ghFullApp struct {
	ghApp
	ClientID           string `json:"client_id,omitempty"`
	InstallationsCount int    `json:"installations_count"`
}

func (c *call) fullApp(a apps.App) ghFullApp {
	perms := map[string]any{}
	for k, v := range a.Permissions {
		perms[k] = v
	}
	events := a.Events
	if events == nil {
		events = []string{}
	}
	created, updated := ts(a.CreatedAt), ts(a.UpdatedAt)
	external := a.HomepageURL
	if external == "" {
		external = c.s.base
	}
	return ghFullApp{ghApp: ghApp{ID: a.NumID, Slug: a.Slug, NodeID: nodeID("App", a.ID),
		Owner: c.userObj(a.OwnerID, a.OwnerKind, a.OwnerName), Name: a.Name, Description: a.Description,
		ExternalURL: external, HTMLURL: c.s.base + "/apps/" + esc(a.Slug), Permissions: perms, Events: events,
		CreatedAt: &created, UpdatedAt: &updated}, ClientID: a.ClientID, InstallationsCount: a.Installations}
}

// reporterApp is the app of a check run or suite: the App that reported
// it with an installation token, else the reporter name (appObj).
func (c *call) reporterApp(name string, appID uuid.UUID) ghApp {
	if appID == uuid.Nil || c.s.d.Apps == nil {
		return c.appObj(name)
	}
	if g, ok := c.apps[appID]; ok {
		return g
	}
	a, err := c.s.d.Apps.AppByID(c.ctx, appID)
	if err != nil {
		return c.appObj(name)
	}
	g := c.fullApp(a).ghApp
	if c.apps == nil {
		c.apps = map[uuid.UUID]ghApp{}
	}
	c.apps[appID] = g
	return g
}

type ghInstallation struct {
	ID                     int64             `json:"id"`
	NodeID                 string            `json:"node_id"`
	Account                *ghUser           `json:"account"`
	RepositorySelection    string            `json:"repository_selection"`
	AccessTokensURL        string            `json:"access_tokens_url"`
	RepositoriesURL        string            `json:"repositories_url"`
	HTMLURL                string            `json:"html_url"`
	AppID                  int64             `json:"app_id"`
	AppSlug                string            `json:"app_slug"`
	TargetID               int64             `json:"target_id"`
	TargetType             string            `json:"target_type"`
	Permissions            map[string]string `json:"permissions"`
	Events                 []string          `json:"events"`
	CreatedAt              string            `json:"created_at"`
	UpdatedAt              string            `json:"updated_at"`
	SingleFileName         *string           `json:"single_file_name"`
	HasMultipleSingleFiles bool              `json:"has_multiple_single_files"`
	SingleFilePaths        []string          `json:"single_file_paths"`
	SuspendedBy            *ghUser           `json:"suspended_by"`
	SuspendedAt            *string           `json:"suspended_at"`
}

func (c *call) installationObj(i apps.Installation) ghInstallation {
	num := strconv.FormatInt(i.NumID, 10)
	acc := c.userObj(i.AccountID, i.AccountKind, i.AccountName)
	events := i.Events
	if events == nil {
		events = []string{}
	}
	return ghInstallation{ID: i.NumID, NodeID: nodeID("Installation", i.ID), Account: acc,
		RepositorySelection: i.RepositorySelection,
		AccessTokensURL:     c.s.api + "/app/installations/" + num + "/access_tokens",
		RepositoriesURL:     c.s.api + "/installation/repositories",
		HTMLURL:             c.s.base + "/settings/installations/" + num, AppID: i.AppNumID, AppSlug: i.AppSlug,
		TargetID: acc.ID, TargetType: acc.Type, Permissions: i.Permissions, Events: events,
		CreatedAt: ts(i.CreatedAt), UpdatedAt: ts(i.UpdatedAt), SingleFilePaths: []string{},
		SuspendedAt: tsp(i.SuspendedAt)}
}

// ghShortRepo is the repository of a new access token.
type ghShortRepo struct {
	ID       int64  `json:"id"`
	NodeID   string `json:"node_id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

// ---- handlers ----

func (c *call) appsSvc() (*apps.Service, error) {
	if c.s.d.Apps == nil {
		return nil, errNotFound
	}
	return c.s.d.Apps, nil
}

// jwtApp is the App of a JWT request.
func (c *call) jwtApp() (*apps.Service, uuid.UUID, error) {
	svc, err := c.appsSvc()
	if err != nil {
		return nil, uuid.Nil, err
	}
	if c.who.App == nil {
		return nil, uuid.Nil, errf(http.StatusUnauthorized, "A JSON web token could not be decoded")
	}
	return svc, c.who.App.ID, nil
}

func (c *call) installationParam() (int64, error) {
	n, err := strconv.ParseInt(chi.URLParam(c.r, "installation_id"), 10, 64)
	if err != nil || n <= 0 {
		return 0, errNotFound
	}
	return n, nil
}

func (c *call) getApp() error {
	svc, id, err := c.jwtApp()
	if err != nil {
		return err
	}
	a, err := svc.AppByID(c.ctx, id)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.fullApp(a))
}

func (c *call) publicApp() error {
	svc, err := c.appsSvc()
	if err != nil {
		return err
	}
	a, err := svc.PublicApp(c.ctx, c.who, chi.URLParam(c.r, "slug"))
	if err != nil {
		return err
	}
	if c.who.App == nil || c.who.App.ID != a.ID {
		a.ClientID = ""
	}
	return c.ok(http.StatusOK, c.fullApp(a))
}

func (c *call) listAppInstallations() error {
	svc, id, err := c.jwtApp()
	if err != nil {
		return err
	}
	p := c.pager()
	list, err := svc.AppInstallations(c.ctx, id, p.offset(), p.limit())
	if err != nil {
		return err
	}
	page, more := cut(p, list)
	out := make([]ghInstallation, 0, len(page))
	for _, i := range page {
		out = append(out, c.installationObj(i))
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

func (c *call) getAppInstallation() error {
	svc, id, err := c.jwtApp()
	if err != nil {
		return err
	}
	num, err := c.installationParam()
	if err != nil {
		return err
	}
	i, err := svc.InstallationOfApp(c.ctx, id, num)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.installationObj(i))
}

func (c *call) deleteAppInstallation() error {
	svc, id, err := c.jwtApp()
	if err != nil {
		return err
	}
	num, err := c.installationParam()
	if err != nil {
		return err
	}
	if err := svc.UninstallAsApp(c.ctx, id, num, c.client().IP); err != nil {
		return err
	}
	return c.noContent()
}

func suspendInstallation(suspend bool) func(c *call) error {
	return func(c *call) error {
		svc, id, err := c.jwtApp()
		if err != nil {
			return err
		}
		num, err := c.installationParam()
		if err != nil {
			return err
		}
		if err := svc.SuspendAsApp(c.ctx, id, num, suspend, c.client().IP); err != nil {
			return err
		}
		return c.noContent()
	}
}

func (c *call) createInstallationToken() error {
	svc, id, err := c.jwtApp()
	if err != nil {
		return err
	}
	num, err := c.installationParam()
	if err != nil {
		return err
	}
	var body struct {
		Repositories  []string          `json:"repositories"`
		RepositoryIDs []int64           `json:"repository_ids"`
		Permissions   map[string]string `json:"permissions"`
	}
	if err := c.decode(&body); err != nil {
		return err
	}
	req := apps.TokenRequest{RepoNames: body.Repositories, Permissions: body.Permissions}
	for _, n := range body.RepositoryIDs {
		u, err := c.ids.parse(strconv.FormatInt(n, 10))
		if err != nil {
			if errors.Is(err, errNotFound) {
				return errf(http.StatusUnprocessableEntity,
					"There is at least one repository that does not exist or is not accessible to the parent installation.")
			}
			return err
		}
		req.RepoIDs = append(req.RepoIDs, u)
	}
	tok, err := svc.CreateAccessToken(c.ctx, id, num, req, c.client().IP)
	if err != nil {
		return err
	}
	out := map[string]any{"token": tok.Token, "expires_at": ts(tok.ExpiresAt), "permissions": tok.Permissions,
		"repository_selection": tok.RepositorySelection}
	if tok.Repos != nil {
		list := make([]ghShortRepo, 0, len(tok.Repos))
		for _, r := range tok.Repos {
			list = append(list, ghShortRepo{ID: c.ids.of(r.ID), NodeID: nodeID("Repository", r.ID), Name: r.Name,
				FullName: r.Owner + "/" + r.Name, Private: r.Visibility != string(authz.Public)})
		}
		out["repositories"] = list
	}
	return c.ok(http.StatusCreated, out)
}

func (c *call) repoInstallation() error {
	svc, id, err := c.jwtApp()
	if err != nil {
		return err
	}
	i, err := svc.InstallationForRepoName(c.ctx, id, c.owner, c.repo)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.installationObj(i))
}

func accountInstallation(param, kind string) func(c *call) error {
	return func(c *call) error {
		svc, id, err := c.jwtApp()
		if err != nil {
			return err
		}
		i, err := svc.InstallationForAccount(c.ctx, id, chi.URLParam(c.r, param), kind)
		if err != nil {
			return err
		}
		return c.ok(http.StatusOK, c.installationObj(i))
	}
}

// repoPage answers the repositories of an installation the caller sees.
func (c *call) repoPage(selection string, refs []apps.RepoRef) error {
	list := make([]repos.Repo, 0, len(refs))
	for _, ref := range refs {
		r, err := c.s.d.Repos.Get(c.ctx, c.who, ref.Owner, ref.Name)
		if err != nil {
			if classify(err) != nil {
				continue // not visible to the caller
			}
			return err
		}
		list = append(list, r)
	}
	p := c.pager()
	page, more := slice(p, list)
	us := make([]uuid.UUID, 0, 2*len(page))
	for _, r := range page {
		us = append(us, r.ID, r.OwnerID)
	}
	c.ids.prefetch(us...)
	out := make([]*ghRepo, 0, len(page))
	for _, r := range page {
		out = append(out, c.repoObj(r))
	}
	c.link(p, more)
	return c.ok(http.StatusOK, map[string]any{"total_count": len(list), "repository_selection": selection,
		"repositories": out})
}

func (c *call) installationRepos() error {
	svc, err := c.appsSvc()
	if err != nil {
		return err
	}
	if c.who.Installation == nil {
		return errf(http.StatusForbidden, "This endpoint requires an installation access token")
	}
	inst, refs, err := svc.GrantRepos(c.ctx, c.who.Installation)
	if err != nil {
		return err
	}
	sel := inst.RepositorySelection
	if !c.who.Installation.AllRepos {
		sel = apps.SelectionSelected
	}
	return c.repoPage(sel, refs)
}

func (c *call) revokeInstallationToken() error {
	svc, err := c.appsSvc()
	if err != nil {
		return err
	}
	if c.who.Installation == nil {
		return errf(http.StatusForbidden, "This endpoint requires an installation access token")
	}
	if err := svc.RevokeToken(c.ctx, c.who.Installation); err != nil {
		return err
	}
	return c.noContent()
}

func (c *call) userInstallations() error {
	svc, err := c.appsSvc()
	if err != nil {
		return err
	}
	if c.who.UserID == uuid.Nil {
		return errAuth
	}
	list, err := svc.UserInstallations(c.ctx, c.who)
	if err != nil {
		return err
	}
	p := c.pager()
	page, more := slice(p, list)
	out := make([]ghInstallation, 0, len(page))
	for _, i := range page {
		out = append(out, c.installationObj(i))
	}
	c.link(p, more)
	return c.ok(http.StatusOK, map[string]any{"total_count": len(list), "installations": out})
}

func (c *call) userInstallationRepos() error {
	svc, err := c.appsSvc()
	if err != nil {
		return err
	}
	if c.who.UserID == uuid.Nil {
		return errAuth
	}
	num, err := c.installationParam()
	if err != nil {
		return err
	}
	inst, err := svc.UserInstallation(c.ctx, c.who, num)
	if err != nil {
		return err
	}
	refs, err := svc.Repos(c.ctx, inst)
	if err != nil {
		return err
	}
	return c.repoPage(inst.RepositorySelection, refs)
}
