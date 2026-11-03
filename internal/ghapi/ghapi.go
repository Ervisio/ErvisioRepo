// Package ghapi serves the GitHub REST v3 compatible subset under /api/v3
// (PLAN §6): enough of repositories, contents, pull requests, issues,
// releases and commit statuses for gh api, go-github and octokit based
// tools, plus the GraphQL subset used by the gh CLI (package graphql,
// mounted at /api/graphql and /api/v3/graphql). docs/api-v3.md lists every
// endpoint.
//
// The handlers call the same services as /api/v1 and authenticate with
// personal access tokens only ("Authorization: token|Bearer <pat>", or
// Basic with the token as password); session cookies are ignored, so the
// API needs no CSRF protection. Numeric ids are mapped to the UUIDs of
// the services by the ghapi_ids table.
package ghapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/ghapi/graphql"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Prefix is where the API is mounted.
const Prefix = "/api/v3"

// uploadPrefix is where release assets are uploaded, as on GitHub
// Enterprise Server (the upload_url of a release points there).
const uploadPrefix = "/api/uploads"

// docsURL is the documentation_url of every error.
const docsURL = "https://docs.github.com/rest"

// Deps are the collaborators of the API. Every service is required: Mount
// answers 503 on the whole prefix when one is missing.
type Deps struct {
	Log *slog.Logger
	// DB stores the numeric ids (satisfied by *pgxpool.Pool).
	DB dbgen.DBTX
	// PublicURL is the external base URL, used in every url field.
	PublicURL string
	Version   string
	// Auth provides the access token store and the trusted proxies.
	Auth     *auth.Middleware
	Repos    Repos
	Orgs     Orgs
	Identity Identity
	Issues   Issues
	Pulls    Pulls
	Releases Releases
	Browse   *gitbrowse.Service
	// Statuses stores commit statuses and checks; nil answers empty lists
	// and 501 on writes.
	Statuses Statuses
	// Timeout bounds the non-streaming requests (0 = none).
	Timeout time.Duration
}

func (d Deps) complete() bool {
	return d.DB != nil && d.Auth != nil && d.Repos != nil && d.Orgs != nil && d.Identity != nil &&
		d.Issues != nil && d.Pulls != nil && d.Releases != nil && d.Browse != nil
}

// Server holds the handlers.
type Server struct {
	d    Deps
	base string // public URL without the trailing slash
	api  string // base + Prefix
	q    *dbgen.Queries
}

// Mount mounts /api/v3 and the upload endpoint on r. The asset transfers
// stream and are mounted outside the request timeout.
func Mount(r chi.Router, d Deps) {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if !d.complete() {
		// Without the services (tests, partial setups) /api/v3 is left to
		// the 404 of /api.
		d.Log.Debug("ghapi: services missing, /api/v3 not mounted")
		return
	}
	base := strings.TrimRight(d.PublicURL, "/")
	s := &Server{d: d, base: base, api: base + Prefix, q: dbgen.New(d.DB)}
	gql := s.graphql()
	r.Route(Prefix, func(v3 chi.Router) {
		v3.Use(s.authenticate)
		v3.NotFound(func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "Not Found") })
		v3.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		})
		// Streaming routes: asset upload and download.
		v3.Post("/repos/{owner}/{repo}/releases/{id}/assets", s.h((*call).uploadAsset))
		v3.Get("/repos/{owner}/{repo}/releases/assets/{id}", s.h((*call).getAsset))
		v3.Group(func(g chi.Router) {
			if d.Timeout > 0 {
				g.Use(middleware.Timeout(d.Timeout))
			}
			s.routes(g)
			g.Handle("/graphql", gql)
		})
	})
	r.With(s.authenticate).Post(uploadPrefix+"/repos/{owner}/{repo}/releases/{id}/assets", s.h((*call).uploadAsset))
	// gh sends most of its commands to GraphQL: /api/graphql on GitHub
	// Enterprise Server, /api/v3/graphql for some clients.
	mw := []func(http.Handler) http.Handler{s.authenticate}
	if d.Timeout > 0 {
		mw = append(mw, middleware.Timeout(d.Timeout))
	}
	r.With(mw...).Handle("/api/graphql", gql)
}

