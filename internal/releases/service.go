// Package releases implements releases bound to git tags and their downloadable
// assets (PLAN §4.2). Release metadata lives in the database; asset bytes
// are streamed into the storage tier under the "releases" data type (or "lfs"
// when no releases policy is configured). A tag that does not exist yet is
// created through internal/git under a Write lease, and the git journal is
// marked dirty and synced so the new ref is replicated like a push.
package releases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// Storage policies (data types) assets are written to, in order of preference.
const (
	PolicyReleases = "releases"
	PolicyLFS      = "lfs"
)

// DefaultMaxAssetSize bounds one asset (5 GiB).
const DefaultMaxAssetSize int64 = 5 << 30

// Audit actions.
const (
	ActionCreated      = "release.created"
	ActionUpdated      = "release.updated"
	ActionDeleted      = "release.deleted"
	ActionAssetAdded   = "release.asset_uploaded"
	ActionAssetRemoved = "release.asset_deleted"
)

// Errors. Repository lookups surface repos.ErrNotFound, repos.ErrForbidden and
// *repos.MovedError unchanged.
var (
	ErrNotFound  = errors.New("release not found")
	ErrTagTaken  = errors.New("a release for this tag already exists")
	ErrNameTaken = errors.New("an asset with this name already exists in the release")
	// ErrNoStorage means no storage policy is configured for assets.
	ErrNoStorage = errors.New("release assets are not available: no storage policy")
)

// ValidationError is a rejected input (HTTP 400).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// RepoAccess resolves a repository for a caller (*repos.Service).
type RepoAccess interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
}

// GitTier hands out locked hot repositories (*gitstore.Tier).
type GitTier interface {
	Acquire(ctx context.Context, repoID uuid.UUID, mode gitstore.Mode) (*gitstore.Lease, error)
}

// Journal replicates a repository whose refs changed (*gitjournal.Journal).
type Journal interface {
	Sync(ctx context.Context, repo uuid.UUID) error
}

// Storage opens the storage class of a data type (*tier.Tier).
type Storage interface {
	Class(policy string) (*tier.Class, error)
}

// Options configures a Service.
type Options struct {
	Pool    *pgxpool.Pool
	Repos   RepoAccess
	Git     GitTier // nil: tags cannot be created, releases need existing tags
	Journal Journal // nil: new tags are replicated by the sweep job only
	Storage Storage
	// MaxAssetSize bounds one upload; 0 uses DefaultMaxAssetSize.
	MaxAssetSize int64
	Log          *slog.Logger
	Now          func() time.Time
}

// Service implements the release flows.
type Service struct {
	o   Options
	q   *dbgen.Queries
	log *slog.Logger
}

// New builds a Service.
func New(o Options) *Service {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxAssetSize <= 0 {
		o.MaxAssetSize = DefaultMaxAssetSize
	}
	return &Service{o: o, q: dbgen.New(o.Pool), log: o.Log}
}

// Release is a release with its assets.
type Release struct {
	ID          uuid.UUID
	RepoID      uuid.UUID
	Tag         string
	Name        string
	Body        string // markdown
	Draft       bool
	Prerelease  bool
	Latest      bool
	Target      string
	Commit      string
	Author      string
	CreatedAt   time.Time
	PublishedAt *time.Time
	Assets      []Asset
}

// Asset is a file attached to a release.
type Asset struct {
	ID            uuid.UUID
	ReleaseID     uuid.UUID
	Name          string
	Label         string
	ContentType   string
	Size          int64
	SHA256        string // hex
	DownloadCount int64
	Uploader      uuid.UUID
	CreatedAt     time.Time
}

// access resolves the repository; write needs the releases permission,
// otherwise reading is enough. canDraft reports whether drafts are visible.
func (s *Service) access(ctx context.Context, who auth.Principal, owner, name string, write bool) (repos.Repo, error) {
	r, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return repos.Repo{}, err
	}
	if write && !authz.Allowed(r.Permission, authz.ActManageBranches) {
		return repos.Repo{}, repos.ErrForbidden
	}
	return r, nil
}

