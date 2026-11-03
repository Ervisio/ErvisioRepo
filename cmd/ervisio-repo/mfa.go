package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/auth/mfa"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

// setupMFA builds the second-factor service. TOTP secrets are sealed with
// security.master_key_file when one is configured.
func setupMFA(cfg *config.Config, pool *pgxpool.Pool, ids *identity.Stack, log *slog.Logger) (*mfa.Service, error) {
	key, err := optionalMasterKey(cfg.Security.MasterKeyFile)
	if err != nil {
		return nil, err
	}
	return mfa.New(mfa.Options{
		Pool: pool, Identity: ids.Service, Limiter: ids.Limiter,
		MasterKey: key, Config: cfg, Log: log,
	})
}

// optionalMasterKey reads the master key file with the same permission
// check as the storage key. A missing file means no key: mfa.New then warns
// that TOTP secrets are stored unsealed.
func optionalMasterKey(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path) //nolint:gosec // operator-configured key file, not request data
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("master key: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("master key: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("master key: %s must not be accessible by group or others (mode %04o)", path, fi.Mode().Perm())
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1024))
	if err != nil {
		return nil, fmt.Errorf("master key: %w", err)
	}
	key, err := parseMasterKey(raw)
	if err != nil {
		return nil, fmt.Errorf("master key %s: %w", path, err)
	}
	return key, nil
}
