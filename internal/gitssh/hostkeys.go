package gitssh

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// Host key files in the host key directory.
const (
	hostKeyEd25519 = "ssh_host_ed25519_key"
	hostKeyRSA     = "ssh_host_rsa_key"
	rsaBits        = 3072
)

// LoadHostKeys returns the ed25519 and RSA host keys kept in dir, generating
// the missing ones with mode 0600 (the directory gets 0700). A key file
// readable by group or others is refused.
func LoadHostKeys(dir string) ([]ssh.Signer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("host key dir: %w", err)
	}
	gens := []struct {
		name string
		gen  func() (crypto.PrivateKey, error)
	}{
		{hostKeyEd25519, func() (crypto.PrivateKey, error) {
			_, k, err := ed25519.GenerateKey(rand.Reader)
			return k, err
		}},
		{hostKeyRSA, func() (crypto.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, rsaBits) }},
	}
	signers := make([]ssh.Signer, 0, len(gens))
	for _, g := range gens {
		s, err := loadOrCreate(filepath.Join(dir, g.name), g.gen)
		if err != nil {
			return nil, err
		}
		signers = append(signers, s)
	}
	return signers, nil
}

func loadOrCreate(path string, gen func() (crypto.PrivateKey, error)) (ssh.Signer, error) {
	st, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return createKey(path, gen)
	case err != nil:
		return nil, fmt.Errorf("stat host key: %w", err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("host key %s: permissions %v are too open (want 0600)", path, st.Mode().Perm())
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is the configured host key dir
	if err != nil {
		return nil, fmt.Errorf("read host key: %w", err)
	}
	s, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("parse host key %s: %w", path, err)
	}
	return s, nil
}

func createKey(path string, gen func() (crypto.PrivateKey, error)) (ssh.Signer, error) {
	key, err := gen()
	if err != nil {
		return nil, fmt.Errorf("generate host key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(key, "ervisio-repo host key")
	if err != nil {
		return nil, fmt.Errorf("marshal host key: %w", err)
	}
	data := pem.EncodeToMemory(block)
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return nil, fmt.Errorf("create host key: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("chmod host key: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("write host key: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("sync host key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close host key: %w", err)
	}
	// Link instead of rename: a concurrent first start keeps the key that
	// won the race.
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("install host key: %w", err)
	} else if err != nil {
		return loadOrCreate(path, gen)
	}
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("host key signer: %w", err)
	}
	return s, nil
}