func canSeeDrafts(r repos.Repo) bool { return authz.Allowed(r.Permission, authz.ActManageBranches) }

func (s *Service) auditEvent(who auth.Principal, action string, r repos.Repo, rel uuid.UUID, c identity.Client, data map[string]any) audit.Event {
	org := uuid.Nil
	if r.OwnerKind == "org" {
		org = r.OwnerID
	}
	if data == nil {
		data = map[string]any{}
	}
	data["repo"] = r.FullName()
	return audit.Event{Actor: who.UserID, Action: action, TargetType: "release", TargetID: rel.String(), IP: c.IP, Data: data, OrgID: org}
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// hydrate attaches assets, author names and the latest flag to rows.
func (s *Service) hydrate(ctx context.Context, repoID uuid.UUID, rows []dbgen.Release) ([]Release, error) {
	if len(rows) == 0 {
		return []Release{}, nil
	}
	ids := make([]uuid.UUID, 0, len(rows))
	authors := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
		if r.AuthorID.Valid {
			authors = append(authors, r.AuthorID.UUID)
		}
	}
	assets, err := s.q.ListReleaseAssets(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list release assets: %w", err)
	}
	by := map[uuid.UUID][]Asset{}
	for _, a := range assets {
		by[a.ReleaseID] = append(by[a.ReleaseID], assetFrom(a))
	}
	names := map[uuid.UUID]string{}
	if len(authors) > 0 {
		ns, err := s.q.ReleaseAuthorNames(ctx, authors)
		if err != nil {
			return nil, fmt.Errorf("load release authors: %w", err)
		}
		for _, n := range ns {
			names[n.ID] = n.Name
		}
	}
	var latest uuid.UUID
	if l, err := s.q.GetLatestRelease(ctx, repoID); err == nil {
		latest = l.ID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get latest release: %w", err)
	}
	out := make([]Release, 0, len(rows))
	for _, r := range rows {
		rel := Release{ID: r.ID, RepoID: r.RepoID, Tag: r.Tag, Name: r.Name, Body: r.Body, Draft: r.Draft,
			Prerelease: r.Prerelease, Latest: r.ID == latest, Target: r.Target, Commit: r.CommitSha,
			CreatedAt: r.CreatedAt.Time, PublishedAt: tsPtr(r.PublishedAt), Assets: by[r.ID]}
		if rel.Assets == nil {
			rel.Assets = []Asset{}
		}
		if r.AuthorID.Valid {
			rel.Author = names[r.AuthorID.UUID]
		}
		out = append(out, rel)
	}
	return out, nil
}

func assetFrom(a dbgen.ReleaseAsset) Asset {
	return Asset{ID: a.ID, ReleaseID: a.ReleaseID, Name: a.Name, Label: a.Label, ContentType: a.ContentType, Size: a.Size,
		SHA256: fmt.Sprintf("%x", a.Sha256), DownloadCount: a.DownloadCount, Uploader: a.UploaderID.UUID, CreatedAt: a.CreatedAt.Time}
}

func pageArgs(offset, limit int) (int32, int32) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	return int32(offset), int32(limit) //nolint:gosec // bounded above
}

// List returns the releases of a repository, newest first. Drafts are only
// shown to callers who can manage releases. tag filters on an exact tag.
func (s *Service) List(ctx context.Context, who auth.Principal, owner, name, tag string, offset, limit int) ([]Release, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return nil, err
	}
	off, lim := pageArgs(offset, limit)
	rows, err := s.q.ListReleases(ctx, dbgen.ListReleasesParams{RepoID: r.ID, IncludeDrafts: canSeeDrafts(r), Tag: tag, Lim: lim, Off: off})
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	return s.hydrate(ctx, r.ID, rows)
}

