package issues

import (
	"context"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Hydrate turns issue rows into Issues (labels, assignees, milestone,
// reactions as seen by viewer, user names). The pull request service uses
// it for the issue part of its pull requests.
func (s *Service) Hydrate(ctx context.Context, viewer uuid.UUID, rows []dbgen.Issue) ([]Issue, error) {
	return s.hydrate(ctx, viewer, rows)
}