// graphql builds the GraphQL endpoint over the same services; it answers
// 501 when a service lacks a method the resolvers need.
func (s *Server) graphql() http.Handler {
	d := s.d
	gd := graphql.Deps{Log: d.Log, DB: d.DB, PublicURL: d.PublicURL, Orgs: d.Orgs, Identity: d.Identity,
		Releases: d.Releases, Classify: func(err error) (int, string) {
			if e := classify(err); e != nil {
				return e.status, e.msg
			}
			return http.StatusInternalServerError, ""
		}}
	var ok1, ok2, ok3 bool
	gd.Repos, ok1 = d.Repos.(graphql.Repos)
	gd.Issues, ok2 = d.Issues.(graphql.Issues)
	gd.Pulls, ok3 = d.Pulls.(graphql.Pulls)
	if d.Statuses != nil {
		gd.Checks = d.Statuses
	}
	var h http.Handler
	if ok1 && ok2 && ok3 {
		srv, err := graphql.New(gd)
		if err == nil {
			h = srv
		} else {
			d.Log.Warn("ghapi: graphql not available", "error", err)
		}
	}
	if h == nil {
		h = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotImplemented, "GraphQL is not supported by this server; use the REST API under "+Prefix)
		})
	}
	return h
}

func (s *Server) routes(r chi.Router) {
	r.Get("/", s.h((*call).root))
	r.Get("/meta", s.h((*call).meta))
	r.Get("/rate_limit", s.h((*call).rateLimit))
	r.Get("/user", s.h((*call).currentUser))
	r.Get("/user/orgs", s.h((*call).myOrgs))
	r.Get("/user/repos", s.h((*call).myRepos))
	r.Post("/user/repos", s.h((*call).createRepo))
	r.Get("/users/{username}", s.h((*call).getUser))
	r.Get("/users/{username}/repos", s.h(s.ownerRepos("username")))
	r.Get("/orgs/{org}", s.h((*call).getOrg))
	r.Get("/orgs/{org}/repos", s.h(s.ownerRepos("org")))
	r.Post("/orgs/{org}/repos", s.h((*call).createRepo))

	r.Route("/repos/{owner}/{repo}", func(r chi.Router) {
		r.Get("/", s.h((*call).getRepo))
		r.Patch("/", s.h((*call).updateRepo))
		r.Delete("/", s.h((*call).deleteRepo))

		r.Get("/contents", s.h((*call).getContents))
		r.Get("/contents/*", s.h((*call).getContents))
		r.Put("/contents/*", s.h((*call).putContents))
		r.Delete("/contents/*", s.h((*call).deleteContents))
		r.Get("/readme", s.h((*call).getReadme))
		r.Get("/readme/*", s.h((*call).getReadme))
		r.Get("/branches", s.h((*call).listBranches))
		r.Get("/branches/*", s.h((*call).getBranch))
		r.Get("/tags", s.h((*call).listTags))
		r.Get("/git/ref/*", s.h((*call).getRef))
		r.Get("/git/refs", s.h((*call).matchingRefs))
		r.Get("/git/refs/*", s.h((*call).matchingRefs))
		r.Post("/git/refs", s.h((*call).createRef))
		r.Patch("/git/refs/*", s.h((*call).updateRef))
		r.Delete("/git/refs/*", s.h((*call).deleteRef))
		r.Get("/git/matching-refs/*", s.h((*call).matchingRefs))
		r.Get("/commits", s.h((*call).listCommits))
		// commits/{ref}, commits/{ref}/status, /statuses, /check-runs and
		// /check-suites: the ref may contain '/' (see commitRoute).
		r.Get("/commits/*", s.h((*call).commitRoute))

		r.Get("/statuses/*", s.h(refRoute((*call).listStatuses)))
		r.Post("/statuses/*", s.h(refRoute((*call).createStatus)))
		r.Post("/check-runs", s.h((*call).createCheckRun))
		r.Get("/check-runs/{id}", s.h((*call).getCheckRun))
		r.Patch("/check-runs/{id}", s.h((*call).updateCheckRun))
		r.Get("/check-runs/{id}/annotations", s.h((*call).listCheckRunAnnotations))
		r.Post("/check-suites", s.h((*call).unsupportedChecks))
		r.Get("/check-suites/{id}", s.h((*call).getCheckSuite))
		r.Get("/check-suites/{id}/check-runs", s.h((*call).listSuiteCheckRuns))

		r.Get("/issues", s.h((*call).listIssues))
		r.Post("/issues", s.h((*call).createIssue))
		r.Get("/issues/{number}", s.h((*call).getIssue))
		r.Patch("/issues/{number}", s.h((*call).updateIssue))
		r.Get("/issues/{number}/comments", s.h((*call).listIssueComments))
		r.Post("/issues/{number}/comments", s.h((*call).createIssueComment))
		r.Get("/issues/comments/{id}", s.h((*call).getIssueComment))
		r.Patch("/issues/comments/{id}", s.h((*call).updateIssueComment))
		r.Delete("/issues/comments/{id}", s.h((*call).deleteIssueComment))
		r.Get("/issues/{number}/labels", s.h((*call).listIssueLabels))
		r.Post("/issues/{number}/labels", s.h((*call).addIssueLabels))
		r.Put("/issues/{number}/labels", s.h((*call).setIssueLabels))
		r.Delete("/issues/{number}/labels", s.h((*call).clearIssueLabels))
		r.Delete("/issues/{number}/labels/{name}", s.h((*call).removeIssueLabel))
		r.Post("/issues/{number}/assignees", s.h((*call).addAssignees))
		r.Delete("/issues/{number}/assignees", s.h((*call).removeAssignees))
		r.Get("/assignees", s.h((*call).listAssignees))
		r.Get("/assignees/{username}", s.h((*call).checkAssignee))
		r.Get("/labels", s.h((*call).listLabels))
		r.Post("/labels", s.h((*call).createLabel))
		r.Get("/labels/{name}", s.h((*call).getLabel))
		r.Patch("/labels/{name}", s.h((*call).updateLabel))
		r.Delete("/labels/{name}", s.h((*call).deleteLabel))
		r.Get("/milestones", s.h((*call).listMilestones))
		r.Post("/milestones", s.h((*call).createMilestone))
		r.Get("/milestones/{number}", s.h((*call).getMilestone))
		r.Patch("/milestones/{number}", s.h((*call).updateMilestone))
		r.Delete("/milestones/{number}", s.h((*call).deleteMilestone))

		r.Get("/pulls", s.h((*call).listPulls))
		r.Post("/pulls", s.h((*call).createPull))
		r.Get("/pulls/{number}", s.h((*call).getPull))
		r.Patch("/pulls/{number}", s.h((*call).updatePull))
		r.Get("/pulls/{number}/commits", s.h((*call).listPullCommits))
		r.Get("/pulls/{number}/files", s.h((*call).listPullFiles))
		r.Get("/pulls/{number}/merge", s.h((*call).checkMerged))
		r.Put("/pulls/{number}/merge", s.h((*call).mergePull))
		r.Put("/pulls/{number}/update-branch", s.h((*call).updatePullBranch))
		r.Get("/pulls/{number}/reviews", s.h((*call).listReviews))
		r.Post("/pulls/{number}/reviews", s.h((*call).createReview))
		r.Get("/pulls/{number}/reviews/{id}", s.h((*call).getReview))
		r.Delete("/pulls/{number}/reviews/{id}", s.h((*call).deletePendingReview))
		r.Post("/pulls/{number}/reviews/{id}/events", s.h((*call).submitReview))
		r.Put("/pulls/{number}/reviews/{id}/dismissals", s.h((*call).dismissReview))
		r.Get("/pulls/{number}/reviews/{id}/comments", s.h((*call).listReviewComments))
		r.Get("/pulls/{number}/comments", s.h((*call).listPullComments))
		r.Post("/pulls/{number}/comments", s.h((*call).createPullComment))
		r.Patch("/pulls/comments/{id}", s.h((*call).updatePullComment))
		r.Delete("/pulls/comments/{id}", s.h((*call).deletePullComment))
		r.Get("/pulls/{number}/requested_reviewers", s.h((*call).listRequestedReviewers))
		r.Post("/pulls/{number}/requested_reviewers", s.h((*call).requestReviewers))
		r.Delete("/pulls/{number}/requested_reviewers", s.h((*call).removeReviewers))

		r.Get("/releases", s.h((*call).listReleases))
		r.Post("/releases", s.h((*call).createRelease))
		r.Get("/releases/latest", s.h((*call).latestRelease))
		r.Get("/releases/tags/*", s.h((*call).releaseByTag))
		r.Get("/releases/{id}", s.h((*call).getRelease))
		r.Patch("/releases/{id}", s.h((*call).updateRelease))
		r.Delete("/releases/{id}", s.h((*call).deleteRelease))
		r.Get("/releases/{id}/assets", s.h((*call).listAssets))
		r.Patch("/releases/assets/{id}", s.h((*call).updateAsset))
		r.Delete("/releases/assets/{id}", s.h((*call).deleteAsset))
	})
}

