package releases

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/ghsync"
)

// SyncGitHubReleases reconciles published releases after their git tags agree.
func (s *Service) SyncGitHubReleases(ctx context.Context, ic ghsync.ImportContext) error {
	if len(ic.SyncedTags) == 0 {
		return nil
	}
	repo, err := s.q.GetRepo(ctx, ic.RepoID)
	if err != nil {
		return fmt.Errorf("get repository: %w", err)
	}
	local, err := s.localSyncReleases(ctx, ic)
	if err != nil {
		return err
	}
	var remote []gitHubRelease
	err = ghsync.Each(ctx, ic.API, ic.Repo()+"/releases", func(gr gitHubRelease) error {
		if ic.SyncedTags[gr.TagName] {
			remote = append(remote, gr)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("list GitHub releases: %w", err)
	}
	links, err := s.q.ListGitHubImportMap(ctx, dbgen.ListGitHubImportMapParams{RepoID: ic.RepoID, Kind: ghsync.MapAsset})
	if err != nil {
		return fmt.Errorf("list GitHub asset mappings: %w", err)
	}
	assetLinks := make(map[int64]uuid.UUID, len(links))
	for _, link := range links {
		assetLinks[link.GithubID] = link.LocalID
	}

	var syncErr error
	for _, gr := range remote {
		rel := local[gr.TagName]
		delete(local, gr.TagName) // A draft also occupies the tag.
		if gr.Draft || gr.PublishedAt == nil || gr.PublishedAt.IsZero() {
			continue
		}
		if err := s.syncGitHubRelease(ctx, ic, repo.Repo.OwnerID, rel, gr, assetLinks); err != nil {
			syncErr = errors.Join(syncErr, fmt.Errorf("sync release %q: %w", gr.TagName, err))
		}
	}
	for tag, rel := range local {
		if err := s.syncGitHubRelease(ctx, ic, repo.Repo.OwnerID, rel, gitHubRelease{}, assetLinks); err != nil {
			syncErr = errors.Join(syncErr, fmt.Errorf("sync release %q: %w", tag, err))
		}
	}
	return syncErr
}

func (s *Service) localSyncReleases(ctx context.Context, ic ghsync.ImportContext) (map[string]dbgen.Release, error) {
	releases := make(map[string]dbgen.Release)
	args := dbgen.ListReleasesParams{RepoID: ic.RepoID, IncludeDrafts: true, Lim: 100}
	for {
		page, err := s.q.ListReleases(ctx, args)
		if err != nil {
			return nil, fmt.Errorf("list local releases: %w", err)
		}
		for _, rel := range page {
			if ic.SyncedTags[rel.Tag] {
				releases[rel.Tag] = rel
			}
		}
		if len(page) < int(args.Lim) {
			return releases, nil
		}
		args.Off += args.Lim
	}
}

func (s *Service) syncGitHubRelease(ctx context.Context, ic ghsync.ImportContext, ownerID uuid.UUID, rel dbgen.Release, gr gitHubRelease, assetLinks map[int64]uuid.UUID) error {
	var metadataErr error
	switch {
	case rel.ID != uuid.Nil && (rel.Draft || !rel.PublishedAt.Valid):
		return nil
	case rel.ID == uuid.Nil:
		next, _, err := s.createImportedRelease(ctx, ic, gr, true)
		if err != nil || next.ID == uuid.Nil || next.Draft || !next.PublishedAt.Valid {
			return err
		}
		rel = next
	case gr.ID == 0:
		payload := githubReleaseMetadata{
			TagName: rel.Tag, Name: rel.Name, Body: rel.Body, Prerelease: rel.Prerelease, MakeLatest: "legacy",
		}
		if err := ic.API.Do(ctx, http.MethodPost, ic.Repo()+"/releases", payload, &gr); err != nil {
			return fmt.Errorf("publish GitHub release: %w", err)
		}
		if gr.ID <= 0 {
			return errors.New("GitHub returned no release ID")
		}
	default:
		metadataErr = s.syncReleaseMetadata(ctx, ic, rel, gr)
	}
	return errors.Join(metadataErr, s.syncReleaseAssets(ctx, ic, ownerID, rel, gr, assetLinks))
}

type githubReleaseMetadata struct {
	TagName    string `json:"tag_name,omitempty"`
	Name       string `json:"name"`
	Body       string `json:"body"`
	Prerelease bool   `json:"prerelease"`
	MakeLatest string `json:"make_latest,omitempty"`
}

func (s *Service) syncReleaseMetadata(ctx context.Context, ic ghsync.ImportContext, rel dbgen.Release, gr gitHubRelease) error {
	meta := githubReleaseMetadata{Name: ghsync.Clip(ptrStr(gr.Name), 255), Body: ghsync.Clip(ptrStr(gr.Body), 125000), Prerelease: gr.Prerelease}
	local := githubReleaseMetadata{Name: rel.Name, Body: rel.Body, Prerelease: rel.Prerelease}
	// Older imports may have prefixed an attribution for an unlinked author.
	attribution := ghsync.Attribution(gr.Author)
	if local.Body != meta.Body && strings.HasPrefix(local.Body, attribution) {
		meta.Body = ghsync.Clip(attribution+ptrStr(gr.Body), 125000)
	}
	if meta == local {
		return nil
	}
	localTime := rel.UpdatedAt.Time.Truncate(time.Second)
	remoteTime := gr.UpdatedAt
	// Without updated_at, published_at only proves a lower bound on GitHub's last edit.
	if remoteTime.IsZero() || gr.PublishedAt.After(remoteTime) {
		remoteTime = *gr.PublishedAt
	}
	switch {
	case remoteTime.After(localTime):
		return s.applyGitHubReleaseMetadata(ctx, rel, meta)
	case !gr.UpdatedAt.IsZero() && localTime.After(remoteTime):
		patch := make(map[string]any, 3)
		if local.Name != meta.Name {
			patch["name"] = local.Name
		}
		if local.Body != meta.Body {
			patch["body"] = strings.TrimPrefix(local.Body, attribution)
		}
		if local.Prerelease != meta.Prerelease {
			patch["prerelease"] = local.Prerelease
		}
		if err := ic.API.Do(ctx, http.MethodPatch, githubReleasePath(ic, gr), patch, nil); err != nil {
			return fmt.Errorf("update GitHub release: %w", err)
		}
	}
	return nil
}

func (s *Service) applyGitHubReleaseMetadata(ctx context.Context, rel dbgen.Release, meta githubReleaseMetadata) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		current, err := q.GetReleaseForUpdate(ctx, dbgen.GetReleaseForUpdateParams{ID: rel.ID, RepoID: rel.RepoID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock release: %w", err)
		}
		if current.Draft || current.UpdatedAt != rel.UpdatedAt {
			return nil
		}
		_, err = q.UpdateRelease(ctx, dbgen.UpdateReleaseParams{ID: rel.ID, RepoID: rel.RepoID, Tag: rel.Tag,
			Name: meta.Name, Body: meta.Body, Draft: rel.Draft, Prerelease: meta.Prerelease,
			Target: rel.Target, CommitSha: rel.CommitSha, PublishedAt: rel.PublishedAt, MakeLatest: rel.MakeLatest})
		if err != nil {
			return fmt.Errorf("update local release: %w", err)
		}
		return nil
	})
}

