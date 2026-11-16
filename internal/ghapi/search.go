package ghapi

import (
	"net/http"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
)

// The search endpoints answer GitHub's search envelope. Issue and
// repository searches share the qualifier parsing of the GraphQL search
// field (internal/ghapi/graphql); issue searches need a repo: qualifier.

// searchPage is the answer of a search endpoint.
type searchPage struct {
	TotalCount        int  `json:"total_count"`
	IncompleteResults bool `json:"incomplete_results"`
	Items             any  `json:"items"`
}

// searchQuery returns the q parameter, required as on GitHub.
func (c *call) searchQuery() (string, error) {
	q := strings.TrimSpace(c.query("q"))
	if q == "" {
		return "", errf(http.StatusUnprocessableEntity, "Validation Failed: q is missing")
	}
	return q, nil
}

func (c *call) searchRepositories() error {
	if err := c.read(); err != nil {
		return err
	}
	q, err := c.searchQuery()
	if err != nil {
		return err
	}
	if c.s.gql == nil {
		return errf(http.StatusNotImplemented, "Search is not available on this server")
	}
	p := c.pager()
	hits, total, more, err := c.s.gql.Search(c.ctx, c.who, "REPOSITORY", q, p.offset(), p.per)
	if err != nil {
		return err
	}
	us := make([]uuid.UUID, 0, 2*len(hits))
	rs := make([]uuid.UUID, 0, len(hits))
	for _, h := range hits {
		us = append(us, h.Repo.ID, h.Repo.OwnerID)
		rs = append(rs, h.Repo.ID)
	}
	c.ids.prefetch(us...)
	c.prefetchForks(rs...)
	type item struct {
		*ghRepo
		Score float64 `json:"score"`
	}
	items := make([]item, 0, len(hits))
	for _, h := range hits {
		items = append(items, item{ghRepo: c.repoObj(h.Repo), Score: 1})
	}
	c.link(p, more)
	return c.ok(http.StatusOK, searchPage{TotalCount: total, Items: items})
}

func (c *call) searchIssues() error {
	if err := c.read(); err != nil {
		return err
	}
	q, err := c.searchQuery()
	if err != nil {
		return err
	}
	if c.s.gql == nil {
		return errf(http.StatusNotImplemented, "Search is not available on this server")
	}
	p := c.pager()
	hits, total, more, err := c.s.gql.Search(c.ctx, c.who, "ISSUE", q, p.offset(), p.per)
	if err != nil {
		return err
	}
	list := make([]issues.Issue, 0, len(hits))
	for _, h := range hits {
		switch {
		case h.Issue != nil:
			list = append(list, *h.Issue)
		case h.Pull != nil:
			i := h.Pull.Issue
			i.Kind = issues.KindPull
			list = append(list, i)
		}
	}
	c.prefetchIssues(list)
	type item struct {
		ghIssue
		Score float64 `json:"score"`
	}
	items := make([]item, 0, len(hits))
	j := 0
	for _, h := range hits {
		if h.Issue == nil && h.Pull == nil {
			continue
		}
		// The issue objects build their URLs from the repository of the call.
		c.owner, c.repo = h.Repo.Owner, h.Repo.Name
		items = append(items, item{ghIssue: c.issueObj(list[j]), Score: 1})
		j++
	}
	c.link(p, more)
	return c.ok(http.StatusOK, searchPage{TotalCount: total, Items: items})
}

// codeSearch is a parsed code search: text with repo:, path: and in:
// qualifiers.
type codeSearch struct {
	owner, name string
	path        string
	text        []string
}

func parseCodeSearch(q string) codeSearch {
	var s codeSearch
	for _, w := range strings.Fields(q) {
		k, v, ok := strings.Cut(w, ":")
		switch {
		case ok && strings.EqualFold(k, "repo"):
			s.owner, s.name, _ = strings.Cut(v, "/")
		case ok && strings.EqualFold(k, "path"):
			s.path = strings.Trim(v, "/")
		case ok && (strings.EqualFold(k, "in") || strings.EqualFold(k, "language") ||
			strings.EqualFold(k, "extension") || strings.EqualFold(k, "filename")):
			// Not supported: ignored rather than searched as text.
		default:
			s.text = append(s.text, strings.Trim(w, `"`))
		}
	}
	return s
}

