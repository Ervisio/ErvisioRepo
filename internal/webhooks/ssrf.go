package webhooks

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// errBlockedAddress is returned by the dialer for a non-public address.
var errBlockedAddress = errors.New("destination address is not allowed")

// extraBlocked are special-purpose ranges that the netip predicates do not
// cover (RFC 6890 and friends): CGNAT, "this network", IETF protocol
// assignments, documentation, benchmarking, reserved, NAT64, discard-only,
// 6to4 and documentation IPv6.
var extraBlocked = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "255.255.255.255/32",
		"64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001:db8::/32", "2002::/16",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// guard decides which addresses hooks may reach.
type guard struct {
	allowPrivate bool
	allowed      []netip.Prefix
}

func newGuard(allowPrivate bool, cidrs []string) (*guard, error) {
	g := &guard{allowPrivate: allowPrivate}
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			return nil, fmt.Errorf("webhooks.allowed_cidrs %q: %w", c, err)
		}
		g.allowed = append(g.allowed, p.Masked())
	}
	return g, nil
}

// public reports whether a is a global unicast address of the public
// Internet.
func public(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() || !a.IsGlobalUnicast() {
		return false
	}
	for _, p := range extraBlocked {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// permits reports whether a hook may connect to a.
func (g *guard) permits(a netip.Addr) bool {
	a = a.Unmap()
	if g.allowPrivate || public(a) {
		return true
	}
	for _, p := range g.allowed {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// control runs after DNS resolution, right before each connect: checking
// the resolved address here (not the host name) defeats DNS rebinding.
func (g *guard) control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("webhook dial %s: %w", address, err)
	}
	if !g.permits(ap.Addr()) {
		return fmt.Errorf("webhook dial %s: %w", ap.Addr(), errBlockedAddress)
	}
	return nil
}

// checkURL validates a hook URL. A literal address or a local host name is
// rejected early; names are checked again on every connection.
func (g *guard) checkURL(raw string) error {
	if len(raw) > 2048 {
		return invalid("url is longer than 2048 characters")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return invalid("url must be an absolute http or https URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return invalid("url must use http or https")
	}
	if u.User != nil {
		return invalid("url must not contain credentials")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return invalid("url must not contain a fragment")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return invalid("url has no host")
	}
	if g.allowPrivate {
		return nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return invalid("url must not point to the local host")
	}
	if a, err := netip.ParseAddr(host); err == nil && !g.permits(a) {
		return invalid("url points to a non-public address (see webhooks.allow_private and webhooks.allowed_cidrs)")
	}
	return nil
}

// newClient builds the HTTP client of the deliveries: no proxy (it would
// bypass the address check), no redirects, the guard on every dial and the
// configured timeout on the whole attempt.
func newClient(g *guard, timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second, Control: g.control}
	t := &http.Transport{
		Proxy:                 nil,
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          50,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Transport: t,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
