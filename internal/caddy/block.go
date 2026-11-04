package caddy

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	hostLabel   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	hasLetter   = regexp.MustCompile(`[a-z]`)
	numericTLD  = regexp.MustCompile(`^[0-9-]+$`)
	schemeHTTP  = regexp.MustCompile(`^https?://`)
	targetChars = regexp.MustCompile(`^[A-Za-z0-9._\[\]:-]+$`)
	proxyLine   = regexp.MustCompile(`(?m)^\s*reverse_proxy\s+(\S+)`)
)

// ValidHost reports whether host is a name Caddy can serve with a public
// certificate: lower case, at least two DNS labels, no wildcard, no IP address.
func ValidHost(host string) bool {
	if len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	labels := strings.Split(host, ".")
	for _, l := range labels {
		if !hostLabel.MatchString(l) {
			return false
		}
	}
	tld := labels[len(labels)-1]
	return hasLetter.MatchString(tld) && !numericTLD.MatchString(tld)
}

// NormalizeHost trims, lower-cases and drops a trailing dot or a pasted
// http(s):// prefix. ok is false when the result is not a valid host.
func NormalizeHost(input string) (string, bool) {
	h := strings.ToLower(strings.TrimSpace(input))
	h = schemeHTTP.ReplaceAllString(h, "")
	h = strings.TrimSuffix(h, "/")
	h = strings.TrimSuffix(h, ".")
	return h, ValidHost(h)
}

// GenerateBlock returns the site block (ending with a newline), with the Git
// settings of PLAN 9.4: large pushes, long clones, streamed logs, no
// compression of git packs. inline adds the end marker for blocks kept inside
// the main Caddyfile.
func GenerateBlock(host, target string, inline bool) (string, error) {
	if !ValidHost(host) {
		return "", fmt.Errorf("not a valid host name: %s", host)
	}
	if !targetChars.MatchString(target) {
		return "", fmt.Errorf("not a valid upstream: %s", target)
	}
	lines := []string{
		Marker,
		MarkerHint,
		host + " {",
		"\trequest_body {",
		"\t\tmax_size 10GB",
		"\t}",
		"\treverse_proxy " + target + " {",
		"\t\tflush_interval -1",
		"\t\ttransport http {",
		"\t\t\tread_timeout 0",
		"\t\t\twrite_timeout 0",
		"\t\t}",
		"\t}",
		"\tencode {",
		"\t\tzstd",
		"\t\tgzip",
		"\t\tmatch {",
		"\t\t\tnot header Content-Type application/x-git-*",
		"\t\t}",
		"\t}",
		"}",
	}
	if inline {
		lines = append(lines, EndMarker)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// Managed is a block written by this package.
type Managed struct {
	Host   string
	Target string
}

// ParseManaged reads one of our blocks (a whole ervisio-repo.caddy file, or the
// inline part). It returns nil when the text is not ours.
func ParseManaged(text string) *Managed {
	src := normalizeNL(text)
	lines := strings.Split(src, "\n")
	if strings.TrimSpace(lines[0]) != Marker {
		return nil
	}
	o, err := Scan(src)
	if err != nil || len(o.Sites) != 1 {
		return nil
	}
	site := o.Sites[0]
	hosts := HostsOf(site.Head)
	if len(hosts) != 1 {
		return nil
	}
	body := strings.Join(lines[site.Start+1:site.End], "\n")
	m := &Managed{Host: hosts[0]}
	if g := proxyLine.FindStringSubmatch(body); g != nil {
		m.Target = g[1]
	}
	return m
}

// Upstream says where a reverse proxy reaches the server.
type Upstream struct {
	// Host and Port are the published address (Caddy on the host).
	Host string
	Port int
	// Container, ContainerPort and Network are used by Caddy in a container.
	Container     string
	ContainerPort int
	Network       string
}

// ProxyTarget picks the address Caddy sends requests to: the host address for
// Caddy on the host (or with host networking), the container for Caddy in
// Docker.
func ProxyTarget(viaContainer bool, u Upstream) string {
	if viaContainer {
		return fmt.Sprintf("%s:%d", u.Container, u.ContainerPort)
	}
	host := u.Host
	switch {
	case host == "" || host == "0.0.0.0" || host == "::" || host == "[::]":
		host = "127.0.0.1"
	case strings.Contains(host, ":") && !strings.HasPrefix(host, "["):
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s:%d", host, u.Port)
}

// DockerProxyLabels returns the labels for caddy-docker-proxy, which reads
// sites from container labels instead of a Caddyfile.
func DockerProxyLabels(host string, u Upstream) string {
	return strings.Join([]string{
		"caddy: " + host,
		fmt.Sprintf("caddy.reverse_proxy: \"{{upstreams %d}}\"", u.ContainerPort),
		"caddy.reverse_proxy.flush_interval: -1",
		"caddy.request_body.max_size: 10GB",
	}, "\n")
}

var jsonLine = regexp.MustCompile(`^Error:|error`)

// CleanOutput reduces Caddy's output to its last error lines.
func CleanOutput(out string) string {
	var lines, errs []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
			if jsonLine.MatchString(l) && !strings.HasPrefix(l, "{") {
				errs = append(errs, l)
			}
		}
	}
	pick := lines
	if len(errs) > 0 {
		pick = errs
	}
	if len(pick) > 3 {
		pick = pick[len(pick)-3:]
	}
	s := strings.Join(pick, "\n")
	if len(s) > 800 {
		s = s[:800]
	}
	return s
}

// ConfigFromArgv returns the --config value of a Caddy command line, or
// /etc/caddy/Caddyfile.
func ConfigFromArgv(argv []string) string {
	for i, a := range argv {
		if (a == "--config" || a == "-c") && i+1 < len(argv) {
			return argv[i+1]
		}
		if strings.HasPrefix(a, "--config=") {
			return strings.TrimPrefix(a, "--config=")
		}
	}
	return "/etc/caddy/Caddyfile"
}

// AdapterFromArgv returns the adapter of a Caddy command line: "caddyfile"
// unless --adapter says otherwise or the config is a .json file.
func AdapterFromArgv(argv []string, configPath string) string {
	for i, a := range argv {
		if a == "--adapter" && i+1 < len(argv) {
			return argv[i+1]
		}
		if strings.HasPrefix(a, "--adapter=") {
			return strings.TrimPrefix(a, "--adapter=")
		}
	}
	if strings.HasSuffix(strings.ToLower(configPath), ".json") {
		return "json"
	}
	return "caddyfile"
}
