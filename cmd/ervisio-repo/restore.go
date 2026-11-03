package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Ervisio/ErvisioRepo/internal/backup"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

// restoreCmd rebuilds an instance from its backups (PLAN §4.5): it restores
// the latest (or the given) PostgreSQL dump of the "backup" class, then
// aligns the repositories with the remote git journal. Run it with the
// server stopped, after `recovery-kit import` on a new machine.
//
//	ervisio-repo restore [--config <path>] [--dump <key>] [--skip-db]
func restoreCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", defaultConfig, "path to the TOML configuration file")
	dumpKey := fs.String("dump", "", "dump key to restore (default: the latest)")
	skipDB := fs.Bool("skip-db", false, "keep the database (restored by other means, e.g. wal-g) and only reconcile")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: unexpected arguments %v", errUsage, fs.Args())
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	o, err := openOffline(ctx, cfg, stderr)
	if err != nil {
		return err
	}
	defer o.close()

	if !*skipDB {
		class, err := o.storage.Class(backup.Policy)
		if err != nil {
			return fmt.Errorf("restore: %w", err)
		}
		key := *dumpKey
		if key == "" {
			dumps, err := backup.ListDumps(ctx, class)
			if err != nil {
				return err
			}
			d, ok := backup.Latest(dumps)
			if !ok {
				return errors.New("restore: no dump in the backup class")
			}
			key = d.Key
		}
		fmt.Fprintf(stdout, "restoring %s\n", key)
		rc, err := backup.OpenDump(ctx, class, key)
		if err != nil {
			return err
		}
		err = backup.RestoreDatabase(ctx, rc, cfg.Database.URL, o.log)
		_ = rc.Close()
		if err != nil {
			return err
		}
		// The dump may predate the running binary.
		if err := db.Migrate(ctx, o.pool, o.log); err != nil {
			return err
		}
	}

	if _, ok := cfg.Storage.Policy[gitstore.Policy]; !ok {
		return nil
	}
	store, err := gitstore.NewStore(o.storage)
	if err != nil {
		return err
	}
	rep, err := backup.ReconcileRepos(ctx, o.pool, store, o.log)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d repositories cold, %d journal objects re-indexed, %d degraded\n",
		rep.Repos-len(rep.Degraded), rep.Reindexed, len(rep.Degraded))
	for _, id := range rep.Degraded {
		fmt.Fprintf(stdout, "degraded: %s\n", id)
	}
	for _, id := range rep.Orphans {
		fmt.Fprintf(stdout, "orphan journal (no repository row): %s\n", id)
	}
	return nil
}