// ---- authentication ----

// authenticate loads the access token of the request, if any. Anonymous
// requests go on; an invalid token is refused as GitHub does.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithRequestInfo(r.Context(), auth.RequestInfo{
			IP: auth.ClientIP(r, s.d.Auth.TrustedProxies), UserAgent: r.UserAgent()})
		w.Header().Set("X-GitHub-Media-Type", "github.v3; format=json")
		if tok, ok := credential(r); ok {
			if s.d.Auth.Tokens == nil {
				writeError(w, http.StatusUnauthorized, "Bad credentials")
				return
			}
			p, err := s.d.Auth.Tokens.Lookup(ctx, tok)
			switch {
			case errors.Is(err, auth.ErrNoToken):
				writeError(w, http.StatusUnauthorized, "Bad credentials")
				return
			case err != nil:
				s.d.Log.ErrorContext(ctx, "ghapi: load access token", "error", err)
				writeError(w, http.StatusServiceUnavailable, "Service Unavailable")
				return
			}
			ctx = auth.WithPrincipal(ctx, p)
			w.Header().Set("X-OAuth-Scopes", oauthScopes(p.Scopes))
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// githubScopes maps the token scopes to the classic GitHub scopes that
// clients check in X-OAuth-Scopes (gh requires repo and read:org).
var githubScopes = map[string][]string{
	auth.ScopeWriteRepo:     {"repo"},
	auth.ScopeReadUser:      {"read:user", "read:org"},
	auth.ScopeWriteUser:     {"user", "read:org"},
	auth.ScopeReadPackages:  {"read:packages"},
	auth.ScopeWritePackages: {"write:packages"},
	auth.ScopeWriteActions:  {"workflow"},
	auth.ScopeAdmin:         {"admin:org"},
}

// oauthScopes is the X-OAuth-Scopes value of a token.
func oauthScopes(scopes []string) string {
	var out []string
	for _, sc := range scopes {
		for _, g := range githubScopes[sc] {
			if !slices.Contains(out, g) {
				out = append(out, g)
			}
		}
	}
	return strings.Join(out, ", ")
}

// credential returns the access token of the Authorization header:
// "token <pat>", "Bearer <pat>" or Basic with the token as password (git
// credential helpers and some libraries send that).
func credential(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	if _, pass, ok := r.BasicAuth(); ok {
		return pass, true
	}
	scheme, cred, _ := strings.Cut(h, " ")
	if strings.EqualFold(scheme, "token") || strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(cred), true
	}
	return strings.TrimSpace(h), true
}

