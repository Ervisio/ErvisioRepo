package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/crypt"
	"github.com/Ervisio/ErvisioRepo/internal/storage/storagecfg"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// storageCmd runs `storage test <target> [--config <path>]`.
func storageCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "overlay" {
		return overlayCmd(ctx, args[1:], stdout, stderr)
	}
	if len(args) == 0 || args[0] != "test" {
		return fmt.Errorf("%w: storage needs \"test <target>\" or \"overlay show|clear\"", errUsage)
	}
	args = args[1:]
	// Accept the target before or after the flags.
	var target string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("storage test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", defaultConfig, "path to the TOML configuration file")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall time limit of the test")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	rest := fs.Args()
	if target == "" && len(rest) > 0 {
		target, rest = rest[0], rest[1:]
	}
	if target == "" {
		return fmt.Errorf("%w: storage test needs a target name", errUsage)
	}
	if len(rest) > 0 {
		return fmt.Errorf("%w: unexpected arguments %v", errUsage, rest)
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	var tc *config.Target
	for i := range cfg.Storage.Targets {
		if cfg.Storage.Targets[i].Name == target {
			tc = &cfg.Storage.Targets[i]
		}
	}
	if tc == nil {
		return fmt.Errorf("%w %q", tier.ErrUnknownTarget, target)
	}
	key, err := loadMasterKey(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	log := newLogger(cfg.Log, stderr)
	store, err := tier.BuildStore(ctx, *tc, tier.BuildOptions{MasterKey: key, Log: log})
	if err != nil {
		return err
	}
	defer tier.CloseStores(map[string]blob.Store{target: store})

	res := tier.CheckStore(ctx, store)
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "STEP\tRESULT\tDURATION\tERROR")
	for _, s := range res.Steps {
		state := "ok"
		if !s.OK {
			state = "FAIL"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, state, s.Duration.Round(time.Millisecond), s.Error)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("storage target %q failed the test", target)
	}
	fmt.Fprintf(stdout, "storage target %q ok (%s)\n", target, res.Duration.Round(time.Millisecond))
	return nil
}

// loadMasterKey reads security.master_key_file, only when a target needs
// encryption. The file holds 32 bytes as hex or base64 text, and must
// not be readable by group or others.
func loadMasterKey(cfg *config.Config) ([]byte, error) {
	need := false
	for _, t := range cfg.Storage.Targets {
		need = need || t.Encrypt
	}
	if !need {
		return nil, nil
	}
	path := cfg.Security.MasterKeyFile
	if path == "" {
		return nil, errors.New("master key: security.master_key_file is required by encrypted targets")
	}
	f, err := os.Open(path) //nolint:gosec // operator-configured key file, not request data
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

func parseMasterKey(raw []byte) ([]byte, error) { return crypt.ParseMasterKey(raw) }

// storageDB is what openStorage reads and writes: the tier's object rows and
// the settings table that holds the storage overlay of the web admin.
type storageDB interface {
	tier.Queries
	storagecfg.Getter
}

// storageBoot describes the storage configuration the tier was built with.
type storageBoot struct {
	cfg  *config.Config // the configuration with the storage overlay applied
	file config.Storage // the storage section of config.toml, without the overlay
	doc  storagecfg.Doc // the overlay that is part of cfg (empty when none applies)
	// stored is the overlay found in the database, applied or not; fallback
	// and cause say that it was not applied, and why.
	stored   storagecfg.Doc
	fallback bool
	cause    string
	key  []byte         // the master key, nil when there is no key file
}

// openStorage merges the storage overlay of the web admin over the file
// configuration, then builds the stores and the tier for serve and for the
// offline commands. The caller closes the stores after the tier is no longer
// used. If the overlay cannot be used and moves no primary, the file
// configuration is used with a warning; otherwise it fails (see storagecfg.Apply).
func openStorage(ctx context.Context, cfg *config.Config, q storageDB, reg prometheus.Registerer,
	log *slog.Logger, onStored func(context.Context, tier.ObjectRef) error,
) (*tier.Tier, map[string]blob.Store, *storageBoot, error) {
	key, err := optionalMasterKey(cfg.Security.MasterKeyFile)
	if err != nil {
		return nil, nil, nil, err
	}
	file := cfg.Storage
	res, err := storagecfg.Apply(ctx, q, key, file, log)
	if err != nil {
		return nil, nil, nil, err
	}
	eff := *cfg
	eff.Storage = res.Storage
	t, stores, err := buildTier(ctx, &eff, q, reg, log, onStored)
	if err != nil && !res.Fallback && len(res.Doc.Targets)+len(res.Doc.Policies) > 0 {
		// The merged configuration did not open: same rule as for a merge error.
		fres, ferr := storagecfg.Fallback(res, file, err, log)
		if ferr != nil {
			return nil, nil, nil, ferr
		}
		res, eff.Storage = fres, file
		t, stores, err = buildTier(ctx, &eff, q, reg, log, onStored)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	return t, stores, &storageBoot{cfg: &eff, file: file, doc: res.Doc, stored: res.Stored, fallback: res.Fallback, cause: res.Cause, key: key}, nil
}

// buildTier opens the stores of cfg.Storage and the tier on them.
func buildTier(ctx context.Context, cfg *config.Config, q tier.Queries, reg prometheus.Registerer,
	log *slog.Logger, onStored func(context.Context, tier.ObjectRef) error,
) (*tier.Tier, map[string]blob.Store, error) {
	key, err := loadMasterKey(cfg)
	if err != nil {
		return nil, nil, err
	}
	stores, err := tier.BuildStores(ctx, cfg.Storage, tier.BuildOptions{MasterKey: key, Registerer: reg, Log: log})
	if err != nil {
		return nil, nil, err
	}
	t, err := tier.New(tier.Options{Config: cfg.Storage, Stores: stores, Queries: q, Log: log, OnStored: onStored})
	if err != nil {
		tier.CloseStores(stores)
		return nil, nil, err
	}
	if err := t.LoadCaches(ctx); err != nil {
		tier.CloseStores(stores)
		return nil, nil, err
	}
	for _, w := range tier.Warnings(cfg.Storage) {
		log.Warn("storage configuration", "code", w.Code, "policy", w.Policy, "message", w.Message)
	}
	return t, stores, nil
}
