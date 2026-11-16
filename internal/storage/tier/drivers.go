package tier

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/local"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/metered"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/retry"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/s3"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/sftp"
	"github.com/Ervisio/ErvisioRepo/internal/storage/crypt"
)

// maxSecretFile bounds the size of credential and key files.
const maxSecretFile = 64 << 10

// Built-in drivers and wrappers, keyed by config.Target.Kind.
func init() {
	RegisterDriver("local", func(_ context.Context, t config.Target, _ BuildOptions) (blob.Store, error) {
		return local.New(t.Name, t.Path)
	})
	RegisterDriver("s3", openS3)
	RegisterDriver("sftp", openSFTP)
	RegisterWrappers(
		func(s blob.Store, _ config.Target, _ BuildOptions) (blob.Store, error) {
			return retry.Wrap(s, retry.Options{}), nil
		},
		func(s blob.Store, _ config.Target, o BuildOptions) (blob.Store, error) {
			return crypt.Wrap(s, crypt.Options{Master: o.MasterKey})
		},
		func(s blob.Store, _ config.Target, o BuildOptions) (blob.Store, error) {
			if o.Registerer == nil {
				return s, nil
			}
			return metered.Wrap(s, metered.NewMetrics(o.Registerer)), nil
		},
	)
}

// openS3 builds an S3 store. Credentials come from credentials_file, or else
// from AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_SESSION_TOKEN.
func openS3(_ context.Context, t config.Target, _ BuildOptions) (blob.Store, error) {
	o := s3.Options{
		Name:      t.Name,
		Endpoint:  t.Endpoint,
		Region:    t.Region,
		Bucket:    t.Bucket,
		Prefix:    t.Prefix,
		PathStyle: t.PathStyle,
	}
	if t.Guarded {
		o.HTTPClient = guardedClient()
	}
	if t.AccessKey != "" || t.SecretKey != "" {
		if t.AccessKey == "" || t.SecretKey == "" {
			return nil, errors.New("inline credentials need both an access key and a secret key")
		}
		o.AccessKeyID, o.SecretAccessKey = t.AccessKey, t.SecretKey
	} else if t.CredentialsFile != "" {
		data, err := readSecretFile(t.CredentialsFile)
		if err != nil {
			return nil, err
		}
		if o.AccessKeyID, o.SecretAccessKey, err = s3.ParseCredentials(data); err != nil {
			return nil, fmt.Errorf("%s: %w", t.CredentialsFile, err)
		}
	} else {
		o.AccessKeyID = os.Getenv("AWS_ACCESS_KEY_ID")
		o.SecretAccessKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
		o.SessionToken = os.Getenv("AWS_SESSION_TOKEN")
		if o.AccessKeyID == "" || o.SecretAccessKey == "" {
			return nil, errors.New("no credentials: set credentials_file or AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY")
		}
	}
	return s3.New(o)
}

// openSFTP builds an SFTP store authenticated with a private key (inline or
// key_file), a password (inline or password_file), or both. The server key is
// always verified against the inline known_hosts content or known_hosts_file.
func openSFTP(_ context.Context, t config.Target, _ BuildOptions) (blob.Store, error) {
	auth, err := sftpAuth(t)
	if err != nil {
		return nil, err
	}
	var hostKeys ssh.HostKeyCallback
	switch {
	case t.KnownHostsData != "":
		if hostKeys, err = HostKeyCallback([]byte(t.KnownHostsData)); err != nil {
			return nil, fmt.Errorf("known hosts: %w", err)
		}
	case t.KnownHostsFile != "":
		if hostKeys, err = knownhosts.New(t.KnownHostsFile); err != nil {
			return nil, fmt.Errorf("known_hosts_file: %w", err)
		}
	default:
		return nil, errors.New("known_hosts_file is required")
	}
	var control func(network, address string, c syscall.RawConn) error
	if t.Guarded {
		control = guardControl
	}
	return sftp.New(sftp.Options{
		DialControl:     control,
		Name:            t.Name,
		Addr:            net.JoinHostPort(t.Host, strconv.Itoa(t.Port)),
		User:            t.User,
		Auth:            auth,
		HostKeyCallback: hostKeys,
		Root:            t.Root,
		MaxConns:        t.MaxConns,
	})
}

// sftpAuth returns the SSH auth methods of an SFTP target: the private key
// first, then the password (also offered as keyboard-interactive, which some
// servers use instead of the password method).
func sftpAuth(t config.Target) ([]ssh.AuthMethod, error) {
	var auth []ssh.AuthMethod
	pem, keyName := []byte(t.KeyPEM), "private key"
	if t.KeyPEM == "" && t.KeyFile != "" {
		var err error
		if pem, err = readSecretFile(t.KeyFile); err != nil {
			return nil, err
		}
		keyName = "key_file " + t.KeyFile
	}
	if len(pem) > 0 {
		signer, err := ParsePrivateKey(pem, []byte(t.KeyPassphrase))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", keyName, err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	password := t.Password
	if password == "" && t.PasswordFile != "" {
		raw, err := readSecretFile(t.PasswordFile)
		if err != nil {
			return nil, err
		}
		password = strings.TrimRight(string(raw), "\r\n")
	}
	if password != "" {
		auth = append(auth, ssh.Password(password), ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = password
			}
			return answers, nil
		}))
	}
	if len(auth) == 0 {
		return nil, errors.New("no credentials: set a private key or a password")
	}
	return auth, nil
}

// ParsePrivateKey parses an SSH private key, using passphrase when it is not
// empty. The error never contains key material.
func ParsePrivateKey(pem, passphrase []byte) (ssh.Signer, error) {
	if len(passphrase) > 0 {
		return ssh.ParsePrivateKeyWithPassphrase(pem, passphrase)
	}
	return ssh.ParsePrivateKey(pem)
}

// HostKeyCallback builds a server key check from known_hosts content. The
// knownhosts package only reads files, so the content goes through a private
// temporary file that is removed right away; it holds public keys only.
func HostKeyCallback(data []byte) (ssh.HostKeyCallback, error) {
	dir, err := os.MkdirTemp("", "erepo-known-hosts-")
	if err != nil {
		return nil, fmt.Errorf("known hosts: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("known hosts: %w", err)
	}
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("known hosts: %w", err)
	}
	return cb, nil
}

// readSecretFile reads a credential file that must not be accessible by
// group or others.
func readSecretFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read secret: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("read secret: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s must not be accessible by group or others (mode %04o)", path, fi.Mode().Perm())
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSecretFile))
	if err != nil {
		return nil, fmt.Errorf("read secret %s: %w", path, err)
	}
	return data, nil
}
