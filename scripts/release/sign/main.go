// Command sign generates, applies and checks the ed25519 release signature.
//
//	go run ./scripts/release/sign genkey <private.pem> <public.pem>
//	go run ./scripts/release/sign sign   <private.pem> <file> <file.sig>
//	go run ./scripts/release/sign verify <public.pem>  <file> <file.sig>
//
// Keys are PEM (PKCS#8 private, PKIX public) so that openssl 3 verifies the
// raw 64-byte signature with: openssl pkeyutl -verify -rawin ...
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: sign genkey, sign or verify")
	}
	switch args[0] {
	case "genkey":
		if len(args) != 3 {
			return fmt.Errorf("usage: sign genkey <private.pem> <public.pem>")
		}
		return genkey(args[1], args[2])
	case "sign":
		if len(args) != 4 {
			return fmt.Errorf("usage: sign sign <private.pem> <file> <file.sig>")
		}
		return sign(args[1], args[2], args[3])
	case "verify":
		if len(args) != 4 {
			return fmt.Errorf("usage: sign verify <public.pem> <file> <file.sig>")
		}
		return verify(args[1], args[2], args[3])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func genkey(privPath, pubPath string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return fmt.Errorf("marshal public key: %w", err)
	}
	if err := os.WriteFile(privPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600); err != nil { //nolint:gosec // operator-supplied path
		return fmt.Errorf("write private key: %w", err)
	}
	if err := os.WriteFile(pubPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o644); err != nil { //nolint:gosec // public key
		return fmt.Errorf("write public key: %w", err)
	}
	return nil
}

func readPEM(path string) ([]byte, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%s: no PEM block", path)
	}
	return block.Bytes, nil
}

func sign(privPath, file, sigPath string) error {
	der, err := readPEM(privPath)
	if err != nil {
		return err
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return fmt.Errorf("parse private key: %w", err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return fmt.Errorf("%s is not an ed25519 key", privPath)
	}
	msg, err := os.ReadFile(file) //nolint:gosec // operator-supplied path
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	if err := os.WriteFile(sigPath, ed25519.Sign(priv, msg), 0o644); err != nil { //nolint:gosec // signature is public
		return fmt.Errorf("write signature: %w", err)
	}
	return nil
}

func verify(pubPath, file, sigPath string) error {
	der, err := readPEM(pubPath)
	if err != nil {
		return err
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return fmt.Errorf("parse public key: %w", err)
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("%s is not an ed25519 key", pubPath)
	}
	msg, err := os.ReadFile(file) //nolint:gosec // operator-supplied path
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	sig, err := os.ReadFile(sigPath) //nolint:gosec // operator-supplied path
	if err != nil {
		return fmt.Errorf("read %s: %w", sigPath, err)
	}
	if !ed25519.Verify(pub, msg, sig) {
		return fmt.Errorf("signature does not match %s", file)
	}
	return nil
}
