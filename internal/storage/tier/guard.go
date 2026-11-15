package tier

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// metadataNet is the IPv6 address range of the EC2 instance metadata service.
var metadataNet = &net.IPNet{IP: net.ParseIP("fd00:ec2::"), Mask: net.CIDRMask(32, 128)}

// BlockedAddress reports whether storage targets created in the web admin may
// not connect to ip: link-local (169.254.0.0/16, fe80::/10, which includes the
// cloud metadata address), the EC2 IPv6 metadata range, unspecified and
// multicast addresses. Private and loopback addresses are allowed, since
// self-hosted object stores such as MinIO live there.
func BlockedAddress(ip net.IP) bool {
	return ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || metadataNet.Contains(ip)
}

// guardControl is a net.Dialer.Control that checks the resolved address at
// connect time, so a name that resolves to a blocked address is refused too.
func guardControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("address %q is not valid", address)
	}
	ip := net.ParseIP(host)
	if ip == nil || BlockedAddress(ip) {
		return fmt.Errorf("connecting to %s is not allowed for storage targets", host)
	}
	return nil
}

// guardedClient is an HTTP client whose connections go through guardControl.
func guardedClient() *http.Client {
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: guardControl}
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}}
}
