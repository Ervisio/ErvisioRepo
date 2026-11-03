package issues

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// NextNumber allocates the next number of a repository, shared by issues and
// pull requests. q must be bound to the transaction that inserts the issues
// row (kind KindIssue or KindPull): the counter row stays locked until that
// transaction ends, so numbers are gap-free unless it rolls back.
func NextNumber(ctx context.Context, q *dbgen.Queries, repoID uuid.UUID) (int64, error) {
	n, err := q.NextIssueNumber(ctx, repoID)
	if err != nil {
		return 0, fmt.Errorf("allocate issue number: %w", err)
	}
	return n, nil
}
