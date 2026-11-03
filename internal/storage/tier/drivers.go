package tier

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"

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
	if t.CredentialsFile != "" {
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

// openSFTP builds an SFTP store authenticated with key_file. The server key
// is always verified against known_hosts_file.
func openSFTP(_ context.Context, t config.Target, _ BuildOptions) (blob.Store, error) {
	pem, err := readSecretFile(t.KeyFile)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(pem)
	if err != nil {
		return nil, fmt.Errorf("key_file %s: %w", t.KeyFile, err)
	}
	if t.KnownHostsFile == "" {
		return nil, errors.New("known_hosts_file is required")
	}
	hostKeys, err := knownhosts.New(t.KnownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("known_hosts_file: %w", err)
	}
	return sftp.New(sftp.Options{
		Name:            t.Name,
		Addr:            net.JoinHostPort(t.Host, strconv.Itoa(t.Port)),
		User:            t.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeys,
		Root:            t.Root,
		MaxConns:        t.MaxConns,
	})
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
