package repos

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// StorageFootprints returns the logical payload size for each authorized repository ID.
func (s *Service) StorageFootprints(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.q.ListRepoStorageFootprints(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list repository storage footprints: %w", err)
	}
	sizes := make(map[uuid.UUID]int64, len(rows))
	for _, r := range rows {
		sizes[r.ID] = r.TotalSizeBytes
	}
	return sizes, nil
}
