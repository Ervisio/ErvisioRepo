package main

import (
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/mail"
	"github.com/Ervisio/ErvisioRepo/internal/notify"
	"github.com/Ervisio/ErvisioRepo/internal/webhooks"
)

// setupNotifications builds the outgoing webhook and the notification
// services. Without a master key hooks still work, but cannot be signed.
func setupNotifications(cfg *config.Config, pool *pgxpool.Pool, sender mail.Sender, log *slog.Logger) (*webhooks.Service, *notify.Service, error) {
	key, err := optionalMasterKey(cfg.Security.MasterKeyFile)
	if err != nil {
		return nil, nil, err
	}
	hooks, err := webhooks.New(webhooks.Options{Pool: pool, MasterKey: key, Config: cfg.Webhooks,
		PublicURL: cfg.Server.PublicURL, Log: log.With("component", "webhooks")})
	if err != nil {
		return nil, nil, fmt.Errorf("webhooks: %w", err)
	}
	notifier := notify.New(notify.Options{Pool: pool, PublicURL: cfg.Server.PublicURL, Sender: sender,
		Log: log.With("component", "notify")})
	return hooks, notifier, nil
}
