package ghapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/releases"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// ---- objects ----

type ghAsset struct {
	URL                string  `json:"url"`
	BrowserDownloadURL string  `json:"browser_download_url"`
	ID                 int64   `json:"id"`
	NodeID             string  `json:"node_id"`
	Name               string  `json:"name"`
	Label              *string `json:"label"`
	State              string  `json:"state"`
	ContentType        string  `json:"content_type"`
	Size               int64   `json:"size"`
	Digest             string  `json:"digest"`
	DownloadCount      int64   `json:"download_count"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
	Uploader           *ghUser `json:"uploader"`
}

type ghRelease struct {
	URL             string    `json:"url"`
	HTMLURL         string    `json:"html_url"`
	AssetsURL       string    `json:"assets_url"`
	UploadURL       string    `json:"upload_url"`
	TarballURL      *string   `json:"tarball_url"`
	ZipballURL      *string   `json:"zipball_url"`
	ID              int64     `json:"id"`
	NodeID          string    `json:"node_id"`
	TagName         string    `json:"tag_name"`
	TargetCommitish string    `json:"target_commitish"`
	Name            *string   `json:"name"`
	Body            *string   `json:"body"`
	Draft           bool      `json:"draft"`
	Prerelease      bool      `json:"prerelease"`
	CreatedAt       string    `json:"created_at"`
	PublishedAt     *string   `json:"published_at"`
	Author          *ghUser   `json:"author"`
	Assets          []ghAsset `json:"assets"`
}

// uploader answers the user object of an asset uploader, by id.
func (c *call) uploader(id uuid.UUID, cache map[uuid.UUID]*ghUser) *ghUser {
	if id == uuid.Nil {
		return nil
	}
	if u, ok := cache[id]; ok {
		return u
	}
	var out *ghUser
	if u, err := c.s.d.Identity.User(c.ctx, id); err == nil {
		out = c.userObj(id, "user", u.Username)
	}
	cache[id] = out
	return out
}

func (c *call) assetObj(rel releases.Release, a releases.Asset, cache map[uuid.UUID]*ghUser) ghAsset {
	id := c.ids.of(a.ID)
	out := ghAsset{
		URL: c.repoAPI() + "/releases/assets/" + strconv.FormatInt(id, 10),
		BrowserDownloadURL: c.s.base + "/api/v1/repos/" + esc(c.owner) + "/" + esc(c.repo) + "/releases/" +
			rel.ID.String() + "/assets/" + a.ID.String() + "/download",
		ID: id, NodeID: nodeID("ReleaseAsset", a.ID), Name: a.Name, Label: strp(a.Label),
		State: "uploaded", ContentType: a.ContentType, Size: a.Size, DownloadCount: a.DownloadCount,
		CreatedAt: ts(a.CreatedAt), UpdatedAt: ts(a.CreatedAt), Uploader: c.uploader(a.Uploader, cache),
	}
	if a.SHA256 != "" {
		out.Digest = "sha256:" + a.SHA256
	}
	return out
}

func (c *call) releaseObj(r releases.Release, cache map[uuid.UUID]*ghUser) ghRelease {
	id := c.ids.of(r.ID)
	sid := strconv.FormatInt(id, 10)
	self := c.repoAPI() + "/releases/" + sid
	out := ghRelease{
		URL: self, HTMLURL: c.repoHTML() + "/releases/" + r.ID.String(), AssetsURL: self + "/assets",
		UploadURL: c.s.base + uploadPrefix + "/repos/" + esc(c.owner) + "/" + esc(c.repo) + "/releases/" + sid +
			"/assets{?name,label}",
		ID: id, NodeID: nodeID("Release", r.ID), TagName: r.Tag, TargetCommitish: r.Target,
		Name: strp(r.Name), Body: strp(r.Body), Draft: r.Draft, Prerelease: r.Prerelease,
		CreatedAt: ts(r.CreatedAt), PublishedAt: tsp(r.PublishedAt), Author: c.userByName(r.Author),
		Assets: make([]ghAsset, 0, len(r.Assets)),
	}
	if !r.Draft {
		tar := c.s.base + "/api/v1/repos/" + esc(c.owner) + "/" + esc(c.repo) + "/archive?ref=" +
			url.QueryEscape(r.Tag) + "&format=tar.gz"
		zip := c.s.base + "/api/v1/repos/" + esc(c.owner) + "/" + esc(c.repo) + "/archive?ref=" +
			url.QueryEscape(r.Tag) + "&format=zip"
		out.TarballURL, out.ZipballURL = &tar, &zip
	}
	if out.TargetCommitish == "" {
		out.TargetCommitish = r.Commit
	}
	for _, a := range r.Assets {
		out.Assets = append(out.Assets, c.assetObj(r, a, cache))
	}
	return out
}

func (c *call) prefetchReleases(list []releases.Release) {
	var us []uuid.UUID
	var names []string
	for _, r := range list {
		us = append(us, r.ID)
		names = append(names, r.Author)
		for _, a := range r.Assets {
			us = append(us, a.ID, a.Uploader)
		}
	}
	c.ids.prefetch(us...)
	c.ids.prefetchUsers(names...)
}

func (c *call) oneRelease(status int, r releases.Release) error {
	c.prefetchReleases([]releases.Release{r})
	out := c.releaseObj(r, map[uuid.UUID]*ghUser{})
	if status == http.StatusCreated {
		c.w.Header().Set("Location", out.URL)
	}
	return c.ok(status, out)
}

// ---- releases ----

func (c *call) listReleases() error {
	if err := c.read(); err != nil {
		return err
	}
	p := c.pager()
	list, err := c.s.d.Releases.List(c.ctx, c.who, c.owner, c.repo, "", p.offset(), p.limit())
	if err != nil {
		return err
	}
	list, more := cut(p, list)
	c.prefetchReleases(list)
	cache := map[uuid.UUID]*ghUser{}
	out := make([]ghRelease, len(list))
	for i, r := range list {
		out[i] = c.releaseObj(r, cache)
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

func (c *call) getRelease() error {
	if err := c.read(); err != nil {
		return err
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	r, err := c.s.d.Releases.Get(c.ctx, c.who, c.owner, c.repo, id)
	if err != nil {
		return err
	}
	return c.oneRelease(http.StatusOK, r)
}

func (c *call) latestRelease() error {
	if err := c.read(); err != nil {
		return err
	}
	r, err := c.s.d.Releases.Latest(c.ctx, c.who, c.owner, c.repo)
	if err != nil {
		return err
	}
	return c.oneRelease(http.StatusOK, r)
}

func (c *call) releaseByTag() error {
	if err := c.read(); err != nil {
		return err
	}
	tag := c.wildcard()
	if tag == "" {
		return errNotFound
	}
	list, err := c.s.d.Releases.List(c.ctx, c.who, c.owner, c.repo, tag, 0, 1)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return errNotFound
	}
	return c.oneRelease(http.StatusOK, list[0])
}

type releaseBody struct {
	TagName         *string `json:"tag_name"`
	TargetCommitish *string `json:"target_commitish"`
	Name            *string `json:"name"`
	Body            *string `json:"body"`
	Draft           *bool   `json:"draft"`
	Prerelease      *bool   `json:"prerelease"`
	MakeLatest      *string `json:"make_latest"`
	GenerateNotes   bool    `json:"generate_release_notes"`
	DiscussionCat   *string `json:"discussion_category_name"`
}

func (c *call) createRelease() error {
	if err := c.write(); err != nil {
		return err
	}
	var b releaseBody
	if err := c.decode(&b); err != nil {
		return err
	}
	if b.TagName == nil || *b.TagName == "" {
		return errf(http.StatusUnprocessableEntity, "Validation Failed: tag_name is required")
	}
	if b.GenerateNotes {
		return errf(http.StatusUnprocessableEntity, "generate_release_notes is not supported")
	}
	in := releases.CreateInput{Tag: *b.TagName, Name: deref(b.Name), Body: deref(b.Body),
		Target: deref(b.TargetCommitish), Draft: b.Draft != nil && *b.Draft, Prerelease: b.Prerelease != nil && *b.Prerelease}
	r, err := c.s.d.Releases.Create(c.ctx, c.who, c.owner, c.repo, in, c.client())
	if err != nil {
		return err
	}
	return c.oneRelease(http.StatusCreated, r)
}

func (c *call) updateRelease() error {
	if err := c.write(); err != nil {
		return err
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	var b releaseBody
	if err := c.decode(&b); err != nil {
		return err
	}
	in := releases.UpdateInput{Tag: b.TagName, Name: b.Name, Body: b.Body, Draft: b.Draft, Prerelease: b.Prerelease,
		Target: b.TargetCommitish}
	r, err := c.s.d.Releases.Update(c.ctx, c.who, c.owner, c.repo, id, in, c.client())
	if err != nil {
		return err
	}
	return c.oneRelease(http.StatusOK, r)
}

func (c *call) deleteRelease() error {
	if err := c.write(); err != nil {
		return err
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	if err := c.s.d.Releases.Delete(c.ctx, c.who, c.owner, c.repo, id, c.client()); err != nil {
		return err
	}
	return c.noContent()
}

// ---- assets ----

func (c *call) listAssets() error {
	if err := c.read(); err != nil {
		return err
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	r, err := c.s.d.Releases.Get(c.ctx, c.who, c.owner, c.repo, id)
	if err != nil {
		return err
	}
	p := c.pager()
	page, more := slice(p, r.Assets)
	c.prefetchReleases([]releases.Release{r})
	cache := map[uuid.UUID]*ghUser{}
	out := make([]ghAsset, len(page))
	for i, a := range page {
		out[i] = c.assetObj(r, a, cache)
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

// assetParam resolves {id} to the asset and its release: the asset URLs
// carry no release id.
func (c *call) assetParam() (releaseID, assetID uuid.UUID, err error) {
	assetID, err = c.idParam()
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	releaseID, err = c.s.q.GetGitHubAPIAssetRelease(c.ctx, assetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, errNotFound
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("load asset release: %w", err)
	}
	return releaseID, assetID, nil
}

// assetRelease answers the release of an asset with the asset itself.
func (c *call) assetRelease(releaseID, assetID uuid.UUID) (releases.Release, releases.Asset, error) {
	r, err := c.s.d.Releases.Get(c.ctx, c.who, c.owner, c.repo, releaseID)
	if err != nil {
		return releases.Release{}, releases.Asset{}, err
	}
	for _, a := range r.Assets {
		if a.ID == assetID {
			return r, a, nil
		}
	}
	return releases.Release{}, releases.Asset{}, errNotFound
}

func (c *call) oneAsset(status int, r releases.Release, a releases.Asset) error {
	c.prefetchReleases([]releases.Release{r})
	out := c.assetObj(r, a, map[uuid.UUID]*ghUser{})
	return c.ok(status, out)
}

type assetBody struct {
	Name  *string `json:"name"`
	Label *string `json:"label"`
	State *string `json:"state"`
}

func (c *call) updateAsset() error {
	if err := c.write(); err != nil {
		return err
	}
	relID, assetID, err := c.assetParam()
	if err != nil {
		return err
	}
	var b assetBody
	if err := c.decode(&b); err != nil {
		return err
	}
	a, err := c.s.d.Releases.UpdateAsset(c.ctx, c.who, c.owner, c.repo, relID, assetID, b.Name, b.Label, c.client())
	if err != nil {
		return err
	}
	r, err := c.s.d.Releases.Get(c.ctx, c.who, c.owner, c.repo, relID)
	if err != nil {
		return err
	}
	return c.oneAsset(http.StatusOK, r, a)
}

func (c *call) deleteAsset() error {
	if err := c.write(); err != nil {
		return err
	}
	relID, assetID, err := c.assetParam()
	if err != nil {
		return err
	}
	if err := c.s.d.Releases.DeleteAsset(c.ctx, c.who, c.owner, c.repo, relID, assetID, c.client()); err != nil {
		return err
	}
	return c.noContent()
}

// uploadAsset is POST {uploads}/repos/{o}/{r}/releases/{id}/assets?name=&label=
// with the raw file as body.
func (c *call) uploadAsset() error {
	if err := c.write(); err != nil {
		return err
	}
	relID, err := c.idParam()
	if err != nil {
		return err
	}
	name := c.query("name")
	if name == "" {
		return errf(http.StatusUnprocessableEntity, "Validation Failed: name is required")
	}
	if c.r.ContentLength < 0 {
		return errf(http.StatusLengthRequired, "Content-Length is required")
	}
	ctype := c.r.Header.Get("Content-Type")
	if mt, _, err := mime.ParseMediaType(ctype); err != nil || mt == "" {
		ctype = ""
	}
	a, err := c.s.d.Releases.UploadAsset(c.ctx, c.who, c.owner, c.repo, relID, releases.UploadInput{
		Name: name, Label: c.query("label"), ContentType: ctype, Size: c.r.ContentLength, Body: c.r.Body,
	}, c.client())
	if err != nil {
		return err
	}
	r, err := c.s.d.Releases.Get(c.ctx, c.who, c.owner, c.repo, relID)
	if err != nil {
		return err
	}
	return c.oneAsset(http.StatusCreated, r, a)
}

// getAsset answers the asset object, or its content when the client asks
// for application/octet-stream (gh release download does).
func (c *call) getAsset() error {
	if err := c.read(); err != nil {
		return err
	}
	relID, assetID, err := c.assetParam()
	if err != nil {
		return err
	}
	if !strings.Contains(c.r.Header.Get("Accept"), "application/octet-stream") {
		r, a, err := c.assetRelease(relID, assetID)
		if err != nil {
			return err
		}
		return c.oneAsset(http.StatusOK, r, a)
	}
	meta, err := c.s.d.Releases.GetAsset(c.ctx, c.who, c.owner, c.repo, relID, assetID)
	if err != nil {
		return err
	}
	var rng *blob.Range
	status := http.StatusOK
	if h := c.r.Header.Get("Range"); h != "" {
		r, ok, unsat := parseRange(h, meta.Size)
		if unsat {
			c.w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(meta.Size, 10))
			return errf(http.StatusRequestedRangeNotSatisfiable, "Requested Range Not Satisfiable")
		}
		if ok {
			rng, status = &r, http.StatusPartialContent
		}
	}
	rc, _, err := c.s.d.Releases.OpenAsset(c.ctx, c.who, c.owner, c.repo, relID, assetID, rng)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	h := c.w.Header()
	h.Set("Content-Type", meta.ContentType)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.Name}))
	h.Set("Accept-Ranges", "bytes")
	h.Set("ETag", `"`+meta.SHA256+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=0")
	length := meta.Size
	if rng != nil {
		length = rng.Length
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rng.Offset, rng.Offset+rng.Length-1, meta.Size))
	}
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	c.w.WriteHeader(status)
	if c.r.Method == http.MethodHead {
		return nil
	}
	if _, err := io.Copy(c.w, rc); err != nil {
		// The status is sent: the client sees a short body.
		c.s.d.Log.WarnContext(c.ctx, "ghapi: asset download interrupted", "error", err)
	}
	return nil
}

// parseRange reads a single "bytes=" range for an object of size bytes.
// ok is false when the header is absent or not a single range (the whole
// object is served); unsat is true when the range cannot be satisfied.
func parseRange(h string, size int64) (rng blob.Range, ok, unsat bool) {
	spec, found := strings.CutPrefix(h, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return blob.Range{}, false, false
	}
	a, b, found := strings.Cut(strings.TrimSpace(spec), "-")
	if !found {
		return blob.Range{}, false, false
	}
	if a == "" {
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return blob.Range{}, false, err == nil
		}
		n = min(n, size)
		return blob.Range{Offset: size - n, Length: n}, size > 0, size == 0
	}
	start, err := strconv.ParseInt(a, 10, 64)
	if err != nil || start < 0 {
		return blob.Range{}, false, false
	}
	if start >= size {
		return blob.Range{}, false, true
	}
	end := size - 1
	if b != "" {
		e, err := strconv.ParseInt(b, 10, 64)
		if err != nil || e < start {
			return blob.Range{}, false, false
		}
		end = min(e, size-1)
	}
	return blob.Range{Offset: start, Length: end - start + 1}, true, false
}