func (s *Service) one(ctx context.Context, r repos.Repo, id uuid.UUID) (dbgen.Release, error) {
	row, err := s.q.GetRelease(ctx, dbgen.GetReleaseParams{ID: id, RepoID: r.ID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Draft && !canSeeDrafts(r)) {
		return dbgen.Release{}, ErrNotFound
	}
	if err != nil {
		return dbgen.Release{}, fmt.Errorf("get release: %w", err)
	}
	return row, nil
}

// Get returns one release.
func (s *Service) Get(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (Release, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return Release{}, err
	}
	row, err := s.one(ctx, r, id)
	if err != nil {
		return Release{}, err
	}
	return s.single(ctx, r.ID, row)
}

func (s *Service) single(ctx context.Context, repoID uuid.UUID, row dbgen.Release) (Release, error) {
	l, err := s.hydrate(ctx, repoID, []dbgen.Release{row})
	if err != nil {
		return Release{}, err
	}
	return l[0], nil
}

// Latest returns the newest published release that is not a prerelease.
func (s *Service) Latest(ctx context.Context, who auth.Principal, owner, name string) (Release, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return Release{}, err
	}
	row, err := s.q.GetLatestRelease(ctx, r.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Release{}, ErrNotFound
	}
	if err != nil {
		return Release{}, fmt.Errorf("get latest release: %w", err)
	}
	return s.single(ctx, r.ID, row)
}

// CreateInput is a new release. Target is the branch or commit a missing tag
// is created from (default: the default branch of the repository).
type CreateInput struct {
	Tag        string
	Name       string
	Body       string
	Draft      bool
	Prerelease bool
	Target     string
}

func checkText(name, v string, max int) error {
	if utf8.RuneCountInString(v) > max {
		return invalid("%s is longer than %d characters", name, max)
	}
	if !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
		return invalid("%s is not valid text", name)
	}
	return nil
}

func validateFields(name, body, target string) error {
	if err := checkText("name", name, 255); err != nil {
		return err
	}
	if err := checkText("body", body, 125000); err != nil {
		return err
	}
	if target != "" {
		if err := git.ValidateRevision(target); err != nil {
			return invalid("target: %v", err)
		}
	}
	return nil
}

// Create adds a release. Unless it is a draft, the tag is created when
// missing, pointing at the target.
func (s *Service) Create(ctx context.Context, who auth.Principal, owner, name string, in CreateInput, c identity.Client) (Release, error) {
	r, err := s.access(ctx, who, owner, name, true)
	if err != nil {
		return Release{}, err
	}
	if err := git.ValidateTagName(in.Tag); err != nil {
		return Release{}, invalid("tag: %v", err)
	}
	if err := validateFields(in.Name, in.Body, in.Target); err != nil {
		return Release{}, err
	}
	if exists, err := s.tagTaken(ctx, r.ID, in.Tag, uuid.Nil); err != nil {
		return Release{}, err
	} else if exists {
		return Release{}, ErrTagTaken
	}
	var sha string
	var published pgtype.Timestamptz
	if !in.Draft {
		if sha, err = s.ensureTag(ctx, r, in.Tag, in.Target); err != nil {
			return Release{}, err
		}
		published = pgtype.Timestamptz{Time: s.o.Now(), Valid: true}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Release{}, fmt.Errorf("release id: %w", err)
	}
	var row dbgen.Release
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		var err error
		row, err = q.InsertRelease(ctx, dbgen.InsertReleaseParams{ID: id, RepoID: r.ID, Tag: in.Tag, Name: in.Name, Body: in.Body,
			Draft: in.Draft, Prerelease: in.Prerelease, Target: in.Target, CommitSha: sha,
			AuthorID: uuid.NullUUID{UUID: who.UserID, Valid: who.UserID != uuid.Nil}, PublishedAt: published})
		if isUnique(err) {
			return ErrTagTaken
		}
		if err != nil {
			return fmt.Errorf("insert release: %w", err)
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionCreated, r, id, c, map[string]any{"tag": in.Tag, "draft": in.Draft}))
	})
	if err != nil {
		return Release{}, err
	}
	return s.single(ctx, r.ID, row)
}

func (s *Service) tagTaken(ctx context.Context, repoID uuid.UUID, tag string, except uuid.UUID) (bool, error) {
	rows, err := s.q.ListReleases(ctx, dbgen.ListReleasesParams{RepoID: repoID, IncludeDrafts: true, Tag: tag, Lim: 2})
	if err != nil {
		return false, fmt.Errorf("find release by tag: %w", err)
	}
	for _, x := range rows {
		if x.ID != except {
			return true, nil
		}
	}
	return false, nil
}

