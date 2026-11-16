package artifacts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/actions/blobsvc"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// The methods below serve the artifact listings and downloads of the
// GitHub compatible API. They do no access control: the caller has
// already checked that the user may read (or, to delete, write) the
// repository.

// ErrNotFound is an artifact that does not exist in the repository.
var ErrNotFound = errors.New("artifact not found")

// Artifact is a finalized artifact.
type Artifact struct {
	ID        int64
	RepoID    uuid.UUID
	RunID     uuid.UUID
	Name      string
	Size      int64
	Digest    string
	Expired   bool
	CreatedAt time.Time
	ExpiresAt time.Time
}

func artifactFrom(a dbgen.ActionsArtifact, now time.Time) Artifact {
	return Artifact{ID: a.ID, RepoID: a.RepoID, RunID: a.RunID, Name: a.Name, Size: a.Size, Digest: a.Digest,
		Expired: !a.ExpiresAt.Time.After(now), CreatedAt: a.CreatedAt.Time, ExpiresAt: a.ExpiresAt.Time}
}

// ListFilter selects artifacts of a repository.
type ListFilter struct {
	RunID       uuid.UUID // uuid.Nil = every run
	Name        string    // "" = every name
	Offset, Max int
}

// List returns a page of the finalized artifacts of a repository, newest
// first, and their total count.
func (s *Service) List(ctx context.Context, repoID uuid.UUID, f ListFilter) ([]Artifact, int64, error) {
	run := uuid.NullUUID{UUID: f.RunID, Valid: f.RunID != uuid.Nil}
	name := pgtype.Text{String: f.Name, Valid: f.Name != ""}
	rows, err := s.q.ListRepoActionsArtifacts(ctx, dbgen.ListRepoActionsArtifactsParams{RepoID: repoID, RunID: run,
		Name: name, Off: int32(min(max(f.Offset, 0), 1<<30)), Lim: int32(min(max(f.Max, 1), 100))}) //nolint:gosec // clamped
	if err != nil {
		return nil, 0, fmt.Errorf("list artifacts: %w", err)
	}
	total, err := s.q.CountRepoActionsArtifacts(ctx, dbgen.CountRepoActionsArtifactsParams{RepoID: repoID, RunID: run, Name: name})
	if err != nil {
		return nil, 0, fmt.Errorf("count artifacts: %w", err)
	}
	now := time.Now()
	out := make([]Artifact, 0, len(rows))
	for _, a := range rows {
		out = append(out, artifactFrom(a, now))
	}
	return out, total, nil
}

func (s *Service) load(ctx context.Context, repoID uuid.UUID, id int64) (dbgen.ActionsArtifact, error) {
	a, err := s.q.GetActionsArtifact(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (a.RepoID != repoID || !a.Finalized)) {
		return dbgen.ActionsArtifact{}, ErrNotFound
	}
	if err != nil {
		return dbgen.ActionsArtifact{}, fmt.Errorf("get artifact: %w", err)
	}
	return a, nil
}

// Get returns an artifact of a repository.
func (s *Service) Get(ctx context.Context, repoID uuid.UUID, id int64) (Artifact, error) {
	a, err := s.load(ctx, repoID, id)
	if err != nil {
		return Artifact{}, err
	}
	return artifactFrom(a, time.Now()), nil
}

// DownloadURL returns a short-lived signed URL of the artifact content
// (a zip for the artifacts of upload-artifact).
func (s *Service) DownloadURL(ctx context.Context, repoID uuid.UUID, id int64) (string, error) {
	a, err := s.load(ctx, repoID, id)
	if err != nil {
		return "", err
	}
	if !a.ExpiresAt.Time.After(time.Now()) {
		return "", ErrNotFound
	}
	return s.signer.URL(blobKind, blobsvc.OpDownload, a.ID, downloadTTL), nil
}

// Delete removes an artifact of a repository with its content.
func (s *Service) Delete(ctx context.Context, repoID uuid.UUID, id int64) error {
	a, err := s.load(ctx, repoID, id)
	if err != nil {
		return err
	}
	return s.remove(ctx, a)
}
