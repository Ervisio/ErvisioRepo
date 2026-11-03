package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionalMasterKey(t *testing.T) {
	dir := t.TempDir()
	if k, err := optionalMasterKey(filepath.Join(dir, "missing.key")); err != nil || k != nil {
		t.Fatalf("missing file: %v %v", k, err)
	}
	path := filepath.Join(dir, "master.key")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(make([]byte, 32))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if k, err := optionalMasterKey(path); err != nil || len(k) != 32 {
		t.Fatalf("valid key: %d %v", len(k), err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := optionalMasterKey(path); err == nil || !strings.Contains(err.Error(), "group or others") {
		t.Fatalf("readable key accepted: %v", err)
	}
	bad := filepath.Join(dir, "bad.key")
	if err := os.WriteFile(bad, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := optionalMasterKey(bad); err == nil {
		t.Fatal("malformed key accepted")
	}
}
