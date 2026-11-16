package config

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// sampleTemplate is the commented configuration; Render fills the
// @@…@@ placeholders.
const sampleTemplate = `# ervisio-runner configuration.
# Durations use Go syntax: "30s", "10m", "6h".

[server]
# Public URL of ErvisioRepo. Empty uses the URL saved by "register".
url = "@@URL@@"
# Skip TLS certificate verification. Only for self-signed test instances.
insecure_tls = false

[runner]
# Credential written by "register" (mode 0600).
state_file = "@@DIR@@/.runner"
# Jobs run at the same time.
capacity = @@CAPACITY@@
# runs-on labels: "name=docker://image" runs the job in a container,
# "name=host" runs it directly on this machine (requires host.enabled).
labels = [
@@LABELS@@]
# Extra environment of every job. env_file holds KEY=VALUE lines; keys in
# [runner.env] win over the file.
env_file = ""
# Clones of remote actions, kept between jobs.
cache_dir = "@@DIR@@/cache"
# FetchTask long-poll timeout (the server answers within ~30s).
fetch_timeout = "40s"
# Pause between polls; doubles on errors up to fetch_interval_max.
fetch_interval = "2s"
fetch_interval_max = "30s"
heartbeat_interval = "30s"
# On SIGINT/SIGTERM running jobs get this long to finish, then they are
# cancelled.
shutdown_timeout = "10m"
# Upper bound of a job when the server sends no timeout.
job_timeout = "6h"
# Log upload: every log_report_interval, or sooner when log_report_batch_size
# rows are buffered or the oldest row waited log_report_max_latency.
log_report_interval = "5s"
log_report_max_latency = "2s"
log_report_batch_size = 100
state_report_interval = "5s"
# Budget of each final upload attempt when a job ends.
report_close_timeout = "10s"

# [runner.env]
# HTTP_PROXY = "http://proxy.internal:3128"

[log]
level = "info"   # debug | info | warn | error
format = "json"  # json | text

[container]
# Docker daemon; empty uses DOCKER_HOST or the default socket.
docker_host = ""
# Network of job containers; empty creates one network per job. "host" is
# refused.
network = ""
# Privileged containers are not supported.
privileged = false
# cgroup limits of each job container ("" / 0 = unlimited).
cpus = "2"
memory = "4g"
pids_limit = 4096
# Adds --security-opt=no-new-privileges. Docker's default seccomp profile is
# always kept.
no_new_privileges = true
# Volumes and host paths jobs may mount (glob patterns). Empty allows only
# the job's own workspace volumes.
valid_volumes = []
# Workspace parent directory inside the container.
workdir_parent = "workspace"
force_pull = false
force_rebuild = false

[host]
# Allow "name=host" labels. Jobs then run unisolated as the runner user.
enabled = @@HOST@@
workdir_parent = "@@DIR@@/host"
`

// DefaultDataDir holds the state file, the action cache and host workspaces.
const DefaultDataDir = "/var/lib/ervisio-runner"

// RenderOptions are the values Render writes into the configuration.
type RenderOptions struct {
	URL      string   // server.url, may be empty
	Labels   []string // runner.labels
	Capacity int      // runner.capacity
	Host     bool     // host.enabled
	DataDir  string   // parent of state_file, cache_dir and host.workdir_parent
}

// Sample is the commented configuration printed by "generate-config".
// Every value equals the built-in default.
func Sample() string {
	return Render(RenderOptions{Labels: DefaultLabels(), Capacity: DefaultCapacity(), DataDir: DefaultDataDir})
}

// Render returns the commented configuration with o applied; every other
// value is the built-in default.
func Render(o RenderOptions) string {
	var b strings.Builder
	for _, l := range o.Labels {
		fmt.Fprintf(&b, "  %s,\n", strconv.Quote(l))
	}
	dir := strings.TrimRight(filepath.ToSlash(o.DataDir), "/")
	if dir == "" {
		dir = DefaultDataDir
	}
	return strings.NewReplacer(
		"@@URL@@", strings.ReplaceAll(o.URL, `"`, ""),
		"@@CAPACITY@@", strconv.Itoa(max(o.Capacity, 1)),
		"@@LABELS@@", b.String(),
		"@@HOST@@", strconv.FormatBool(o.Host),
		"@@DIR@@", strings.ReplaceAll(dir, `"`, ""),
	).Replace(sampleTemplate)
}
