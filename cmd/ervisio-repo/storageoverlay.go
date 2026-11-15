package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/storagecfg"
)

// overlayCmd runs `storage overlay show|clear [--config <path>] [--force]`:
// inspect or remove the storage configuration saved from the web admin. It is
// the recovery path when the server refuses to start because of it.
func overlayCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "show" && args[0] != "clear") {
		return fmt.Errorf("%w: storage overlay needs \"show\" or \"clear\"", errUsage)
	}
	sub := args[0]
	fs := flag.NewFlagSet("storage overlay "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", defaultConfig, "path to the TOML configuration file")
	force := fs.Bool("force", false, "clear even when it moves the primary of a policy that holds objects")
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: unexpected arguments %v", errUsage, fs.Args())
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	q := dbgen.New(pool)

	doc, err := storagecfg.Load(ctx, q)
	if err != nil && !(sub == "clear" && *force && errors.Is(err, storagecfg.ErrCorrupt)) {
		return err
	}
	counts, err := q.StorageObjectCountsByTarget(ctx)
	if err != nil {
		return fmt.Errorf("count objects: %w", err)
	}
	if sub == "show" {
		printOverlay(stdout, doc, cfg.Storage, counts)
		return nil
	}

	if blockers := storagecfg.ClearBlockers(doc, cfg.Storage, counts); len(blockers) > 0 && !*force {
		for _, b := range blockers {
			fmt.Fprintln(stderr, "refusing:", b)
		}
		return errors.New("clearing the overlay would move the primary of a policy that holds objects; use --force only if you know they are still findable under the file primary")
	}
	if err := q.UpsertSetting(ctx, dbgen.UpsertSettingParams{Key: storagecfg.SettingKey, Value: []byte(`{"version":1}`)}); err != nil {
		return fmt.Errorf("clear storage overlay: %w", err)
	}
	fmt.Fprintln(stdout, "storage overlay cleared: the next start uses config.toml only")
	return nil
}

// printOverlay lists the stored overlay without any credential.
func printOverlay(w io.Writer, doc storagecfg.Doc, file config.Storage, counts []dbgen.StorageObjectCountsByTargetRow) {
	if len(doc.Targets) == 0 && len(doc.Policies) == 0 {
		fmt.Fprintln(w, "no storage overlay stored")
		return
	}
	fmt.Fprintln(w, "targets:")
	for _, t := range doc.Targets {
		where := t.Endpoint + "/" + t.Bucket + t.Prefix
		if t.Kind == storagecfg.KindSFTP {
			where = fmt.Sprintf("%s@%s:%d%s", t.User, t.Host, t.Port, t.Root)
		}
		fields := slices.Sorted(maps.Keys(t.Secrets))
		fmt.Fprintf(w, "  %s  %s  %s  encrypt=%t  credentials=[%s]\n", t.Name, t.Kind, where, t.Encrypt, strings.Join(fields, ","))
	}
	fmt.Fprintln(w, "policy overrides:")
	for _, name := range slices.Sorted(maps.Keys(doc.Policies)) {
		p := doc.Policies[name]
		var n int64
		for _, c := range counts {
			if c.Policy == name {
				n += c.Objects
			}
		}
		raw, _ := json.Marshal(p.Replicas)
		fmt.Fprintf(w, "  %s  primary=%s (file: %s)  replicas=%s  cache=%q  objects=%d\n",
			name, p.Primary, file.Policy[name].Primary, raw, p.Cache, n)
	}
}
