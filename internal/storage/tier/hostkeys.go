package tier

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// HostKey is one SSH host key offered by a server.
type HostKey struct {
	Type        string
	Fingerprint string // SHA256, as printed by ssh-keygen -l
	Line        string // known_hosts line
}

// hostKeyAlgorithms are asked one at a time: a handshake only shows the key
// of the algorithm it negotiates.
var hostKeyAlgorithms = [][]string{
	{ssh.KeyAlgoED25519},
	{ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521},
	{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA},
}

// errGotKey stops the handshake once the server key is known: no sign-in is
// attempted.
var errGotKey = errors.New("host key received")

// ScanHostKeys reads the host keys host:port offers, like ssh-keyscan. The
// connection refuses the addresses storage targets may not use.
func ScanHostKeys(ctx context.Context, host string, port int) ([]HostKey, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var keys []HostKey
	var lastErr error
	for _, algos := range hostKeyAlgorithms {
		key, err := hostKey(ctx, addr, algos)
		if err != nil {
			lastErr = err
			continue
		}
		keys = append(keys, HostKey{
			Type:        key.Type(),
			Fingerprint: ssh.FingerprintSHA256(key),
			Line:        knownhosts.Line([]string{knownhosts.Normalize(addr)}, key),
		})
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("read host keys of %s: %w", addr, lastErr)
	}
	return keys, nil
}

func hostKey(ctx context.Context, addr string, algos []string) (ssh.PublicKey, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	d := net.Dialer{Control: guardControl}
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer nc.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = nc.SetDeadline(dl)
	}
	var got ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User:              "erepo-keyscan",
		HostKeyAlgorithms: algos,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = key
			return errGotKey
		},
	}
	_, _, _, err = ssh.NewClientConn(nc, addr, cfg)
	if got != nil {
		return got, nil
	}
	if err == nil {
		err = errors.New("no host key received")
	}
	return nil, err
}
