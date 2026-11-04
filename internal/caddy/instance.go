package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

// Kinds of Caddy instance.
const (
	KindSystem = "system"
	KindDocker = "docker"
)

// Unsupported reasons: why this package will not edit a Caddy (the snippet to
// paste by hand is still shown).
const (
	// UnsupportedJSON: Caddy runs from a JSON config (or --resume).
	UnsupportedJSON = "json"
	// UnsupportedPath: the system Caddyfile is outside /etc/caddy.
	UnsupportedPath = "path"
	// UnsupportedLabels: caddy-docker-proxy, configured with container labels.
	UnsupportedLabels = "labels"
	// UnsupportedStopped: the container is not running.
	UnsupportedStopped = "stopped"
	// UnsupportedReadonly: the container's Caddyfile is on a read-only mount.
	UnsupportedReadonly = "readonly"
)

// Instance is a Caddy found on this machine.
type Instance struct {
	// Key is "system" or "docker-<container name>".
	Key     string
	Kind    string
	Name    string
	Running bool
	// ConfigPath is the Caddyfile as Caddy sees it (inside the container for Docker).
	ConfigPath string
	// Layout is "file" (ervisio-repo.caddy plus an import line) or "inline".
	Layout string
	// HostNetwork: a container with network_mode host reaches the server on the host port.
	HostNetwork bool
	// Ephemeral: a container without any mount for the Caddyfile.
	Ephemeral   bool
	ContainerID string
	Networks    []string
	Unsupported string
}

// FilePath is the path of our own file for this instance.
func (i *Instance) FilePath() string { return dirOf(i.ConfigPath) + "/" + FileName }

// ViaContainer reports whether Caddy reaches the server by container name.
func (i *Instance) ViaContainer() bool { return i.Kind == KindDocker && !i.HostNetwork }

// Outcome is the result of a Caddy command.
type Outcome struct {
	OK     bool
	Output string
}

// Backend reads and writes the files of one instance and runs its commands.
type Backend interface {
	// Read returns the file, or ok=false when it does not exist.
	Read(ctx context.Context, path string) (text string, ok bool, err error)
	Write(ctx context.Context, path, text string) error
	Remove(ctx context.Context, path string) error
	// Files lists the files in the Caddyfile's folder and one level below.
	Files(ctx context.Context) ([]string, error)
	Validate(ctx context.Context) Outcome
	Reload(ctx context.Context) Outcome
}

// Detection is everything Detect found.
type Detection struct {
	Instances   []*Instance
	DockerError string
}

const cmdTimeout = 60 * time.Second

var systemConfigPath = regexp.MustCompile(`^/etc/caddy/[A-Za-z0-9._-]{1,100}(/[A-Za-z0-9._-]{1,100})?$`)