// ---- errors ----

// ghError is the error body of the GitHub REST API. It is the documented
// exception to the application/problem+json rule (CLAUDE.md §10): GitHub
// clients (gh, go-github, octokit) parse {"message", "documentation_url"}
// and would not understand an RFC 9457 problem.
type ghError struct {
	Message          string `json:"message"`
	DocumentationURL string `json:"documentation_url"`
	Status           string `json:"status"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ghError{Message: msg, DocumentationURL: docsURL, Status: strconv.Itoa(status)})
}

// apiError is an answer decided by a handler.
type apiError struct {
	status     int
	msg        string
	retryAfter string
	location   string
}

func (e *apiError) Error() string { return e.msg }

func errf(status int, format string, a ...any) error {
	return &apiError{status: status, msg: fmt.Sprintf(format, a...)}
}

var (
	errNotFound = &apiError{status: http.StatusNotFound, msg: "Not Found"}
	errAuth     = &apiError{status: http.StatusUnauthorized, msg: "Requires authentication"}
)

// ---- request plumbing ----

// call is one request.
type call struct {
	s   *Server
	w   http.ResponseWriter
	r   *http.Request
	ctx context.Context
	who auth.Principal
	ids *ids
	// owner and repo are the path parameters of /repos/{owner}/{repo}.
	owner, repo string
	repos       map[uuid.UUID]*ghRepo
}

func (s *Server) h(fn func(c *call) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		who, _ := auth.PrincipalFrom(r.Context())
		c := &call{s: s, w: w, r: r, ctx: r.Context(), who: who, owner: chi.URLParam(r, "owner"),
			repo: chi.URLParam(r, "repo"), repos: map[uuid.UUID]*ghRepo{}}
		c.ids = newIDs(r.Context(), s.q)
		if err := fn(c); err != nil {
			c.fail(err)
		}
	}
}

func (c *call) client() identity.Client {
	ri := auth.RequestInfoFrom(c.ctx)
	return identity.Client{IP: ri.IP, UserAgent: ri.UserAgent}
}

// read checks the token scope of a read of repository data.
func (c *call) read() error { return c.scope(auth.ScopeReadRepo) }

// write checks that the caller is signed in with the repo:write scope.
func (c *call) write() error {
	if c.who.UserID == uuid.Nil {
		return errAuth
	}
	return c.scope(auth.ScopeWriteRepo)
}

func (c *call) scope(s string) error {
	if c.who.Method == auth.MethodToken && !c.who.HasScope(s) {
		return errf(http.StatusForbidden, "Resource not accessible by personal access token (the %s scope is required)", s)
	}
	return nil
}

// fail answers err.
func (c *call) fail(err error) {
	var ae *apiError
	if !errors.As(err, &ae) {
		var moved *repos.MovedError
		if errors.As(err, &moved) {
			c.moved(moved)
			return
		}
		ae = classify(err)
	}
	if ae == nil {
		c.s.d.Log.ErrorContext(c.ctx, "ghapi: request failed", "method", c.r.Method, "path", c.r.URL.Path,
			"request_id", middleware.GetReqID(c.ctx), "error", err)
		writeError(c.w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if ae.retryAfter != "" {
		c.w.Header().Set("Retry-After", ae.retryAfter)
	}
	if ae.location != "" {
		c.w.Header().Set("Location", ae.location)
	}
	writeError(c.w, ae.status, ae.msg)
}

// moved answers a renamed or transferred repository like GitHub: 301 to
// the same path under the new name.
func (c *call) moved(m *repos.MovedError) {
	old := "/repos/" + c.owner + "/" + c.repo
	rest := strings.TrimPrefix(strings.TrimPrefix(c.r.URL.Path, Prefix), old)
	loc := c.s.api + "/repos/" + url.PathEscape(m.Owner) + "/" + url.PathEscape(m.Name) + rest
	if c.r.URL.RawQuery != "" {
		loc += "?" + c.r.URL.RawQuery
	}
	c.w.Header().Set("Location", loc)
	writeJSON(c.w, http.StatusMovedPermanently, map[string]string{"message": "Moved Permanently", "url": loc,
		"documentation_url": docsURL})
}

// ok writes v, unless resolving the numeric ids of v failed.
func (c *call) ok(status int, v any) error {
	if c.ids.err != nil {
		return c.ids.err
	}
	writeJSON(c.w, status, v)
	return nil
}

func (c *call) noContent() error {
	c.w.WriteHeader(http.StatusNoContent)
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// maxBody bounds a JSON request body (file contents are base64 inside it).
const maxBody = 64 << 20

// decode reads the JSON body into v. An empty body leaves v unchanged.
func (c *call) decode(v any) error {
	body := http.MaxBytesReader(c.w, c.r.Body, maxBody)
	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errf(http.StatusRequestEntityTooLarge, "Request body is too large")
		}
		return errf(http.StatusBadRequest, "Problems parsing JSON")
	}
	return nil
}

func (c *call) query(key string) string { return c.r.URL.Query().Get(key) }

// number parses the {number} path parameter.
func (c *call) number() (int64, error) {
	n, err := strconv.ParseInt(chi.URLParam(c.r, "number"), 10, 64)
	if err != nil || n <= 0 {
		return 0, errNotFound
	}
	return n, nil
}

// idParam resolves the numeric {id} path parameter to its UUID.
func (c *call) idParam() (uuid.UUID, error) { return c.ids.parse(chi.URLParam(c.r, "id")) }

// wildcard returns the * path parameter, unescaped.
func (c *call) wildcard() string {
	p := chi.URLParam(c.r, "*")
	if u, err := url.PathUnescape(p); err == nil {
		p = u
	}
	return strings.Trim(p, "/")
}

// ---- pagination ----

const (
	defaultPerPage = 30
	maxPerPage     = 100
)

// pager is the page/per_page pagination of a list.
type pager struct{ page, per int }

func (c *call) pager() pager {
	p := pager{page: 1, per: defaultPerPage}
	if n, err := strconv.Atoi(c.query("page")); err == nil && n > 0 {
		p.page = n
	}
	if n, err := strconv.Atoi(c.query("per_page")); err == nil && n > 0 {
		p.per = min(n, maxPerPage)
	}
	return p
}

func (p pager) offset() int { return (p.page - 1) * p.per }

// limit is how many items to ask a service for: one more than a page,
// to learn whether a next page exists, within the service limit of 100.
func (p pager) limit() int { return min(p.per+1, maxPerPage) }

// cut trims a list fetched with limit to the page and reports whether
// more items follow. At 100 per page a full page counts as "more".
func cut[T any](p pager, list []T) ([]T, bool) {
	if len(list) > p.per {
		return list[:p.per], true
	}
	return list, p.per == maxPerPage && len(list) == maxPerPage
}

// slice pages a list held in memory.
func slice[T any](p pager, list []T) ([]T, bool) {
	start := min(p.offset(), len(list))
	end := min(start+p.per, len(list))
	return list[start:end], end < len(list)
}

// link sets the Link header of a page.
func (c *call) link(p pager, more bool) {
	var parts []string
	add := func(page int, rel string) {
		q := c.r.URL.Query()
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(p.per))
		u := c.s.api + strings.TrimPrefix(c.r.URL.Path, Prefix) + "?" + q.Encode()
		parts = append(parts, "<"+u+`>; rel="`+rel+`"`)
	}
	if more {
		add(p.page+1, "next")
	}
	if p.page > 1 {
		add(p.page-1, "prev")
		add(1, "first")
	}
	if len(parts) > 0 {
		c.w.Header().Set("Link", strings.Join(parts, ", "))
	}
}

// ---- formatting ----

// ts formats a time as GitHub does.
func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func tsp(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	s := ts(*t)
	return &s
}

func esc(s string) string { return url.PathEscape(s) }

// escPath escapes each segment of a slash separated path.
func escPath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func (s *Server) repoAPI(owner, name string) string {
	return s.api + "/repos/" + esc(owner) + "/" + esc(name)
}

func (s *Server) repoHTML(owner, name string) string {
	return s.base + "/" + esc(owner) + "/" + esc(name)
}

func (c *call) repoAPI() string  { return c.s.repoAPI(c.owner, c.repo) }
func (c *call) repoHTML() string { return c.s.repoHTML(c.owner, c.repo) }
