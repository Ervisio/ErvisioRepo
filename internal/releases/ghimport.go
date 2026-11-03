package releases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/ghsync"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
)

// Import of the releases of a GitHub repository and their assets
// (ghsync.Importer). The git import runs first, so the tags exist; a
// release whose tag is missing (a draft) gets no commit. Assets are
// streamed from GitHub into the release storage class, charged to the
// quota of the repository owner. Releases and assets are recorded with
// their github_import_map rows, so a retry skips what is done.

type gitHubAsset struct {
	ID          int64              `json:"id"`
	Name        string             `json:"name"`
	Label       string             `json:"label"`
	ContentType string             `json:"content_type"`
	Size        int64              `json:"size"`
	Uploader    *ghsync.GitHubUser `json:"uploader"`
}

type gitHubRelease struct {
	ID          int64              `json:"id"`
	TagName     string             `json:"tag_name"`
	Target      string             `json:"target_commitish"`
	Name        *string            `json:"name"`
	Body        *string            `json:"body"`
	Draft       bool               `json:"draft"`
	Prerelease  bool               `json:"prerelease"`
	Author      *ghsync.GitHubUser `json:"author"`
	CreatedAt   time.Time          `json:"created_at"`
	PublishedAt *time.Time         `json:"published_at"`
	Assets      []gitHubAsset      `json:"assets"`
}

// GitHubImporter returns the importer of the releases of a GitHub
// repository.
func (s *Service) GitHubImporter() ghsync.Importer { return releaseImporter{s} }

type releaseImporter struct{ s *Service }

func (releaseImporter) Name() string { return ghsync.ImportReleases }

func (im releaseImporter) Import(ctx context.Context, ic ghsync.ImportContext) error {
	s := im.s
	repo, err := s.q.GetRepo(ctx, ic.RepoID)
	if err != nil {
		return fmt.Errorf("get repository: %w", err)
	}
	n, a := 0, 0
	err = ghsync.Each(ctx, ic.API, ic.Repo()+"/releases", func(gr gitHubRelease) error {
		rel, created, err := s.importRelease(ctx, ic, gr)
		if err != nil || rel.ID == uuid.Nil {
			return err
		}
		if created {
			n++
		}
		if len(gr.Assets) == 0 {
			return nil
		}
		path := ic.Repo() + "/releases/" + strconv.FormatInt(gr.ID, 10) + "/assets"
		return ghsync.Each(ctx, ic.API, path, func(ga gitHubAsset) error {
			ok, err := s.importAsset(ctx, ic, repo.Repo.OwnerID, rel, ga)
			if ok {
				a++
			}
			return err
		})
	})
	if err != nil {
		return fmt.Errorf("import releases: %w", err)
	}
	ic.Log.InfoContext(ctx, "github releases imported", "repo", ic.RepoID, "releases", n, "assets", a)
	return nil
}

