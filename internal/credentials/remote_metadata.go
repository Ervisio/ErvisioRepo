package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// ErvisioCodeRemoteMetadata contains only the last published session metadata.
type ErvisioCodeRemoteMetadata struct {
	SessionID      string    `json:"sessionId"`
	Title          string    `json:"title"`
	LastActivityAt time.Time `json:"lastActivityAt"`
}

// ListErvisioCodeRemoteMetadata returns the snapshots of the account's currently linked devices.
func (s *Service) ListErvisioCodeRemoteMetadata(ctx context.Context, userID uuid.UUID) (map[uuid.UUID][]ErvisioCodeRemoteMetadata, error) {
	rows, err := s.q.ListErvisioCodeRemoteMetadata(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list ErvisioCode Remote metadata: %w", err)
	}
	devices := make(map[uuid.UUID][]ErvisioCodeRemoteMetadata, len(rows))
	for _, row := range rows {
		var metadata []ErvisioCodeRemoteMetadata
		if err := json.Unmarshal(row.RemoteSessionMetadata, &metadata); err != nil {
			return nil, fmt.Errorf("decode ErvisioCode Remote metadata: %w", err)
		}
		devices[row.ID] = metadata
	}
	return devices, nil
}

// RecordErvisioCodeRemoteMetadata updates the device snapshot and normalizes metadata in place.
func (s *Service) RecordErvisioCodeRemoteMetadata(ctx context.Context, userID, tokenID uuid.UUID, metadata []ErvisioCodeRemoteMetadata) error {
	data, err := s.q.GetErvisioCodeRemoteMetadata(ctx, dbgen.GetErvisioCodeRemoteMetadataParams{UserID: userID, TokenID: tokenID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get ErvisioCode Remote metadata: %w", err)
	}
	var previous []ErvisioCodeRemoteMetadata
	if err := json.Unmarshal(data, &previous); err != nil {
		return fmt.Errorf("decode ErvisioCode Remote metadata: %w", err)
	}
	normalizeRemoteMetadata(metadata, previous, time.Now().UTC())
	if slices.Equal(metadata, previous) {
		return nil
	}
	data, err = json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode ErvisioCode Remote metadata: %w", err)
	}
	if err := s.q.SetErvisioCodeRemoteMetadata(ctx, dbgen.SetErvisioCodeRemoteMetadataParams{UserID: userID, TokenID: tokenID, Metadata: data}); err != nil {
		return fmt.Errorf("set ErvisioCode Remote metadata: %w", err)
	}
	return nil
}

func normalizeRemoteMetadata(metadata, previous []ErvisioCodeRemoteMetadata, now time.Time) {
	slices.SortFunc(metadata, func(a, b ErvisioCodeRemoteMetadata) int {
		return strings.Compare(a.SessionID, b.SessionID)
	})
	for i, session := range metadata {
		activity := session.LastActivityAt.UTC()
		if activity.After(now) {
			activity = now
		}
		for _, prior := range previous {
			if prior.SessionID == session.SessionID && activity.Before(prior.LastActivityAt) {
				activity = prior.LastActivityAt
				break
			}
		}
		if activity.IsZero() {
			activity = now
		}
		metadata[i].LastActivityAt = activity
	}
}
