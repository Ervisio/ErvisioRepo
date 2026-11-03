package credentials

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/ssh"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
)

// MethodSSHKey is the auth.Principal method of a caller authenticated by an
// SSH public key. It holds no token scopes: the key grants what its owner
// may do.
const MethodSSHKey = "ssh_key"

// ErrUnknownKey is returned for a key that is not registered, or whose
// owner is suspended.
var ErrUnknownKey = errors.New("unknown ssh key")

// SSHCaller is the owner of an authenticating SSH key.
type SSHCaller struct {
	Principal auth.Principal
	KeyID     uuid.UUID
}

// AuthenticateSSHKey returns the owner of a public key presented to the SSH
// server. The key type is not checked here: only keys accepted by
// ParseSSHKey can be registered.
func (s *Service) AuthenticateSSHKey(ctx context.Context, pub ssh.PublicKey) (SSHCaller, error) {
	fp := strings.TrimPrefix(ssh.FingerprintSHA256(pub), "SHA256:")
	row, err := s.q.GetSSHKeyByFingerprint(ctx, fp)
	if errors.Is(err, pgx.ErrNoRows) {
		return SSHCaller{}, ErrUnknownKey
	}
	if err != nil {
		return SSHCaller{}, fmt.Errorf("get ssh key: %w", err)
	}
	if row.SuspendedAt.Valid {
		return SSHCaller{}, ErrUnknownKey
	}
	return SSHCaller{
		Principal: auth.Principal{UserID: row.UserID, Username: row.Username, Role: row.Role, Method: MethodSSHKey},
		KeyID:     row.ID,
	}, nil
}