func systemctlProp(out, name string) string {
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, name+"="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

var execArgv = regexp.MustCompile(`argv\[\]=([^;]*)`)

func detectSystem(ctx context.Context) *Instance {
	out, err := sys.Run(ctx, "systemctl", "show", "caddy.service", "--property=LoadState,ActiveState,ExecStart")
	if err != nil || systemctlProp(string(out), "LoadState") != "loaded" {
		return nil
	}
	var argv []string
	if m := execArgv.FindStringSubmatch(systemctlProp(string(out), "ExecStart")); m != nil {
		argv = strings.Fields(m[1])
	}
	cfg := ConfigFromArgv(argv)
	inst := &Instance{
		Key: KindSystem, Kind: KindSystem, Name: "Caddy", ConfigPath: cfg, Layout: "file",
		Running: systemctlProp(string(out), "ActiveState") == "active",
	}
	switch {
	case contains(argv, "--resume") || AdapterFromArgv(argv, cfg) != "caddyfile":
		inst.Unsupported = UnsupportedJSON
	case !systemConfigPath.MatchString(cfg):
		inst.Unsupported = UnsupportedPath
	}
	return inst
}

type dockerInspect struct {
	ID     string `json:"Id"`
	Name   string
	State  struct{ Running bool }
	Config struct {
		Image      string
		Entrypoint []string
		Cmd        []string
	}
	HostConfig      struct{ NetworkMode string }
	NetworkSettings struct{ Networks map[string]json.RawMessage }
	Mounts          []struct {
		Destination string
		RW          bool
	}
}

var proxyImage = regexp.MustCompile(`(?i)caddy-docker-proxy`)

// looksLikeCaddy: the image is caddy (any registry or tag), or the command is caddy.
func looksLikeCaddy(image, command string) bool {
	img := strings.ToLower(image)
	img, _, _ = strings.Cut(img, "@")
	base := img[strings.LastIndexByte(img, '/')+1:]
	base, _, _ = strings.Cut(base, ":")
	if base == "caddy" || strings.HasPrefix(base, "caddy-") || strings.HasSuffix(base, "-caddy") || proxyImage.MatchString(img) {
		return true
	}
	cmd := strings.Trim(command, `"`)
	return strings.HasPrefix(cmd, "caddy run") || strings.HasPrefix(cmd, "caddy docker-proxy") ||
		strings.HasPrefix(cmd, "/usr/bin/caddy run")
}

func covers(dest, p string) bool {
	return p == dest || strings.HasPrefix(p, strings.TrimSuffix(dest, "/")+"/")
}

func instanceFromInspect(c dockerInspect) *Instance {
	name := strings.TrimPrefix(c.Name, "/")
	argv := append(append([]string{}, c.Config.Entrypoint...), c.Config.Cmd...)
	cfg := ConfigFromArgv(argv)
	dir := dirOf(cfg)
	dirMounted, fileMounted := false, false
	closest, closestRW, closestLen := false, true, -1
	for _, m := range c.Mounts {
		if covers(m.Destination, dir) {
			dirMounted = true
		}
		if m.Destination == cfg {
			fileMounted = true
		}
		if covers(m.Destination, cfg) && len(m.Destination) > closestLen {
			closest, closestRW, closestLen = true, m.RW, len(m.Destination)
		}
	}
	inst := &Instance{
		Key: "docker-" + name, Kind: KindDocker, Name: name, Running: c.State.Running,
		ConfigPath: cfg, Layout: "inline", HostNetwork: c.HostConfig.NetworkMode == "host",
		Ephemeral: !dirMounted && !fileMounted, ContainerID: c.ID,
	}
	if dirMounted {
		inst.Layout = "file"
	}
	for n := range c.NetworkSettings.Networks {
		inst.Networks = append(inst.Networks, n)
	}
	sort.Strings(inst.Networks)
	switch {
	case proxyImage.MatchString(c.Config.Image) || contains(argv, "docker-proxy"):
		inst.Unsupported = UnsupportedLabels
	case contains(argv, "--resume") || AdapterFromArgv(argv, cfg) != "caddyfile":
		inst.Unsupported = UnsupportedJSON
	case !c.State.Running:
		inst.Unsupported = UnsupportedStopped
	case closest && !closestRW:
		inst.Unsupported = UnsupportedReadonly
	}
	return inst
}

func detectDocker(ctx context.Context) ([]*Instance, error) {
	out, err := sys.Run(ctx, "docker", "ps", "-a", "--no-trunc", "--format", "{{json .}}")
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	var ids []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var row struct{ ID, Image, Command string }
		if json.Unmarshal([]byte(l), &row) == nil && looksLikeCaddy(row.Image, row.Command) {
			ids = append(ids, row.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	raw, err := sys.Run(ctx, append([]string{"docker", "inspect"}, ids...)...)
	if err != nil {
		return nil, fmt.Errorf("inspect containers: %w", err)
	}
	var list []dockerInspect
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse docker inspect: %w", err)
	}
	var res []*Instance
	for _, c := range list {
		res = append(res, instanceFromInspect(c))
	}
	sort.Slice(res, func(a, b int) bool {
		if res[a].Running != res[b].Running {
			return res[a].Running
		}
		return res[a].Name < res[b].Name
	})
	return res, nil
}

// Detect finds every Caddy on this machine: the system service first, then
// containers. A missing docker binary is not an error.
func Detect(ctx context.Context) *Detection {
	d := &Detection{}
	if s := detectSystem(ctx); s != nil {
		d.Instances = append(d.Instances, s)
	}
	list, err := detectDocker(ctx)
	if err != nil {
		var ee *sys.ExitError
		if errors.As(err, &ee) {
			d.DockerError = err.Error()
		}
	}
	d.Instances = append(d.Instances, list...)
	return d
}

// Pick chooses the Caddy to use: running and supported first, the system
// service before containers. key selects one by name when not empty.
func (d *Detection) Pick(key string) *Instance {
	if key != "" {
		for _, i := range d.Instances {
			if i.Key == key || i.Name == key {
				return i
			}
		}
		return nil
	}
	score := func(i *Instance) int {
		s := 0
		if i.Unsupported == "" {
			s += 2
		}
		if i.Running {
			s++
		}
		return s
	}
	var best *Instance
	for _, i := range d.Instances {
		if best == nil || score(i) > score(best) {
			best = i
		}
	}
	return best
}

// BackendFor returns the backend of an instance.
func BackendFor(i *Instance) Backend {
	if i.Kind == KindDocker {
		return &dockerBackend{inst: i}
	}
	return &systemBackend{inst: i}
}

/* ---------- system ---------- */

type systemBackend struct{ inst *Instance }

func (b *systemBackend) Read(_ context.Context, path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), true, nil
}

func (b *systemBackend) Write(_ context.Context, path, text string) error {
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func (b *systemBackend) Remove(_ context.Context, path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func (b *systemBackend) Files(_ context.Context) ([]string, error) {
	var out []string
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("list %s: %w", dir, err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			switch {
			case e.Type().IsRegular():
				out = append(out, p)
			case e.IsDir() && depth < 1:
				if err := walk(p, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(dirOf(b.inst.ConfigPath), 0); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func runOutcome(ctx context.Context, c sys.Cmd) Outcome {
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	if c.Timeout == 0 {
		c.Timeout = cmdTimeout
	}
	err := sys.Exec(ctx, c)
	return Outcome{OK: err == nil, Output: strings.TrimSpace(buf.String())}
}

func (b *systemBackend) Validate(ctx context.Context) Outcome {
	return runOutcome(ctx, sys.Cmd{
		Argv: []string{"caddy", "validate", "--config", b.inst.ConfigPath, "--adapter", "caddyfile"},
		Env:  []string{"XDG_DATA_HOME=/tmp/ervisio-repo-caddy/data", "XDG_CONFIG_HOME=/tmp/ervisio-repo-caddy/config"},
	})
}

func (b *systemBackend) Reload(ctx context.Context) Outcome {
	return runOutcome(ctx, sys.Cmd{Argv: []string{"systemctl", "reload", "caddy.service"}})
}

/* ---------- docker ---------- */

type dockerBackend struct{ inst *Instance }

func (b *dockerBackend) exec(ctx context.Context, stdin string, env []string, argv ...string) (string, string, error) {
	full := []string{"docker", "exec", "-i"}
	for _, e := range env {
		full = append(full, "-e", e)
	}
	full = append(full, b.inst.ContainerID)
	full = append(full, argv...)
	var out, errb bytes.Buffer
	c := sys.Cmd{Argv: full, Stdout: &out, Stderr: &errb, Timeout: cmdTimeout}
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	err := sys.Exec(ctx, c)
	return out.String(), errb.String(), err
}

func (b *dockerBackend) Read(ctx context.Context, path string) (string, bool, error) {
	out, stderr, err := b.exec(ctx, "", nil, "cat", path)
	if err == nil {
		return out, true, nil
	}
	if strings.Contains(stderr, "No such file") {
		return "", false, nil
	}
	return "", false, fmt.Errorf("read %s: %w", path, err)
}

// Write copies over the file with `cat >` so a Caddyfile mounted as a single
// file keeps its inode (a rename fails on a bind mount).
func (b *dockerBackend) Write(ctx context.Context, path, text string) error {
	if text == "" {
		text = "\n"
	}
	if _, _, err := b.exec(ctx, text, nil, "sh", "-c", `cat > "$1"`, "sh", path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func (b *dockerBackend) Remove(ctx context.Context, path string) error {
	if _, _, err := b.exec(ctx, "", nil, "rm", "-f", path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func (b *dockerBackend) Files(ctx context.Context) ([]string, error) {
	out, _, err := b.exec(ctx, "", nil, "find", dirOf(b.inst.ConfigPath), "-maxdepth", "2", "-type", "f", "!", "-name", ".*")
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dirOf(b.inst.ConfigPath), err)
	}
	var res []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			res = append(res, l)
		}
	}
	sort.Strings(res)
	return res, nil
}

func (b *dockerBackend) run(ctx context.Context, env []string, argv ...string) Outcome {
	var buf bytes.Buffer
	full := []string{"docker", "exec"}
	for _, e := range env {
		full = append(full, "-e", e)
	}
	full = append(append(full, b.inst.ContainerID), argv...)
	err := sys.Exec(ctx, sys.Cmd{Argv: full, Stdout: &buf, Stderr: &buf, Timeout: cmdTimeout})
	return Outcome{OK: err == nil, Output: strings.TrimSpace(buf.String())}
}

func (b *dockerBackend) Validate(ctx context.Context) Outcome {
	return b.run(ctx,
		[]string{"XDG_DATA_HOME=/tmp/ervisio-repo-caddy/data", "XDG_CONFIG_HOME=/tmp/ervisio-repo-caddy/config"},
		"caddy", "validate", "--config", b.inst.ConfigPath, "--adapter", "caddyfile")
}

func (b *dockerBackend) Reload(ctx context.Context) Outcome {
	return b.run(ctx, nil, "caddy", "reload", "--config", b.inst.ConfigPath, "--adapter", "caddyfile", "--force")
}

// ConnectNetwork attaches the Caddy container to the network of the server.
func ConnectNetwork(ctx context.Context, network, container string) error {
	if _, err := sys.Run(ctx, "docker", "network", "connect", network, container); err != nil {
		return fmt.Errorf("connect %s to %s: %w", container, network, err)
	}
	return nil
}