// tagCommit returns the commit of refs/tags/<tag>, or "" when the tag does
// not exist.
func (s *Service) tagCommit(ctx context.Context, repoID uuid.UUID, tag string) (string, error) {
	if s.o.Git == nil {
		return "", nil
	}
	l, err := s.o.Git.Acquire(ctx, repoID, gitstore.Read)
	if err != nil {
		return "", fmt.Errorf("acquire repository: %w", err)
	}
	defer l.Release()
	out, err := git.Run(ctx, l.Path, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/tags/"+tag+"^{commit}")
	if isExit1(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve tag: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// importRelease creates the release gr, or maps it to the local release of
// its tag. The zero release means gr cannot be imported.
func (s *Service) importRelease(ctx context.Context, ic ghsync.ImportContext, gr gitHubRelease) (dbgen.Release, bool, error) {
	if id, ok, err := ic.Mapped(ctx, s.q, ghsync.MapRelease, gr.ID); err != nil {
		return dbgen.Release{}, false, err
	} else if ok {
		rel, err := s.q.GetRelease(ctx, dbgen.GetReleaseParams{ID: id, RepoID: ic.RepoID})
		if errors.Is(err, pgx.ErrNoRows) {
			return dbgen.Release{}, false, nil // deleted since
		}
		if err != nil {
			return dbgen.Release{}, false, fmt.Errorf("get release: %w", err)
		}
		return rel, false, nil
	}
	tag := gr.TagName
	if tag == "" || len(tag) > 255 || strings.ContainsAny(tag, "\x00 ~^:?*[\\") {
		ic.Log.WarnContext(ctx, "github release skipped: unusable tag", "repo", ic.RepoID, "release", gr.ID)
		return dbgen.Release{}, false, nil
	}
	sha, err := s.tagCommit(ctx, ic.RepoID, tag)
	if err != nil {
		return dbgen.Release{}, false, err
	}
	author, body, err := ic.Author(ctx, gr.Author, ptrStr(gr.Body), 125000)
	if err != nil {
		return dbgen.Release{}, false, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return dbgen.Release{}, false, fmt.Errorf("release id: %w", err)
	}
	published := ghsync.TimePtr(gr.PublishedAt)
	if gr.Draft {
		published = ghsync.TimePtr(nil)
	}
	updated := ghsync.Stamp(gr.CreatedAt)
	if published.Valid {
		updated = published
	}
	var rel dbgen.Release
	created := false
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		var err error
		rel, err = q.ImportRelease(ctx, dbgen.ImportReleaseParams{ID: id, RepoID: ic.RepoID, Tag: tag,
			Name: ghsync.Clip(ptrStr(gr.Name), 255), Body: body, Draft: gr.Draft, Prerelease: gr.Prerelease,
			Target: ghsync.Clip(gr.Target, 255), CommitSha: sha, AuthorID: author,
			CreatedAt: ghsync.Stamp(gr.CreatedAt), UpdatedAt: updated, PublishedAt: published})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if rel, err = q.GetReleaseByTag(ctx, dbgen.GetReleaseByTagParams{RepoID: ic.RepoID, Tag: tag}); err != nil {
				return fmt.Errorf("get release by tag: %w", err)
			}
		case err != nil:
			return fmt.Errorf("import release: %w", err)
		default:
			created = true
		}
		return ic.Remember(ctx, q, ghsync.MapRelease, gr.ID, rel.ID)
	})
	return rel, created, err
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// importAsset streams the GitHub asset ga into the storage of rel.
// Assets too large, beyond the quota or whose name is taken are skipped
// with a warning.
func (s *Service) importAsset(ctx context.Context, ic ghsync.ImportContext, ownerID uuid.UUID, rel dbgen.Release, ga gitHubAsset) (bool, error) {
	if _, ok, err := ic.Mapped(ctx, s.q, ghsync.MapAsset, ga.ID); err != nil || ok {
		return false, err
	}
	skip := func(why string) (bool, error) {
		ic.Log.WarnContext(ctx, "github release asset skipped", "repo", ic.RepoID, "tag", rel.Tag,
			"asset", ga.Name, "reason", why)
		return false, nil
	}
	if err := ValidateAssetName(ga.Name); err != nil {
		return skip(err.Error())
	}
	if ga.Size < 0 || ga.Size > s.o.MaxAssetSize {
		return skip("larger than the asset size limit")
	}
	cl, err := s.class()
	if err != nil {
		return false, err
	}
	uploader, err := ic.User(ctx, ga.Uploader)
	if err != nil {
		return false, err
	}
	label := ghsync.Clip(ga.Label, 255)
	ctype := assetType(ga.Name, ga.ContentType)
	path := ic.Repo() + "/releases/assets/" + strconv.FormatInt(ga.ID, 10)
	err = ic.API.Download(ctx, path, func(body io.Reader) error {
		_, err := s.storeAsset(ctx, ownerID, rel.RepoID, rel.ID,
			UploadInput{Name: ga.Name, Label: label, Size: ga.Size, Body: body}, ctype, cl, uploader,
			func(q *dbgen.Queries, row dbgen.ReleaseAsset) error {
				return ic.Remember(ctx, q, ghsync.MapAsset, ga.ID, row.ID)
			})
		return err
	})
	switch {
	case errors.Is(err, ErrNameTaken):
		return skip("an asset with this name exists")
	case quota.IsExceeded(err):
		return skip("storage quota exceeded")
	case err != nil:
		return false, fmt.Errorf("import asset %s: %w", ga.Name, err)
	}
	return true, nil
}
