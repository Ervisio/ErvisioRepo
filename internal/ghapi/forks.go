package ghapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/pulls"
	"github.com/Ervisio/ErvisioRepo/internal/repoinit"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// forkFact is the fork flag and fork count of a repository object.
type forkFact struct {
	IsFork bool
	Forks  int64
}

// prefetchForks loads the fork facts of repositories in one round trip.
// A failure leaves them unknown (not a fork, no forks): the facts are
// decoration, not worth failing a listing for.
func (c *call) prefetchForks(ids ...uuid.UUID) {
	if c.forks == nil {
		c.forks = map[uuid.UUID]forkFact{}
	}
	var missing []uuid.UUID
	for _, id := range ids {
		if _, ok := c.forks[id]; !ok && id != uuid.Nil {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return
	}
	rows, err := c.s.q.ListGitHubAPIRepoForks(c.ctx, missing)
	if err != nil {
		c.s.d.Log.WarnContext(c.ctx, "ghapi: load fork facts", "error", err)
	}
	for _, id := range missing {
		c.forks[id] = forkFact{}
	}
	for _, r := range rows {
		c.forks[r.ID] = forkFact{IsFork: r.IsFork, Forks: r.Forks}
	}
}

func (c *call) forkFacts(id uuid.UUID) forkFact {
	c.prefetchForks(id)
	return c.forks[id]
}

// forkBody is the body of POST /repos/{owner}/{repo}/forks.
// default_branch_only is accepted and ignored: a fork copies every branch.
type forkBody struct {
	Organization string `json:"organization"`
	Name         string `json:"name"`
}

func (c *call) createFork() error {
	if err := c.write(); err != nil {
		return err
	}
	var b forkBody
	if err := c.decode(&b); err != nil {
		return err
	}
	r, err := c.s.d.Pulls.CreateFork(c.ctx, c.who, c.owner, c.repo, pulls.ForkInput{Owner: b.Organization, Name: b.Name},
		c.client())
	if errors.Is(err, repos.ErrNameTaken) {
		// Like GitHub, forking again answers the existing fork.
		if existing, ok := c.existingFork(b); ok {
			r, err = existing, nil
		}
	}
	if err != nil {
		return err
	}
	c.forks = nil // the parent gained a fork
	g := c.repoObj(r)
	if info, err := c.s.d.Pulls.ForkInfo(c.ctx, c.who, r.ID); err == nil {
		if info.Parent != nil {
			g.Parent = c.repoObj(*info.Parent)
		}
		if info.Source != nil {
			g.Source = c.repoObj(*info.Source)
		}
	}
	// GitHub answers 202: the copy is asynchronous there, synchronous here.
	return c.ok(http.StatusAccepted, g)
}

// existingFork returns the fork of the current repository that a fork
// request with b would have created, if it exists.
func (c *call) existingFork(b forkBody) (repos.Repo, bool) {
	owner, name := b.Organization, b.Name
	if owner == "" {
		owner = c.who.Username
	}
	parent, err := c.s.d.Repos.Get(c.ctx, c.who, c.owner, c.repo)
	if err != nil {
		return repos.Repo{}, false
	}
	if name == "" {
		name = parent.Name
	}
	r, err := c.s.d.Repos.Get(c.ctx, c.who, owner, name)
	if err != nil {
		return repos.Repo{}, false
	}
	info, err := c.s.d.Pulls.ForkInfo(c.ctx, c.who, r.ID)
	if err != nil || info.Parent == nil || info.Parent.ID != parent.ID {
		return repos.Repo{}, false
	}
	return r, true
}

func (c *call) listForks() error {
	if err := c.read(); err != nil {
		return err
	}
	p := c.pager()
	list, err := c.s.d.Pulls.ListForks(c.ctx, c.who, c.owner, c.repo, p.offset(), p.limit())
	if err != nil {
		return err
	}
	return c.repoList(list, p)
}

func (c *call) mergeUpstream() error {
	if err := c.write(); err != nil {
		return err
	}
	var b struct {
		Branch string `json:"branch"`
	}
	if err := c.decode(&b); err != nil {
		return err
	}
	res, err := c.s.d.Pulls.MergeUpstream(c.ctx, c.who, c.owner, c.repo, b.Branch)
	if err != nil {
		return err
	}
	msg := "This branch is not behind the upstream " + res.Base + "."
	if res.Type == "fast-forward" {
		msg = "Successfully fetched and fast-forwarded from upstream " + res.Base + "."
	}
	return c.ok(http.StatusOK, map[string]string{"message": msg, "merge_type": res.Type, "base_branch": res.Base})
}

// ---- gitignore and license templates ----

func (c *call) listGitignores() error { return c.ok(http.StatusOK, repoinit.Gitignores()) }

func (c *call) getGitignore() error {
	name, body, ok := repoinit.Gitignore(chi.URLParam(c.r, "name"))
	if !ok {
		return errNotFound
	}
	if strings.Contains(c.r.Header.Get("Accept"), "raw") {
		c.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = c.w.Write([]byte(body))
		return nil
	}
	return c.ok(http.StatusOK, map[string]string{"name": name, "source": body})
}

type ghLicense struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	SPDXID string `json:"spdx_id"`
	URL    string `json:"url"`
	NodeID string `json:"node_id"`
}

type ghFullLicense struct {
	ghLicense
	HTMLURL        string   `json:"html_url"`
	Description    string   `json:"description"`
	Implementation string   `json:"implementation"`
	Permissions    []string `json:"permissions"`
	Conditions     []string `json:"conditions"`
	Limitations    []string `json:"limitations"`
	Body           string   `json:"body"`
	Featured       bool     `json:"featured"`
}

func (c *call) licenseObj(l repoinit.License) ghLicense {
	return ghLicense{Key: l.Key, Name: l.Name, SPDXID: l.SPDXID, URL: c.s.api + "/licenses/" + esc(l.Key),
		NodeID: nodeID("License", uuid.NewSHA1(uuid.NameSpaceURL, []byte("license:"+l.Key)))}
}

func (c *call) listLicenses() error {
	ls := repoinit.Licenses()
	if c.query("featured") == "true" {
		var f []repoinit.License
		for _, l := range ls {
			if l.Featured {
				f = append(f, l)
			}
		}
		ls = f
	}
	out := make([]ghLicense, 0, len(ls))
	for _, l := range ls {
		out = append(out, c.licenseObj(l))
	}
	return c.ok(http.StatusOK, out)
}

func (c *call) getLicense() error {
	l, ok := repoinit.LicenseByKey(chi.URLParam(c.r, "license"))
	if !ok {
		return errNotFound
	}
	return c.ok(http.StatusOK, ghFullLicense{ghLicense: c.licenseObj(l),
		HTMLURL:     "https://choosealicense.com/licenses/" + l.Key + "/",
		Description: l.Description, Implementation: l.Implementation, Permissions: l.Permissions,
		Conditions: l.Conditions, Limitations: l.Limitations, Body: l.Body(), Featured: l.Featured})
}
