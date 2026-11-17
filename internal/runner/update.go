package runner

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
)

// Self-update. The server asks for a version in the Declare/Heartbeat
// answer; the daemon downloads the release asset from the place install.sh
// uses, checks it against the release checksums file exactly like
// install.sh, and stages the binary in <state dir>/bin. On unix the daemon
// then execs the staged binary once the running jobs are done; at every
// start a staged binary newer than the running one takes over (Handoff),
// because the installed one may sit in a read-only /usr/local/bin. On
// Windows the staged binary replaces the executable and the daemon exits
// so the scheduled task restarts it.

const (
	stagedPrefix    = "ervisio-runner-"
	maxAssetSize    = 256 << 20
	maxChecksumSize = 64 << 10
	downloadTimeout = 15 * time.Minute
)

var releaseVersionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// RestartError is returned by Daemon.Run when an update is staged and the
// jobs have finished: the caller restarts on Path (see Restart).
type RestartError struct {
	Path    string
	Version string
}

func (e *RestartError) Error() string {
	return "restarting on ervisio-runner " + e.Version + " (self-update)"
}

// SelfUpdateSupported reports whether this process may replace its own
// binary: not ephemeral and not inside a container (container runners
// follow their image).
func SelfUpdateSupported(ephemeral bool) bool {
	if ephemeral {
		return false
	}
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			return false
		}
	}
	return true
}

// versionKey parses "x.y.z" (optional "v"); ok is false for anything else.
func versionKey(v string) (key [3]int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) != 3 {
		return key, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return key, false
		}
		key[i] = n
	}
	return key, true
}

// newerVersion reports a > b; a development build b is never older.
func newerVersion(a, b string) bool {
	ka, ok1 := versionKey(a)
	kb, ok2 := versionKey(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return ka[i] > kb[i]
		}
	}
	return false
}

// stagedDir is where verified binaries wait for the restart.
func stagedDir(stateFile string) string {
	return filepath.Join(filepath.Dir(stateFile), "bin")
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// Handoff runs at daemon start: when a staged binary newer than current
// exists it replaces this process with it (unix). Older staged binaries
// are removed. It returns only when there is nothing to hand off to, or
// with the version and error of a failed hand-off.
func Handoff(stateFile, current string, log *slog.Logger) (string, error) {
	cleanupReplaced()
	dir := stagedDir(stateFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil
	}
	best, bestPath := "", ""
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), exeSuffix())
		v, ok := strings.CutPrefix(name, stagedPrefix)
		if !ok || e.IsDir() || !releaseVersionRe.MatchString(v) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if !newerVersion(v, current) && v != strings.TrimPrefix(current, "v") {
			if err := os.Remove(p); err == nil {
				log.Info("removed an old staged runner binary", "path", p)
			}
			continue
		}
		if best == "" || newerVersion(v, best) {
			best, bestPath = v, p
		}
	}
	if best == "" || !newerVersion(best, current) || !canExec() {
		return "", nil
	}
	log.Info("handing off to the updated runner binary", "version", best, "path", bestPath)
	if err := execBinary(bestPath); err != nil {
		return best, fmt.Errorf("start %s: %w", filepath.Base(bestPath), err)
	}
	return "", nil
}

// Restart replaces the process with the staged binary (unix) or, on
// Windows where the executable was already replaced, returns an error so
// that the process exits non-zero and the service manager starts it again.
func Restart(re *RestartError) error {
	if !canExec() {
		return re
	}
	if err := execBinary(re.Path); err != nil {
		return fmt.Errorf("start the updated runner: %w", err)
	}
	return nil
}