// UpdateInput changes a release; nil fields are kept. The tag can only change
// while the release is a draft.
type UpdateInput struct {
	Tag        *string
	Name       *string
	Body       *string
	Draft      *bool
	Prerelease *bool
	Target     *string
}

// Update changes a release. Publishing a draft creates the tag if missing.
func (s *Service) Update(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, in UpdateInput, c identity.Client) (Release, error) {
	r, err := s.access(ctx, who, owner, name, true)
	if err != nil {
		return Release{}, err
	}
	cur, err := s.one(ctx, r, id)
	if err != nil {
		return Release{}, err
	}
	next := cur
	if in.Tag != nil && *in.Tag != cur.Tag {
		if !cur.Draft {
			return Release{}, invalid("the tag of a published release cannot be changed")
		}
		if err := git.ValidateTagName(*in.Tag); err != nil {
			return Release{}, invalid("tag: %v", err)
		}
		if exists, err := s.tagTaken(ctx, r.ID, *in.Tag, cur.ID); err != nil {
			return Release{}, err
		} else if exists {
			return Release{}, ErrTagTaken
		}
		next.Tag = *in.Tag
	}
	if in.Name != nil {
		next.Name = *in.Name
	}
	if in.Body != nil {
		next.Body = *in.Body
	}
	if in.Prerelease != nil {
		next.Prerelease = *in.Prerelease
	}
	if in.Target != nil {
		next.Target = *in.Target
	}
	if in.Draft != nil {
		next.Draft = *in.Draft
	}
	if err := validateFields(next.Name, next.Body, next.Target); err != nil {
		return Release{}, err
	}
	if !next.Draft && !cur.PublishedAt.Valid {
		sha, err := s.ensureTag(ctx, r, next.Tag, next.Target)
		if err != nil {
			return Release{}, err
		}
		next.CommitSha = sha
		next.PublishedAt = pgtype.Timestamptz{Time: s.o.Now(), Valid: true}
	}
	var row dbgen.Release
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if _, err := q.GetReleaseForUpdate(ctx, dbgen.GetReleaseForUpdateParams{ID: id, RepoID: r.ID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock release: %w", err)
		}
		var err error
		row, err = q.UpdateRelease(ctx, dbgen.UpdateReleaseParams{ID: id, RepoID: r.ID, Tag: next.Tag, Name: next.Name, Body: next.Body,
			Draft: next.Draft, Prerelease: next.Prerelease, Target: next.Target, CommitSha: next.CommitSha, PublishedAt: next.PublishedAt})
		if isUnique(err) {
			return ErrTagTaken
		}
		if err != nil {
			return fmt.Errorf("update release: %w", err)
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionUpdated, r, id, c, map[string]any{"tag": next.Tag}))
	})
	if err != nil {
		return Release{}, err
	}
	return s.single(ctx, r.ID, row)
}

// Delete removes a release and its assets. The git tag is kept.
func (s *Service) Delete(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, c identity.Client) error {
	r, err := s.access(ctx, who, owner, name, true)
	if err != nil {
		return err
	}
	var assets []dbgen.ReleaseAsset
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		cur, err := q.GetReleaseForUpdate(ctx, dbgen.GetReleaseForUpdateParams{ID: id, RepoID: r.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock release: %w", err)
		}
		if assets, err = q.ListReleaseAssets(ctx, []uuid.UUID{id}); err != nil {
			return fmt.Errorf("list release assets: %w", err)
		}
		if _, err := q.DeleteRelease(ctx, dbgen.DeleteReleaseParams{ID: id, RepoID: r.ID}); err != nil {
			return fmt.Errorf("delete release: %w", err)
		}
		var total int64
		for _, a := range assets {
			total += a.Size
		}
		if err := quota.Release(ctx, q, r.OwnerID, quota.Delta{Bytes: total}); err != nil {
			return err
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionDeleted, r, id, c, map[string]any{"tag": cur.Tag, "assets": len(assets)}))
	})
	if err != nil {
		return err
	}
	for _, a := range assets {
		s.dropObject(context.WithoutCancel(ctx), a)
	}
	return nil
}

