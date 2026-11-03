package ghsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Importer imports one kind of non-git data of a GitHub repository (issues,
// pull requests, labels, milestones, releases...) after its git data.
//
// This is the extension point of the import: the git data is imported by
// the package itself; every other kind of data is an Importer passed in
// Options.Importers, typically by the package that owns that data (for
// example internal/issues for issues and labels). Importers run in order,
// in the import job, after every branch and tag was written to the new
// repository and its lease was released.
//
// An Importer must be idempotent: a failed import job is retried, and the
// importers before the failing one run again. It reads GitHub only through
// ImportContext.API (already authenticated, rate limits and pagination are
// the importer's business) and writes through its own service, never to
// the git repository. A returned error fails the import; its message is
// shown to the user, so it must carry no secret.
type Importer interface {
	// Name identifies the importer in logs and errors ("issues").
	Name() string
	Import(ctx context.Context, ic ImportContext) error
}

// ImportContext is what an Importer receives.
type ImportContext struct {
	ImportID uuid.UUID
	// RepoID is the new local repository.
	RepoID uuid.UUID
	// RemoteOwner and RemoteName name the GitHub repository.
	RemoteOwner, RemoteName string
	// API is authenticated as the connection of the import.
	API *API
	// CreatedBy is the user who started the import (the author of
	// imported data whose GitHub author has no local account).
	CreatedBy uuid.UUID
	Log       *slog.Logger
}

// ImportInput starts an import.
type ImportInput struct {
	ConnectionID uuid.UUID
	RemoteOwner  string
	RemoteName   string
	// Owner, Name, Description and Visibility describe the new repository;
	// Name defaults to RemoteName, Visibility to private.
	Owner, Name, Description, Visibility string
}

// Import is the state of an import.
type Import struct {
	ID                      uuid.UUID
	RepoID                  uuid.UUID
	RepoFullName            string
	RemoteOwner, RemoteName string
	Status, LastError       string
	CreatedAt               time.Time
	StartedAt, FinishedAt   *time.Time
}

func toImport(r dbgen.GithubImport, fullName string) Import {
	return Import{ID: r.ID, RepoID: r.RepoID, RepoFullName: fullName, RemoteOwner: r.RemoteOwner,
		RemoteName: r.RemoteName, Status: r.Status, LastError: r.LastError, CreatedAt: r.CreatedAt.Time,
		StartedAt: ts(r.StartedAt), FinishedAt: ts(r.FinishedAt)}
}

// checkRemote validates a GitHub owner and repository name.
func checkRemote(owner, name string) error {
	if !remoteOwnerRe.MatchString(owner) {
		return invalidf("remoteOwner is not a valid GitHub account name")
	}
	if !remoteNameRe.MatchString(name) || name == "." || name == ".." || strings.HasSuffix(name, ".git") {
		return invalidf("remoteName is not a valid GitHub repository name")
	}
	return nil
}

// Import creates a repository and schedules the import of the git data
// of a GitHub repository into it.
func (s *Service) Import(ctx context.Context, who auth.Principal, in ImportInput, c identity.Client) (Import, error) {
	if who.UserID == uuid.Nil {
		return Import{}, ErrForbidden
	}
	if err := checkRemote(in.RemoteOwner, in.RemoteName); err != nil {
		return Import{}, err
	}
	rs, err := s.repoSvc()
	if err != nil {
		return Import{}, err
	}
	owner := in.Owner
	if owner == "" {
		owner = who.Username
	}
	ns, err := s.q.GetNamespaceByName(ctx, owner)
	if noRows(err) {
		return Import{}, ErrNotFound
	}
	if err != nil {
		return Import{}, fmt.Errorf("get namespace: %w", err)
	}
	conn, err := s.usable(ctx, who, in.ConnectionID, ns.ID)
	if err != nil {
		return Import{}, err
	}
	cred, err := s.credential(conn)
	if err != nil {
		return Import{}, err
	}
	api, err := s.tokens.api(ctx, cred)
	if err != nil {
		return Import{}, fmt.Errorf("GitHub authentication: %w", err)
	}
	gh, err := getRepo(ctx, api, in.RemoteOwner, in.RemoteName)
	if notFound(err) {
		return Import{}, invalidf("the GitHub repository %s/%s does not exist or the connection cannot read it", in.RemoteOwner, in.RemoteName)
	}
	if err != nil {
		return Import{}, fmt.Errorf("get GitHub repository: %w", err)
	}
	name := in.Name
	if name == "" {
		name = in.RemoteName
	}
	repo, err := rs.Create(ctx, who, repos.CreateInput{Owner: owner, Name: name, Description: in.Description,
		Visibility: in.Visibility, DefaultBranch: gh.DefaultBranch}, c)
	if err != nil {
		return Import{}, err
	}
	row, err := s.q.InsertGitHubImport(ctx, dbgen.InsertGitHubImportParams{ID: uuid.New(), RepoID: repo.ID,
		ConnectionID: uuid.NullUUID{UUID: conn.ID, Valid: true}, RemoteOwner: in.RemoteOwner,
		RemoteName: in.RemoteName, CreatedBy: nullUser(who.UserID)})
	if err != nil {
		return Import{}, fmt.Errorf("insert import: %w", err)
	}
	if err := s.enqueue(ctx, ImportArgs{ImportID: row.ID}); err != nil {
		return Import{}, fmt.Errorf("enqueue import: %w", err)
	}
	s.o.Log.InfoContext(ctx, "github import scheduled", "repo", repo.ID, "remote", in.RemoteOwner+"/"+in.RemoteName)
	return toImport(row, repo.FullName()), nil
}

// GetImport returns the import of a repository.
func (s *Service) GetImport(ctx context.Context, who auth.Principal, owner, name string) (Import, error) {
	rs, err := s.repoSvc()
	if err != nil {
		return Import{}, err
	}
	repo, err := rs.Authorize(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return Import{}, err
	}
	row, err := s.q.GetGitHubImportByRepo(ctx, repo.ID)
	if noRows(err) {
		return Import{}, ErrNotFound
	}
	if err != nil {
		return Import{}, fmt.Errorf("get import: %w", err)
	}
	return toImport(row, repo.FullName()), nil
}

// runImport runs an import job: git data first, then the importers.
func (s *Service) runImport(ctx context.Context, id uuid.UUID) error {
	imp, err := s.q.GetGitHubImport(ctx, id)
	if noRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get import: %w", err)
	}
	if imp.Status == "done" {
		return nil
	}
	if err := s.setImport(ctx, id, "running", ""); err != nil {
		return err
	}
	if err := s.importGit(ctx, imp); err != nil {
		if errors.Is(err, gitstore.ErrBusy) {
			return err
		}
		return s.failImport(ctx, imp, err)
	}
	if len(s.o.Importers) > 0 {
		r, err := s.open(ctx, imp.ConnectionID.UUID, imp.RemoteOwner, imp.RemoteName)
		if err != nil {
			return s.failImport(ctx, imp, err)
		}
		for _, im := range s.o.Importers {
			if err := im.Import(ctx, ImportContext{ImportID: imp.ID, RepoID: imp.RepoID,
				RemoteOwner: imp.RemoteOwner, RemoteName: imp.RemoteName, API: r.api,
				CreatedBy: imp.CreatedBy.UUID, Log: s.o.Log.With("importer", im.Name())}); err != nil {
				return s.failImport(ctx, imp, fmt.Errorf("import %s: %w", im.Name(), err))
			}
		}
	}
	s.o.Log.InfoContext(ctx, "github import done", "repo", imp.RepoID)
	return s.setImport(ctx, id, "done", "")
}

