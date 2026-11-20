package credentials

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

type ErvisioCodeDevice struct {
	ID           uuid.UUID
	Name         string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	LastUsedAt   *time.Time
	RemoteOnline *bool
}

const ErvisioCodeHubSessionTTL = 15 * time.Minute

func (s *Service) CreateErvisioCodeHubSession(ctx context.Context, userID, tokenID uuid.UUID) (string, error) {
	secret, hash := auth.NewToken()
	n, err := s.q.SetErvisioCodeHubSession(ctx, dbgen.SetErvisioCodeHubSessionParams{
		UserID: userID, TokenID: tokenID, SessionHash: hash, TtlSeconds: int32(ErvisioCodeHubSessionTTL / time.Second),
	})
	if err != nil {
		return "", fmt.Errorf("set ErvisioCode hub session: %w", err)
	}
	if n == 0 {
		return "", ErrNotFound
	}
	return secret, nil
}

func (s *Service) GetErvisioCodeHubSession(ctx context.Context, userID uuid.UUID, secret string) (string, error) {
	if !auth.ValidTokenFormat(secret) {
		return "", ErrNotFound
	}
	username, err := s.q.GetErvisioCodeHubSession(ctx, dbgen.GetErvisioCodeHubSessionParams{
		UserID: userID, SessionHash: auth.HashToken(secret),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get ErvisioCode hub session: %w", err)
	}
	return username, nil
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
		device := ErvisioCodeDevice{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt.Time, ExpiresAt: r.ExpiresAt.Time,
			LastUsedAt: ts(r.LastUsedAt)}
		if r.RemoteLastSeenAt.Valid {
			device.RemoteOnline = &r.RemoteOnline
		}
		out = append(out, device)
	}
	return out, nil
}

func (s *Service) SetErvisioCodeRemotePresence(ctx context.Context, userID, id uuid.UUID, active bool) error {
	n, err := s.q.SetErvisioCodeRemotePresence(ctx, dbgen.SetErvisioCodeRemotePresenceParams{
		UserID: userID, TokenID: id, Active: active,
	})
	if err != nil {
		return fmt.Errorf("set ErvisioCode remote presence: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
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
