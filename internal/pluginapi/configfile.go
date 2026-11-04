package pluginapi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ConfigFile is the TOML file the settings are persisted in.
type ConfigFile interface {
	Path() string
	Read() ([]byte, error)
	// Write replaces the file atomically.
	Write(data []byte) error
}

// FileConfig is a ConfigFile on the local disk. This is the server's own
// configuration, not user data, so it does not go through internal/storage.
func FileConfig(path string) ConfigFile { return fileConfig(path) }

type fileConfig string

func (f fileConfig) Path() string { return string(f) }

func (f fileConfig) Read() ([]byte, error) {
	data, err := os.ReadFile(string(f))
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return data, nil
}

// errReadOnly is returned when the file or its folder cannot be written, for
// example the config folder mounted read-only in the Docker stack.
var errReadOnly = errors.New("the configuration file is not writable")

func (f fileConfig) Write(data []byte) error {
	dir := filepath.Dir(string(f))
	mode := os.FileMode(0o640)
	if st, err := os.Stat(string(f)); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return fmt.Errorf("%w: %w", errReadOnly, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(name, string(f)); err != nil {
		return fmt.Errorf("%w: %w", errReadOnly, err)
	}
	return nil
}