type ghCodeResult struct {
	Name       string  `json:"name"`
	Path       string  `json:"path"`
	SHA        string  `json:"sha"`
	URL        string  `json:"url"`
	GitURL     string  `json:"git_url"`
	HTMLURL    string  `json:"html_url"`
	Repository *ghRepo `json:"repository"`
	Score      float64 `json:"score"`
}

// searchCode searches the default branch of one repository (repo:
// qualifier required) with git grep: one item per matching file.
func (c *call) searchCode() error {
	if err := c.read(); err != nil {
		return err
	}
	q, err := c.searchQuery()
	if err != nil {
		return err
	}
	s := parseCodeSearch(q)
	if s.owner == "" || s.name == "" {
		return errf(http.StatusUnprocessableEntity, "Validation Failed: code search needs a repo:owner/name qualifier")
	}
	if len(s.text) == 0 {
		return errf(http.StatusUnprocessableEntity, "Validation Failed: the query has no search text")
	}
	r, err := c.s.d.Repos.Get(c.ctx, c.who, s.owner, s.name)
	if err != nil {
		return err
	}
	res, err := c.s.d.Browse.Grep(c.ctx, browseRepo(r), "", strings.Join(s.text, " "),
		gitbrowse.GrepOptions{IgnoreCase: true, Path: s.path})
	if err != nil {
		return err
	}
	var files []string
	seen := map[string]bool{}
	for _, m := range res.Matches {
		if !seen[m.Path] {
			seen[m.Path] = true
			files = append(files, m.Path)
		}
	}
	p := c.pager()
	page, more := slice(p, files)
	c.ids.prefetch(r.ID, r.OwnerID)
	repo := c.repoObj(r)
	api, html := c.s.repoAPI(r.Owner, r.Name), c.s.repoHTML(r.Owner, r.Name)
	items := make([]ghCodeResult, 0, len(page))
	for _, f := range page {
		items = append(items, ghCodeResult{Name: path.Base(f), Path: f, SHA: res.Ref.Commit,
			URL:    api + "/contents/" + escPath(f) + "?ref=" + res.Ref.Commit,
			GitURL: api + "/git/trees/" + res.Ref.Commit, HTMLURL: html + "/blob/" + res.Ref.Commit + "/" + escPath(f),
			Repository: repo, Score: 1})
	}
	c.link(p, more)
	return c.ok(http.StatusOK, searchPage{TotalCount: len(files), IncompleteResults: res.Truncated, Items: items})
}

// searchUsers finds users by the prefix of their name, within the user
// directory the caller may see. Qualifiers other than plain text are
// ignored.
func (c *call) searchUsers() error {
	if c.who.UserID == uuid.Nil {
		return errAuth
	}
	q, err := c.searchQuery()
	if err != nil {
		return err
	}
	var text []string
	for _, w := range strings.Fields(q) {
		if !strings.Contains(w, ":") {
			text = append(text, w)
		}
	}
	if len(text) == 0 {
		return c.ok(http.StatusOK, searchPage{Items: []any{}})
	}
	list, err := c.s.d.Identity.SearchUsers(c.ctx, c.who, strings.Join(text, " "), uuid.Nil, 50)
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, len(list))
	for i, u := range list {
		ids[i] = u.ID
	}
	c.ids.prefetch(ids...)
	p := c.pager()
	page, more := slice(p, list)
	type item struct {
		*ghUser
		Score float64 `json:"score"`
	}
	items := make([]item, 0, len(page))
	for _, u := range page {
		obj := c.userObj(u.ID, "user", u.Username)
		obj.AvatarURL = u.AvatarURL
		items = append(items, item{ghUser: obj, Score: 1})
	}
	c.link(p, more)
	return c.ok(http.StatusOK, searchPage{TotalCount: len(list), Items: items})
}