func (s *Service) setImport(ctx context.Context, id uuid.UUID, status, msg string) error {
	if err := s.q.SetGitHubImportStatus(ctx, dbgen.SetGitHubImportStatusParams{ID: id, Status: status, LastError: msg}); err != nil {
		return fmt.Errorf("set import status: %w", err)
	}
	return nil
}

// failImport records a final failure: the job is not retried, the user
// sees the message and can delete the repository and import again.
func (s *Service) failImport(ctx context.Context, imp dbgen.GithubImport, err error) error {
	msg := safeError(err)
	s.o.Log.WarnContext(ctx, "github import failed", "repo", imp.RepoID, "error", msg)
	return s.setImport(ctx, imp.ID, "error", msg)
}

// importGit fetches every branch and tag of GitHub and writes them in one
// transaction through the hook session (journal, protection).
func (s *Service) importGit(ctx context.Context, imp dbgen.GithubImport) error {
	if !imp.ConnectionID.Valid {
		return errors.New("the GitHub connection was deleted")
	}
	r, err := s.open(ctx, imp.ConnectionID.UUID, imp.RemoteOwner, imp.RemoteName)
	if err != nil {
		return err
	}
	l, err := s.o.Tier.Acquire(ctx, imp.RepoID, gitstore.Write)
	if err != nil {
		return fmt.Errorf("acquire repository: %w", err)
	}
	defer l.Release()
	scratch, rem, err := fetchRemote(ctx, l, r)
	if err != nil {
		return err
	}
	local, err := listRefs(ctx, l.Path, headsPrefix, tagsPrefix)
	if err != nil {
		return err
	}
	var edits []edit
	for _, ref := range sortedKeys(rem) {
		if local[ref] == rem[ref] {
			continue // retried job: already imported
		}
		edits = append(edits, edit{ref: ref, newSHA: rem[ref], oldSHA: local[ref], verify: true})
	}
	edits = append(edits, cleanup(scratch).edits...)
	if len(edits) == len(scratch) {
		return updateRefsRaw(ctx, l.Path, edits)
	}
	return s.updateRefs(ctx, l, edits)
}
