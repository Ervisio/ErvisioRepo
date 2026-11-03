// Package db owns the PostgreSQL connection pool, the embedded schema
// migrations (ours via goose, River's via rivermigrate) and the generated
// queries in dbgen.
package db

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open creates a connection pool and checks that the database is reachable.
func Open(ctx context.Context, cfg config.Database) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if cfg.MaxConns > 0 {
		pcfg.MaxConns = cfg.MaxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// SettingInstanceID is the settings key holding the instance UUID.
const SettingInstanceID = "instance.id"

// EnsureInstanceID returns the instance UUID, generating a UUID v7 on first
// start. Concurrent callers always observe the same value.
func EnsureInstanceID(ctx context.Context, q *dbgen.Queries) (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate instance id: %w", err)
	}
	value, err := json.Marshal(id.String())
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode instance id: %w", err)
	}
	if err := q.InsertSettingIfAbsent(ctx, dbgen.InsertSettingIfAbsentParams{Key: SettingInstanceID, Value: value}); err != nil {
		return uuid.Nil, fmt.Errorf("store instance id: %w", err)
	}
	row, err := q.GetSetting(ctx, SettingInstanceID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("load instance id: %w", err)
	}
	var stored string
	if err := json.Unmarshal(row.Value, &stored); err != nil {
		return uuid.Nil, fmt.Errorf("decode instance id: %w", err)
	}
	parsed, err := uuid.Parse(stored)
	if err != nil {
		return uuid.Nil, fmt.Errorf("decode instance id: %w", err)
	}
	return parsed, nil
}