// dropObject deletes the stored bytes of a removed asset. A failure leaves an
// orphan that the cleanup job removes later.
func (s *Service) dropObject(ctx context.Context, a dbgen.ReleaseAsset) {
	cl, err := s.o.Storage.Class(a.StoragePolicy)
	if err == nil {
		err = cl.Delete(ctx, a.StorageKey)
	}
	if err != nil {
		s.log.WarnContext(ctx, "delete release asset object", "asset", a.ID, "policy", a.StoragePolicy, "error", err)
	}
}

// ensureTag returns the commit of refs/tags/<tag>, creating the tag at target
// when it does not exist. It holds a Write lease only while git runs; the
// journal is marked dirty first, like a push, and synced after the lease is
// released.
func (s *Service) ensureTag(ctx context.Context, r repos.Repo, tag, target string) (string, error) {
	if s.o.Git == nil {
		return "", errors.New("git storage is not available")
	}
	if target == "" {
		target = r.DefaultBranch
	}
	sha, created, err := s.tagOnDisk(ctx, r.ID, tag, target)
	if err != nil {
		return "", err
	}
	if created && s.o.Journal != nil {
		if err := s.o.Journal.Sync(context.WithoutCancel(ctx), r.ID); err != nil {
			// The dirty marker stays: the sweep job replicates the tag.
			s.log.WarnContext(ctx, "sync journal after tag creation", "repo", r.ID, "tag", tag, "error", err)
		}
	}
	return sha, nil
}

func (s *Service) tagOnDisk(ctx context.Context, repoID uuid.UUID, tag, target string) (sha string, created bool, err error) {
	l, err := s.o.Git.Acquire(ctx, repoID, gitstore.Write)
	if err != nil {
		return "", false, fmt.Errorf("acquire repository: %w", err)
	}
	defer l.Release()
	ref := "refs/tags/" + tag
	if out, err := git.Run(ctx, l.Path, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
		return strings.TrimSpace(string(out)), false, nil
	} else if !isExit1(err) {
		return "", false, fmt.Errorf("resolve tag: %w", err)
	}
	out, err := git.Run(ctx, l.Path, "rev-parse", "--verify", "--quiet", "--end-of-options", target+"^{commit}")
	if err != nil {
		if isExit1(err) {
			return "", false, invalid("target %q does not exist in the repository", target)
		}
		return "", false, fmt.Errorf("resolve target: %w", err)
	}
	sha = strings.TrimSpace(string(out))
	if err := s.q.MarkRepoJournalDirty(ctx, repoID); err != nil {
		return "", false, fmt.Errorf("mark journal dirty: %w", err)
	}
	// An all-zero old value makes update-ref fail when the tag appeared
	// meanwhile (it cannot while the Write lease is held, but it is cheap).
	zero := strings.Repeat("0", len(sha))
	if _, err := git.Run(ctx, l.Path, "update-ref", ref, sha, zero); err != nil {
		return "", false, fmt.Errorf("create tag: %w", err)
	}
	return sha, true, nil
}

// ---- assets ----

// UploadInput describes an asset upload. Size is the exact length of Body.
type UploadInput struct {
	Name        string
	Label       string
	ContentType string
	Size        int64
	Body        io.Reader
}

// ValidateAssetName checks a file name: a single path element without
// control characters.
func ValidateAssetName(n string) error {
	switch {
	case n == "" || n == "." || n == "..":
		return invalid("asset name is required")
	case utf8.RuneCountInString(n) > 255 || !utf8.ValidString(n):
		return invalid("asset name is too long or not valid UTF-8")
	case strings.ContainsAny(n, "/\\"):
		return invalid("asset name must not contain slashes")
	}
	for _, c := range n {
		if c < 0x20 || c == 0x7f {
			return invalid("asset name must not contain control characters")
		}
	}
	return nil
}

func (s *Service) class() (*tier.Class, error) {
	if s.o.Storage == nil {
		return nil, ErrNoStorage
	}
	for _, p := range []string{PolicyReleases, PolicyLFS} {
		if c, err := s.o.Storage.Class(p); err == nil {
			return c, nil
		}
	}
	return nil, ErrNoStorage
}

