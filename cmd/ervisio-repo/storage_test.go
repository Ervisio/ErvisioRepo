package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/config"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	toml := "[server]\npublic_url = \"http://localhost:3400\"\n[database]\nurl = \"postgres://x@localhost/x\"\n" + body
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStorageTestCommand(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, "[[storage.target]]\nname = \"disk\"\nkind = \"local\"\npath = \""+dir+"\"\n")

	for _, args := range [][]string{
		{"storage", "test", "disk", "--config", path},
		{"storage", "test", "--config", path, "disk"},
	} {
		code, out, errOut := runCLI(args...)
		if code != 0 || !strings.Contains(out, `storage target "disk" ok`) || !strings.Contains(out, "delete") {
			t.Fatalf("%v: code=%d out=%q err=%q", args, code, out, errOut)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "erepo-storage-test")); len(entries) != 0 {
		t.Fatalf("probe left behind: %v", entries)
	}

	tests := []struct {
		args     []string
		code     int
		inStderr string
	}{
		{[]string{"storage"}, 2, `storage needs "test <target>"`},
		{[]string{"storage", "test", "--config", path}, 2, "needs a target name"},
		{[]string{"storage", "test", "disk", "extra", "--config", path}, 2, "unexpected arguments"},
		{[]string{"storage", "test", "--config", path, "disk", "extra"}, 2, "unexpected arguments"},
		{[]string{"storage", "test", "nope", "--config", path}, 1, `unknown target "nope"`},
	}
	for _, tt := range tests {
		code, _, errOut := runCLI(tt.args...)
		if code != tt.code || !strings.Contains(errOut, tt.inStderr) {
			t.Errorf("%v: code=%d stderr=%q", tt.args, code, errOut)
		}
	}
}

func TestStorageTestFailure(t *testing.T) {
	// A regular file where the directory should be: every write fails.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, "[[storage.target]]\nname = \"disk\"\nkind = \"local\"\npath = \""+file+"\"\n")
	code, out, errOut := runCLI("storage", "test", "disk", "--config", path)
	if code == 0 || (!strings.Contains(out, "FAIL") && errOut == "") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestParseMasterKey(t *testing.T) {
	key := bytes.Repeat([]byte{0xab}, 32)
	for name, in := range map[string][]byte{
		"hex":    []byte(hex.EncodeToString(key) + "\n"),
		"base64": []byte(base64.StdEncoding.EncodeToString(key) + "\n"),
	} {
		got, err := parseMasterKey(in)
		if err != nil || !bytes.Equal(got, key) {
			t.Errorf("%s: %x %v", name, got, err)
		}
	}
	for _, bad := range []string{"", "short", string(key), hex.EncodeToString(key[:16]), strings.Repeat("z", 64)} {
		if _, err := parseMasterKey([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLoadMasterKey(t *testing.T) {
	cfg := &config.Config{}
	if k, err := loadMasterKey(cfg); err != nil || k != nil {
		t.Fatalf("no encrypted target: %x %v", k, err)
	}
	cfg.Storage.Targets = []config.Target{{Name: "s3", Kind: "s3", Encrypt: true}}
	if _, err := loadMasterKey(cfg); err == nil {
		t.Fatal("missing key file accepted")
	}
	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, []byte(strings.Repeat("01", 32)), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Security.MasterKeyFile = path
	if _, err := loadMasterKey(cfg); err == nil || !strings.Contains(err.Error(), "group or others") {
		t.Fatalf("world-readable key accepted: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := loadMasterKey(cfg)
	if err != nil || len(k) != 32 || k[0] != 1 {
		t.Fatalf("key = %x, %v", k, err)
	}
	if err != nil && strings.Contains(err.Error(), strings.Repeat("01", 32)) {
		t.Fatal("key material in error")
	}
}
