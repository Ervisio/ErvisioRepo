package ghapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// starMedia reports whether the client asked for the star media type
// (application/vnd.github.star+json), which adds starred_at to the lists.
func (c *call) starMedia() bool { return strings.Contains(c.r.Header.Get("Accept"), "star+json") }

func (c *call) checkStarred() error {
	if c.who.UserID == uuid.Nil {
		return errAuth
	}
	if err := c.read(); err != nil {
		return err
	}
	ok, err := c.s.d.Repos.IsStarred(c.ctx, c.who, c.owner, c.repo)
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound
	}
	return c.noContent()
}

func (c *call) starRepo() error {
	if err := c.write(); err != nil {
		return err
	}
	if err := c.s.d.Repos.Star(c.ctx, c.who, c.owner, c.repo); err != nil {
		return err
	}
	return c.noContent()
}

func (c *call) unstarRepo() error {
	if err := c.write(); err != nil {
		return err
	}
	if err := c.s.d.Repos.Unstar(c.ctx, c.who, c.owner, c.repo); err != nil {
		return err
	}
	return c.noContent()
}

func (c *call) listStargazers() error {
	if err := c.read(); err != nil {
		return err
	}
	p := c.pager()
	list, err := c.s.d.Repos.ListStargazers(c.ctx, c.who, c.owner, c.repo, p.offset(), p.limit())
	if err != nil {
		return err
	}
	page, more := cut(p, list)
	names := make([]string, len(page))
	for i, g := range page {
		names[i] = g.Username
	}
	c.ids.prefetchUsers(names...)
	users := make([]*ghUser, len(page))
	for i, g := range page {
		users[i] = c.userByName(g.Username)
		users[i].AvatarURL = g.AvatarURL
	}
	c.link(p, more)
	if !c.starMedia() {
		return c.ok(http.StatusOK, users)
	}
	type starredBy struct {
		StarredAt string  `json:"starred_at"`
		User      *ghUser `json:"user"`
	}
	out := make([]starredBy, len(page))
	for i, g := range page {
		out[i] = starredBy{StarredAt: ts(g.StarredAt), User: users[i]}
	}
	return c.ok(http.StatusOK, out)
}

func (c *call) myStarred() error {
	if c.who.UserID == uuid.Nil {
		return errAuth
	}
	return c.starredOf(c.who.Username)
}

func (c *call) userStarred() error { return c.starredOf(chi.URLParam(c.r, "username")) }

// starredOf answers the repositories starred by username.
func (c *call) starredOf(username string) error {
	if err := c.read(); err != nil {
		return err
	}
	p := c.pager()
	list, err := c.s.d.Repos.ListStarred(c.ctx, c.who, username, p.offset(), p.limit())
	if err != nil {
		return err
	}
	page, more := cut(p, list)
	us := make([]uuid.UUID, 0, 2*len(page))
	for _, r := range page {
		us = append(us, r.ID, r.OwnerID)
	}
	c.ids.prefetch(us...)
	c.link(p, more)
	if !c.starMedia() {
		out := make([]*ghRepo, len(page))
		for i, r := range page {
			out[i] = c.repoObj(r.Repo)
		}
		return c.ok(http.StatusOK, out)
	}
	type starredRepo struct {
		StarredAt string  `json:"starred_at"`
		Repo      *ghRepo `json:"repo"`
	}
	out := make([]starredRepo, len(page))
	for i, r := range page {
		out[i] = starredRepo{StarredAt: ts(r.StarredAt), Repo: c.repoObj(r.Repo)}
	}
	return c.ok(http.StatusOK, out)
}

var _ Repos = (*repos.Service)(nil)