// UploadAsset streams an asset into the storage tier and records it. The
// bytes are charged to the quota of the repository owner.
func (s *Service) UploadAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID uuid.UUID, in UploadInput, c identity.Client) (Asset, error) {
	r, err := s.access(ctx, who, owner, name, true)
	if err != nil {
		return Asset{}, err
	}
	if err := ValidateAssetName(in.Name); err != nil {
		return Asset{}, err
	}
	if err := checkText("label", in.Label, 255); err != nil {
		return Asset{}, err
	}
	if in.Size < 0 {
		return Asset{}, invalid("the asset size must be known")
	}
	if in.Size > s.o.MaxAssetSize {
		return Asset{}, invalid("asset is larger than the limit of %d bytes", s.o.MaxAssetSize)
	}
	rel, err := s.one(ctx, r, releaseID)
	if err != nil {
		return Asset{}, err
	}
	cl, err := s.class()
	if err != nil {
		return Asset{}, err
	}
	ctype := in.ContentType
	if ctype == "" {
		ctype = mime.TypeByExtension(path.Ext(in.Name))
	}
	if mt, _, err := mime.ParseMediaType(ctype); err != nil || len(ctype) > 255 {
		ctype = "application/octet-stream"
	} else {
		ctype = mt
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Asset{}, fmt.Errorf("asset id: %w", err)
	}
	if err := quota.Reserve(ctx, s.q, r.OwnerID, quota.Delta{Bytes: in.Size}); err != nil {
		return Asset{}, err
	}
	refund := func() {
		if err := quota.Release(context.WithoutCancel(ctx), s.q, r.OwnerID, quota.Delta{Bytes: in.Size}); err != nil {
			s.log.ErrorContext(ctx, "release asset quota", "repo", r.ID, "error", err)
		}
	}
	key := fmt.Sprintf("releases/%s/%s/%s", r.ID, rel.ID, id)
	obj, err := cl.Put(ctx, key, in.Body, in.Size, nil)
	if err != nil {
		refund()
		return Asset{}, fmt.Errorf("store asset: %w", err)
	}
	var row dbgen.ReleaseAsset
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		// The release may have been deleted while the bytes were uploading.
		if _, err := q.GetReleaseForUpdate(ctx, dbgen.GetReleaseForUpdateParams{ID: rel.ID, RepoID: r.ID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock release: %w", err)
		}
		var err error
		row, err = q.InsertReleaseAsset(ctx, dbgen.InsertReleaseAssetParams{ID: id, ReleaseID: rel.ID, RepoID: r.ID, Name: in.Name,
			Label: in.Label, ContentType: ctype, Size: obj.Size, Sha256: obj.SHA256, StoragePolicy: cl.Name(), StorageKey: key,
			UploaderID: uuid.NullUUID{UUID: who.UserID, Valid: who.UserID != uuid.Nil}})
		if isUnique(err) {
			return ErrNameTaken
		}
		if err != nil {
			return fmt.Errorf("insert asset: %w", err)
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionAssetAdded, r, rel.ID, c,
			map[string]any{"asset": in.Name, "size": obj.Size}))
	})
	if err != nil {
		refund()
		if delErr := cl.Delete(context.WithoutCancel(ctx), key); delErr != nil {
			s.log.WarnContext(ctx, "delete unreferenced asset object", "key", key, "error", delErr)
		}
		return Asset{}, err
	}
	return assetFrom(row), nil
}

