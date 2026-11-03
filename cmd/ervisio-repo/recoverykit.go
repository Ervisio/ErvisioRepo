package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/backup"
	"github.com/Ervisio/ErvisioRepo/internal/config"
)

// recoveryKitCmd exports or imports the recovery kit (PLAN §4.5): the
// configuration, the master key and the SSH host keys, sealed with a
// passphrase (argon2id + AES-256-GCM).
//
//	ervisio-repo recovery-kit export --out <file> [--config <path>] [--passphrase-file <f>]
//	ervisio-repo recovery-kit import --in <file> [--root <dir>] [--force] [--passphrase-file <f>]
func recoveryKitCmd(_ context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "export" && args[0] != "import") {
		return fmt.Errorf("%w: recovery-kit needs \"export\" or \"import\"", errUsage)
	}
	sub := args[0]
	fs := flag.NewFlagSet("recovery-kit "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	passFile := fs.String("passphrase-file", "", "file holding the passphrase (default: read a line from stdin)")
	var path, file, root *string
	var force *bool
	if sub == "export" {
		path = fs.String("config", defaultConfig, "path to the TOML configuration file")
		file = fs.String("out", "", "kit file to write")
	} else {
		file = fs.String("in", "", "kit file to read")
		root = fs.String("root", "", "install under this directory instead of the recorded paths")
		force = fs.Bool("force", false, "overwrite existing files with different content")
	}
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 || *file == "" {
		return fmt.Errorf("%w: recovery-kit %s needs %s <file>", errUsage, sub, map[string]string{"export": "--out", "import": "--in"}[sub])
	}
	pass, err := readPassphrase(*passFile, stdin, stderr)
	if err != nil {
		return err
	}
	if sub == "export" {
		return exportKit(*path, *file, pass, stdout)
	}
	f, err := os.Open(*file) //nolint:gosec // operator-chosen kit file
	if err != nil {
		return fmt.Errorf("open kit: %w", err)
	}
	defer f.Close()
	kit, err := backup.OpenKit(f, pass)
	if err != nil {
		return err
	}
	written, err := backup.InstallKit(kit, *root, *force)
	for _, p := range written {
		fmt.Fprintln(stdout, p)
	}
	return err
}

func exportKit(configPath, out string, pass []byte, stdout io.Writer) error {
	if len(pass) < backup.MinPassphrase {
		return fmt.Errorf("the passphrase must be at least %d characters", backup.MinPassphrase)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("config path: %w", err)
	}
	src := backup.KitSources{ConfigFile: abs, MasterKeyFile: cfg.Security.MasterKeyFile}
	if cfg.SSH.Enabled {
		src.HostKeyDir = cfg.SSH.HostKeyDir
	}
	kit, err := backup.CollectKit(src, time.Now())
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := backup.SealKit(&buf, kit, pass); err != nil {
		return err
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil { //nolint:gosec // operator-chosen kit file
		return fmt.Errorf("write kit: %w", err)
	}
	for _, f := range kit.Files() {
		fmt.Fprintln(stdout, f.Path)
	}
	return nil
}

// readPassphrase reads the passphrase from path, or a line from stdin.
func readPassphrase(path string, stdin io.Reader, stderr io.Writer) ([]byte, error) {
	var raw []byte
	if path != "" {
		b, err := os.ReadFile(path) //nolint:gosec // operator-chosen passphrase file
		if err != nil {
			return nil, fmt.Errorf("read passphrase: %w", err)
		}
		raw = b
	} else {
		fmt.Fprint(stderr, "Passphrase: ")
		line, err := bufio.NewReader(stdin).ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read passphrase: %w", err)
		}
		raw = line
	}
	pass := bytes.TrimRight(raw, "\r\n")
	if len(pass) == 0 {
		return nil, errors.New("empty passphrase")
	}
	return pass, nil
}
