package codeshare

import (
	"context"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

type DirectoryOrg struct {
	ID          uuid.UUID
	Name        string
	DisplayName string
	AvatarURL   string
}

func (s *Service) DirectoryRole(ctx context.Context, accountID uuid.UUID) (string, error) {
	role, err := s.q.GetErvisioCodeDirectoryRole(ctx, accountID)
	return role, queryError("read sharing directory role", err)
}

func (s *Service) SearchGrantOrgs(ctx context.Context, accountID uuid.UUID, prefix string, limit int32) ([]DirectoryOrg, error) {
	rows, err := s.q.SearchErvisioCodeGrantOrgs(ctx, dbgen.SearchErvisioCodeGrantOrgsParams{
		AccountID: accountID, Prefix: prefix, PageSize: limit,
	})
	if err != nil {
		return nil, queryError("search sharing organizations", err)
	}
	out := make([]DirectoryOrg, 0, len(rows))
	for _, row := range rows {
		out = append(out, DirectoryOrg{ID: row.ID, Name: row.Name, DisplayName: row.DisplayName, AvatarURL: row.AvatarUrl})
	}
	return out, nil
}