// UpdateAsset renames an asset or changes its label; nil fields are kept.
func (s *Service) UpdateAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID, newName, label *string, c identity.Client) (Asset, error) {
	r, err := s.access(ctx, who, owner, name, true)
	if err != nil {
		return Asset{}, err
	}
	rel, err := s.one(ctx, r, releaseID)
	if err != nil {
		return Asset{}, err
	}
	cur, err := s.q.GetReleaseAsset(ctx, dbgen.GetReleaseAssetParams{ID: assetID, ReleaseID: rel.ID, RepoID: r.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("get asset: %w", err)
	}
	n, l := cur.Name, cur.Label
	if newName != nil {
		if err := ValidateAssetName(*newName); err != nil {
			return Asset{}, err
		}
		n = *newName
	}
	if label != nil {
		if err := checkText("label", *label, 255); err != nil {
			return Asset{}, err
		}
		l = *label
	}
	row, err := s.q.UpdateReleaseAsset(ctx, dbgen.UpdateReleaseAssetParams{ID: assetID, ReleaseID: rel.ID, RepoID: r.ID, Name: n, Label: l})
	if isUnique(err) {
		return Asset{}, ErrNameTaken
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("update asset: %w", err)
	}
	return assetFrom(row), nil
}

// DeleteAsset removes an asset and its stored bytes and refunds the quota.
func (s *Service) DeleteAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID, c identity.Client) error {
	r, err := s.access(ctx, who, owner, name, true)
	if err != nil {
		return err
	}
	rel, err := s.one(ctx, r, releaseID)
	if err != nil {
		return err
	}
	var a dbgen.ReleaseAsset
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		var err error
		if a, err = q.GetReleaseAsset(ctx, dbgen.GetReleaseAssetParams{ID: assetID, ReleaseID: rel.ID, RepoID: r.ID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get asset: %w", err)
		}
		if _, err := q.DeleteReleaseAsset(ctx, dbgen.DeleteReleaseAssetParams{ID: assetID, ReleaseID: rel.ID, RepoID: r.ID}); err != nil {
			return fmt.Errorf("delete asset: %w", err)
		}
		if err := quota.Release(ctx, q, r.OwnerID, quota.Delta{Bytes: a.Size}); err != nil {
			return err
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionAssetRemoved, r, rel.ID, c, map[string]any{"asset": a.Name}))
	})
	if err != nil {
		return err
	}
	s.dropObject(context.WithoutCancel(ctx), a)
	return nil
}

// GetAsset returns the metadata of an asset visible to the caller.
func (s *Service) GetAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID) (Asset, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return Asset{}, err
	}
	rel, err := s.one(ctx, r, releaseID)
	if err != nil {
		return Asset{}, err
	}
	row, err := s.q.GetReleaseAsset(ctx, dbgen.GetReleaseAssetParams{ID: assetID, ReleaseID: rel.ID, RepoID: r.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("get asset: %w", err)
	}
	return assetFrom(row), nil
}

// OpenAsset opens the bytes of an asset (rng selects a part) and counts the
// download when the read starts at the beginning. The caller closes the reader.
func (s *Service) OpenAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID, rng *blob.Range) (io.ReadCloser, Asset, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return nil, Asset{}, err
	}
	rel, err := s.one(ctx, r, releaseID)
	if err != nil {
		return nil, Asset{}, err
	}
	row, err := s.q.GetReleaseAsset(ctx, dbgen.GetReleaseAssetParams{ID: assetID, ReleaseID: rel.ID, RepoID: r.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, Asset{}, ErrNotFound
	}
	if err != nil {
		return nil, Asset{}, fmt.Errorf("get asset: %w", err)
	}
	if s.o.Storage == nil {
		return nil, Asset{}, ErrNoStorage
	}
	cl, err := s.o.Storage.Class(row.StoragePolicy)
	if err != nil {
		return nil, Asset{}, fmt.Errorf("asset storage: %w", err)
	}
	rc, _, err := cl.Get(ctx, row.StorageKey, rng)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return nil, Asset{}, fmt.Errorf("asset %s: object missing: %w", row.ID, err)
		}
		return nil, Asset{}, fmt.Errorf("open asset: %w", err)
	}
	if rng == nil || rng.Offset == 0 {
		if err := s.q.BumpReleaseAssetDownloads(ctx, row.ID); err != nil {
			s.log.WarnContext(ctx, "count asset download", "asset", row.ID, "error", err)
		}
		row.DownloadCount++
	}
	return rc, assetFrom(row), nil
}

// ---- helpers ----

func (s *Service) tx(ctx context.Context, fn func(*dbgen.Queries) error) error {
	tx, err := s.o.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}