// stageUpdate downloads, verifies and stages the requested version and
// returns the binary to restart on.
func stageUpdate(ctx context.Context, u *runnerv1.RunnerUpdate, stateFile, userAgent string) (string, error) {
	ver := strings.TrimPrefix(u.GetVersion(), "v")
	if !releaseVersionRe.MatchString(ver) {
		return "", fmt.Errorf("invalid update version %.40q", u.GetVersion())
	}
	base, err := checkDownloadURL(u.GetDownloadUrl())
	if err != nil {
		return "", err
	}
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	asset := fmt.Sprintf("ervisio-runner_%s_%s_%s%s", ver, runtime.GOOS, runtime.GOARCH, ext)
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	client := &http.Client{}

	dir := stagedDir(stateFile)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	var sums strings.Builder
	if err := fetchTo(ctx, client, base+"/ervisio-runner_"+ver+"_checksums.txt", userAgent, maxChecksumSize, &sums); err != nil {
		return "", fmt.Errorf("checksum download failed: %w", err)
	}
	want := checksumOf(sums.String(), asset)
	if want == "" {
		return "", fmt.Errorf("%s is not listed in the checksums file", asset)
	}

	archive, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", fmt.Errorf("temporary file: %w", err)
	}
	defer func() {
		_ = archive.Close()
		_ = os.Remove(archive.Name())
	}()
	h := sha256.New()
	if err := fetchTo(ctx, client, base+"/"+asset, userAgent, maxAssetSize, io.MultiWriter(archive, h)); err != nil {
		return "", fmt.Errorf("download of %s failed: %w", asset, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("checksum mismatch for %s", asset)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("read %s: %w", asset, err)
	}

	bin, err := os.CreateTemp(dir, ".binary-*")
	if err != nil {
		return "", fmt.Errorf("temporary file: %w", err)
	}
	defer func() {
		_ = bin.Close()
		_ = os.Remove(bin.Name())
	}()
	if runtime.GOOS == "windows" {
		err = extractZip(archive, "ervisio-runner.exe", bin)
	} else {
		err = extractTarGz(archive, "ervisio-runner", bin)
	}
	if err != nil {
		return "", fmt.Errorf("unpack %s: %w", asset, err)
	}
	if err := bin.Sync(); err != nil {
		return "", fmt.Errorf("write binary: %w", err)
	}
	if err := bin.Close(); err != nil {
		return "", fmt.Errorf("write binary: %w", err)
	}
	if err := os.Chmod(bin.Name(), 0o755); err != nil { //nolint:gosec // an executable
		return "", fmt.Errorf("chmod binary: %w", err)
	}
	staged := filepath.Join(dir, stagedPrefix+ver+exeSuffix())
	if err := os.Rename(bin.Name(), staged); err != nil {
		return "", fmt.Errorf("stage binary: %w", err)
	}
	return installStaged(staged)
}

// checkDownloadURL accepts an https base URL without credentials.
func checkDownloadURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the update download URL must be an https URL")
	}
	return u.String(), nil
}

// fetchTo GETs rawURL into w, refusing bodies larger than limit.
func fetchTo(ctx context.Context, c *http.Client, rawURL, userAgent string, limit int64, w io.Writer) error {
	name := path.Base(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("get %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("get %s: HTTP %d", name, resp.StatusCode)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if n > limit {
		return fmt.Errorf("%s is too large", name)
	}
	return nil
}

// checksumOf finds the sha256 of file in a "<hash>  <file>" list, the
// same lookup install.sh does with awk ($2 == f || $2 == "*" f).
func checksumOf(sums, file string) string {
	sc := bufio.NewScanner(strings.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && (f[1] == file || f[1] == "*"+file) {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

func extractTarGz(r io.Reader, name string, w io.Writer) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s not found in the archive", name)
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if h.Typeflag != tar.TypeReg || path.Clean(h.Name) != name {
			continue
		}
		if _, err := io.Copy(w, io.LimitReader(tr, maxAssetSize)); err != nil {
			return fmt.Errorf("extract %s: %w", name, err)
		}
		return nil
	}
}

func extractZip(f *os.File, name string, w io.Writer) error {
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat archive: %w", err)
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	for _, zf := range zr.File {
		if path.Clean(zf.Name) != name {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("extract %s: %w", name, err)
		}
		_, err = io.Copy(w, io.LimitReader(rc, maxAssetSize))
		_ = rc.Close()
		if err != nil {
			return fmt.Errorf("extract %s: %w", name, err)
		}
		return nil
	}
	return fmt.Errorf("%s not found in the archive", name)
}
