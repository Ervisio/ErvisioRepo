package ghapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
)

// ---- readme ----

// getReadme serves readme and readme/{dir}: the README of the directory
// (the one the code browser shows), like a contents file.
func (c *call) getReadme() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	ref, dir := c.query("ref"), c.wildcard()
	repo := browseRepo(r)
	t, err := c.s.d.Browse.Tree(c.ctx, repo, ref, dir, false)
	if err != nil {
		if errors.Is(err, gitbrowse.ErrInvalid) && dir != "" {
			return errNotFound // a file, not a directory
		}
		return err
	}
	if t.Readme == "" {
		return errNotFound
	}
	// Read the commit the tree was listed at; the URLs keep the given ref.
	if strings.Contains(c.r.Header.Get("Accept"), ".raw") {
		return c.rawContents(repo, t.Ref.Commit, t.Readme)
	}
	b, err := c.s.d.Browse.Blob(c.ctx, repo, t.Ref.Commit, t.Readme)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.fileContent(ref, b))
}

// ---- git refs writes ----

type refBody struct {
	Ref   string `json:"ref"`
	SHA   string `json:"sha"`
	Force bool   `json:"force"`
}

// refErr maps the errors of a ref change to the GitHub answers (422).
func refErr(err error) error {
	for _, m := range []struct {
		target error
		msg    string
	}{
		{gitbrowse.ErrRefExists, "Reference already exists"},
		{gitbrowse.ErrRefMissing, "Reference does not exist"},
		{gitbrowse.ErrObjectMissing, "Object does not exist"},
		{gitbrowse.ErrNotFastForward, "Update is not a fast forward"},
	} {
		if errors.Is(err, m.target) {
			return errf(http.StatusUnprocessableEntity, "%s", m.msg)
		}
	}
	if errors.Is(err, gitbrowse.ErrRejected) {
		return errf(http.StatusUnprocessableEntity, "Protected ref update failed: %s",
			strings.TrimPrefix(err.Error(), gitbrowse.ErrRejected.Error()+": "))
	}
	return err
}

// changeRef applies a ref change as the caller, with the push permission.
func (c *call) changeRef(ch gitbrowse.RefChange) (gitbrowse.RefChangeResult, error) {
	r, err := c.pushRepo()
	if err != nil {
		return gitbrowse.RefChangeResult{}, err
	}
	ch.ActorID = c.who.UserID
	res, err := c.s.d.Browse.UpdateRef(c.ctx, browseRepo(r), ch)
	return res, refErr(err)
}

// createRef serves POST git/refs.
func (c *call) createRef() error {
	var b refBody
	if err := c.decode(&b); err != nil {
		return err
	}
	if b.Ref == "" || b.SHA == "" {
		return errf(http.StatusUnprocessableEntity, "Invalid request.\n\n\"ref\" and \"sha\" are required.")
	}
	if !strings.HasPrefix(b.Ref, "refs/") || strings.Count(b.Ref, "/") < 2 {
		return errf(http.StatusUnprocessableEntity, "Reference name must start with 'refs/' and have at least two slashes.")
	}
	res, err := c.changeRef(gitbrowse.RefChange{Op: gitbrowse.RefCreate, Ref: b.Ref, SHA: b.SHA})
	if err != nil {
		return err
	}
	out := c.refObj(res.Ref, res.New)
	c.w.Header().Set("Location", out.URL)
	return c.ok(http.StatusCreated, out)
}

// updateRef serves PATCH git/refs/{ref}: a fast-forward unless force.
func (c *call) updateRef() error {
	var b refBody
	if err := c.decode(&b); err != nil {
		return err
	}
	if b.SHA == "" {
		return errf(http.StatusUnprocessableEntity, "Invalid request.\n\n\"sha\" wasn't supplied.")
	}
	res, err := c.changeRef(gitbrowse.RefChange{Op: gitbrowse.RefUpdate, Ref: "refs/" + c.wildcard(),
		SHA: b.SHA, Force: b.Force})
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.refObj(res.Ref, res.New))
}

// deleteRef serves DELETE git/refs/{ref}.
func (c *call) deleteRef() error {
	if _, err := c.changeRef(gitbrowse.RefChange{Op: gitbrowse.RefDelete, Ref: "refs/" + c.wildcard()}); err != nil {
		return err
	}
	return c.noContent()
}

// ---- commits/{ref}[/<sub-resource>] ----

// commitSubs are the sub-resources of commits/{ref}. A ref may contain
// '/', so the route is a wildcard and a known last segment selects the
// sub-resource (as on GitHub, a ref ending in one of them is ambiguous and
// the sub-resource wins).
var commitSubs = map[string]func(c *call) error{
	"status":       (*call).combinedStatus,
	"statuses":     (*call).listStatuses,
	"check-runs":   (*call).listCheckRuns,
	"check-suites": (*call).listCheckSuites,
}

// commitRoute dispatches GET commits/*.
func (c *call) commitRoute() error {
	rest := c.wildcard()
	fn := (*call).getCommit
	if i := strings.LastIndexByte(rest, '/'); i > 0 {
		if sub, ok := commitSubs[rest[i+1:]]; ok {
			fn, rest = sub, rest[:i]
		}
	}
	return c.withRef(rest, fn)
}

// refRoute serves a {ref} handler under a wildcard route (statuses/*), so
// that the ref may contain '/'.
func refRoute(fn func(c *call) error) func(c *call) error {
	return func(c *call) error { return c.withRef(c.wildcard(), fn) }
}

// withRef runs fn with the {ref} path parameter set to ref.
func (c *call) withRef(ref string, fn func(c *call) error) error {
	if ref == "" {
		return errNotFound
	}
	if rctx := chi.RouteContext(c.r.Context()); rctx != nil {
		rctx.URLParams.Add("ref", ref)
	}
	return fn(c)
}
