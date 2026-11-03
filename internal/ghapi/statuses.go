package ghapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Commit statuses and checks are not stored by ErvisioRepo yet: the read
// endpoints answer empty results so that clients waiting on CI see "no
// checks", and the write endpoints answer 501.

func (c *call) combinedStatus() error {
	if err := c.read(); err != nil {
		return err
	}
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	ref := chi.URLParam(c.r, "ref")
	res, err := c.s.d.Browse.Resolve(c.ctx, browseRepo(r), ref)
	if err != nil {
		return err
	}
	sha := res.Commit
	self := c.repoAPI() + "/commits/" + esc(ref) + "/status"
	return c.ok(http.StatusOK, map[string]any{
		"state": "pending", "statuses": []any{}, "sha": sha, "total_count": 0,
		"repository": c.repoObj(r), "commit_url": c.repoAPI() + "/commits/" + sha, "url": self,
	})
}

func (c *call) listStatuses() error {
	if err := c.read(); err != nil {
		return err
	}
	if _, err := c.loadRepo(); err != nil {
		return err
	}
	return c.ok(http.StatusOK, []any{})
}

func (c *call) createStatus() error {
	if err := c.write(); err != nil {
		return err
	}
	return errf(http.StatusNotImplemented, "Commit statuses are not supported by this server")
}

func (c *call) listCheckRuns() error {
	if err := c.read(); err != nil {
		return err
	}
	if _, err := c.loadRepo(); err != nil {
		return err
	}
	return c.ok(http.StatusOK, map[string]any{"total_count": 0, "check_runs": []any{}})
}

func (c *call) listCheckSuites() error {
	if err := c.read(); err != nil {
		return err
	}
	if _, err := c.loadRepo(); err != nil {
		return err
	}
	return c.ok(http.StatusOK, map[string]any{"total_count": 0, "check_suites": []any{}})
}

func (c *call) unsupportedChecks() error {
	if err := c.write(); err != nil {
		return err
	}
	return errf(http.StatusNotImplemented, "Checks are not supported by this server")
}
