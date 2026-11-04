// Package runner implements the ervisio-runner daemon: registration, the
// task polling loop and job execution with act.
package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// State is the registration saved by "register" and read by "daemon".
type State struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	Credential string   `json:"credential"`
	Labels     []string `json:"labels"`
	Ephemeral  bool     `json:"ephemeral"`
}

// LoadState reads the state file.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("state file %s not found, run \"ervisio-runner register\" first", path)
		}
		return nil, fmt.Errorf("read state file: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse state file %s: %w", path, err)
	}
	if st.Credential == "" || st.URL == "" {
		return nil, fmt.Errorf("state file %s is incomplete, register the runner again", path)
	}
	return &st, nil
}

// SaveState writes the state file atomically with mode 0600.
func SaveState(path string, st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	f, err := os.CreateTemp(dir, ".runner-*")
	if err != nil {
		return fmt.Errorf("create state file: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("chmod state file: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("write state file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync state file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close state file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("install state file: %w", err)
	}
	return nil
}
