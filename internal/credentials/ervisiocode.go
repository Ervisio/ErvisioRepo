package credentials

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

type ErvisioCodeDevice struct {
	ID         uuid.UUID
	Name       string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt *time.Time
}

func (s *Service) GetErvisioCodeDevice(ctx context.Context, userID, id uuid.UUID) (ErvisioCodeDevice, error) {
	r, err := s.q.GetErvisioCodeDevice(ctx, dbgen.GetErvisioCodeDeviceParams{UserID: userID, TokenID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErvisioCodeDevice{}, ErrNotFound
	}
	if err != nil {
		return ErvisioCodeDevice{}, fmt.Errorf("get ErvisioCode device: %w", err)
	}
	return ErvisioCodeDevice{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt.Time, ExpiresAt: r.ExpiresAt.Time,
		LastUsedAt: ts(r.LastUsedAt)}, nil
}

func (s *Service) ListErvisioCodeDevices(ctx context.Context, userID uuid.UUID) ([]ErvisioCodeDevice, error) {
	rows, err := s.q.ListErvisioCodeDevices(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list ErvisioCode devices: %w", err)
	}
	out := make([]ErvisioCodeDevice, 0, len(rows))
	for _, r := range rows {
		out = append(out, ErvisioCodeDevice{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt.Time, ExpiresAt: r.ExpiresAt.Time,
			LastUsedAt: ts(r.LastUsedAt)})
	}
	return out, nil
}

func (s *Service) RevokeErvisioCodeDevice(ctx context.Context, userID, id uuid.UUID, ip string) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteErvisioCodeDevice(ctx, dbgen.DeleteErvisioCodeDeviceParams{UserID: userID, TokenID: id})
		if err != nil {
			return fmt.Errorf("delete ErvisioCode device: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: ActionTokenRevoked, TargetType: "token",
			TargetID: id.String(), IP: ip, Data: map[string]any{"client_id": ErvisioCodeClientID}})
	})
}