func githubReleasePath(ic ghsync.ImportContext, gr gitHubRelease) string {
	return ic.Repo() + "/releases/" + strconv.FormatInt(gr.ID, 10)
}

func (s *Service) syncReleaseAssets(ctx context.Context, ic ghsync.ImportContext, ownerID uuid.UUID, rel dbgen.Release, gr gitHubRelease, assetLinks map[int64]uuid.UUID) error {
	local, err := s.q.ListReleaseAssets(ctx, []uuid.UUID{rel.ID})
	if err != nil {
		return fmt.Errorf("list local release assets: %w", err)
	}
	localNames := make(map[string]bool, len(local))
	for _, asset := range local {
		localNames[asset.Name] = true
	}
	var remote []gitHubAsset
	err = ghsync.Each(ctx, ic.API, githubReleasePath(ic, gr)+"/assets", func(asset gitHubAsset) error {
		remote = append(remote, asset)
		return nil
	})
	if err != nil {
		return fmt.Errorf("list GitHub release assets: %w", err)
	}

	remoteNames := make(map[string]bool, len(remote))
	matchedLocal := make(map[uuid.UUID]bool, len(remote))
	for _, asset := range remote {
		remoteNames[asset.Name] = true
		if id := assetLinks[asset.ID]; id != uuid.Nil {
			matchedLocal[id] = true
		}
	}
	localIDs := make(map[uuid.UUID]bool, len(local))
	var syncErr error
	for _, asset := range local {
		localIDs[asset.ID] = true
		if !remoteNames[asset.Name] && !matchedLocal[asset.ID] {
			if err := s.uploadGitHubAsset(ctx, ic, gr, asset); err != nil {
				syncErr = errors.Join(syncErr, fmt.Errorf("upload asset %q: %w", asset.Name, err))
			}
		}
	}
	for _, asset := range remote {
		if !localNames[asset.Name] && !localIDs[assetLinks[asset.ID]] && (asset.State == "" || asset.State == "uploaded") {
			if _, err := s.storeGitHubAsset(ctx, ic, ownerID, rel, asset); err != nil {
				syncErr = errors.Join(syncErr, err)
			}
		}
	}
	return syncErr
}

func (s *Service) uploadGitHubAsset(ctx context.Context, ic ghsync.ImportContext, gr gitHubRelease, asset dbgen.ReleaseAsset) error {
	base, _, _ := strings.Cut(gr.UploadURL, "{")
	if base == "" {
		return errors.New("GitHub returned no asset upload URL")
	}
	endpoint, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("parse asset upload URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("name", asset.Name)
	if asset.Label != "" {
		query.Set("label", asset.Label)
	}
	endpoint.RawQuery = query.Encode()

	reader, err := s.openAssetObject(ctx, asset, nil)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()

	var uploaded gitHubAsset
	if err := ic.API.Upload(ctx, endpoint.String(), asset.ContentType, asset.Size, reader, &uploaded); err != nil {
		return err
	}
	if uploaded.ID <= 0 {
		return errors.New("GitHub returned no asset ID")
	}
	// GitHub can normalize a filename; remember the ID to prevent a duplicate download.
	return ic.Remember(ctx, s.q, ghsync.MapAsset, uploaded.ID, asset.ID)
}
