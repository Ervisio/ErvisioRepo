package updates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Files shared with the updater container (deploy/docker/updater.sh) in
// updates.dir. Only the server writes request.json; only the updater writes
// the others.
const (
	fileRequest = "request.json"
	fileStatus  = "status.json"
	fileLog     = "update.log"
	fileAlive   = "alive"
)

const (
	// aliveWithin: the updater touches fileAlive every few seconds; older
	// than this it is considered stopped.
	aliveWithin = 90 * time.Second
	// maxStatusFile and maxLogTail bound what is read from the folder.
	maxStatusFile = 64 << 10
	maxLogTail    = 64 << 10
	logLines      = 200
)

// Updater states written by updater.sh.
const (
	StateIdle    = "idle"
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"
)

// UpdaterState is what the updater container reports.
type UpdaterState struct {
	// Available: the updater container is running (recent heartbeat).
	Available bool
	// State is idle, running, done or failed; empty when it never ran.
	State      string
	Version    string
	From       string
	StartedAt  *time.Time
	FinishedAt *time.Time
	// Step is the last "==> step" marker of the log: backup, fetch, pull, up, done.
	Step string
	// Log is the end of the log of the last update.
	Log []string
	// Pending is the version of a request the updater has not taken yet.
	Pending string
}

type statusFile struct {
	State      string `json:"state"`
	Version    string `json:"version"`
	From       string `json:"from"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

type requestFile struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	RequestedAt string `json:"requestedAt"`
	By          string `json:"by"`
}

// readUpdater reads the folder shared with the updater. A missing folder
// or file is an updater that never ran, not an error.
func (s *Service) readUpdater() UpdaterState {
	var st UpdaterState
	dir := s.o.Dir
	if dir == "" {
		return st
	}
	if fi, err := os.Stat(filepath.Join(dir, fileAlive)); err == nil {
		st.Available = s.o.Now().Sub(fi.ModTime()) < aliveWithin
	}
	if b, err := readCapped(filepath.Join(dir, fileStatus), maxStatusFile); err == nil {
		var f statusFile
		if json.Unmarshal(b, &f) == nil {
			st.State, st.Version, st.From = f.State, Normalize(f.Version), Normalize(f.From)
			st.StartedAt, st.FinishedAt = parseTime(f.StartedAt), parseTime(f.FinishedAt)
		}
	}
	if b, err := readCapped(filepath.Join(dir, fileRequest), maxStatusFile); err == nil {
		var r requestFile
		if json.Unmarshal(b, &r) == nil {
			st.Pending = Normalize(r.Version)
		}
	}
	if tail, err := readTail(filepath.Join(dir, fileLog), maxLogTail); err == nil {
		lines := strings.Split(strings.TrimRight(strings.ToValidUTF8(string(tail), "�"), "\n"), "\n")
		for _, l := range lines {
			if step, ok := strings.CutPrefix(strings.TrimSpace(l), "==> "); ok {
				st.Step, _, _ = strings.Cut(step, " ")
			}
		}
		if len(lines) > logLines {
			lines = lines[len(lines)-logLines:]
		}
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
		st.Log = lines
	}
	return st
}

// writeRequest hands an update to the updater: write a temporary file and
// rename it, so the updater never reads half a request.
func (s *Service) writeRequest(r requestFile) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode update request: %w", err)
	}
	tmp, err := os.CreateTemp(s.o.Dir, ".request-*.json")
	if err != nil {
		return fmt.Errorf("create update request: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write update request: %w", err)
	}
	if err := os.Chmod(name, 0o600); err != nil { // the updater reads it as root
		_ = os.Remove(name)
		return fmt.Errorf("write update request: %w", err)
	}
	if err := os.Rename(name, filepath.Join(s.o.Dir, fileRequest)); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write update request: %w", err)
	}
	return nil
}

func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // fixed names inside the configured updater folder
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, limit))
}

// readTail returns at most limit bytes from the end of the file, starting
// at a line boundary.
func readTail(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // fixed names inside the configured updater folder
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := max(fi.Size()-limit, 0)
	b := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(b, off); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if off > 0 {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return b, nil
}

func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
