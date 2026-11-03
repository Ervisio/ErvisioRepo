package tier

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/blobtest"
)

func writeFile(t *testing.T, name string, data []byte, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildStoreS3MinIO(t *testing.T) {
	m := blobtest.StartMinIO(t)
	creds := writeFile(t, "creds", []byte("access_key_id = "+m.AccessKey+"\nsecret_access_key = "+m.SecretKey+"\n"), 0o600)
	tgt := config.Target{Name: "obj", Kind: "s3", Endpoint: m.Endpoint, Bucket: m.Bucket, PathStyle: true,
		Prefix: "factory/", CredentialsFile: creds, Encrypt: true}
	reg := prometheus.NewRegistry()
	ctx := context.Background()
	stores, err := BuildStores(ctx, config.Storage{Targets: []config.Target{tgt, {Name: "obj2", Kind: "s3",
		Endpoint: m.Endpoint, Bucket: m.Bucket, PathStyle: true, CredentialsFile: creds}}},
		BuildOptions{MasterKey: make([]byte, 32), Registerer: reg})
	if err != nil {
		t.Fatal(err)
	}
	defer CloseStores(stores)
	for name, s := range stores {
		if res := CheckStore(ctx, s); !res.OK {
			t.Fatalf("%s: check failed: %+v", name, res)
		}
	}
	// The encrypted target stores ciphertext: read it raw through obj2.
	plain := []byte("factory plaintext")
	if _, err := stores["obj"].Put(ctx, "k/one", bytes.NewReader(plain), int64(len(plain)), blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
	rc, _, err := stores["obj2"].Get(ctx, "factory/k/one", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.HasPrefix(raw, []byte("EREN")) || bytes.Contains(raw, plain) {
		t.Fatalf("object is not encrypted at rest: %q", raw)
	}
	mfs, err := reg.Gather()
	if err != nil || len(mfs) == 0 {
		t.Fatalf("no storage metrics: %v", err)
	}

	bad := writeFile(t, "creds-open", []byte("access_key_id = a\nsecret_access_key = b\n"), 0o644)
	tgt.CredentialsFile = bad
	if _, err := BuildStore(ctx, tgt, BuildOptions{MasterKey: make([]byte, 32)}); err == nil ||
		!strings.Contains(err.Error(), "group or others") {
		t.Fatalf("world-readable credentials accepted: %v", err)
	}
}

func TestBuildStoreSFTP(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := writeFile(t, "id_ed25519", pem.EncodeToMemory(block), 0o600)
	srv := blobtest.StartSFTPWithKey(t, ssh.MarshalAuthorizedKey(sshPub))
	host, portS, err := net.SplitHostPort(srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portS)

	// Learn the server key once, as an operator would with ssh-keyscan.
	var hostKey ssh.PublicKey
	cfg := &ssh.ClientConfig{User: srv.User, Auth: []ssh.AuthMethod{ssh.Password(srv.Password)}, Timeout: 30 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error { hostKey = k; return nil }}
	if c, err := ssh.Dial("tcp", srv.Addr, cfg); err != nil {
		t.Fatalf("keyscan: %v", err)
	} else {
		_ = c.Close()
	}
	known := writeFile(t, "known_hosts", []byte(knownhosts.Line([]string{knownhosts.Normalize(srv.Addr)}, hostKey)+"\n"), 0o600)

	tgt := config.Target{Name: "box", Kind: "sftp", Host: host, Port: port, User: srv.User, KeyFile: keyFile,
		KnownHostsFile: known, Root: srv.Dir, MaxConns: 2, Encrypt: true}
	ctx := context.Background()
	s, err := BuildStore(ctx, tgt, BuildOptions{MasterKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if res := CheckStore(ctx, s); !res.OK {
		t.Fatalf("check failed: %+v", res)
	}
	CloseStores(map[string]blob.Store{"box": s})

	// A different host key must be rejected.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(other)
	tgt.KnownHostsFile = writeFile(t, "known_hosts_bad",
		[]byte(knownhosts.Line([]string{knownhosts.Normalize(srv.Addr)}, otherSigner.PublicKey())+"\n"), 0o600)
	s, err = BuildStore(ctx, tgt, BuildOptions{MasterKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer CloseStores(map[string]blob.Store{"box": s})
	if res := CheckStore(ctx, s); res.OK {
		t.Fatal("server with an unknown host key accepted")
	}

	tgt.KnownHostsFile = ""
	if _, err := BuildStore(ctx, tgt, BuildOptions{MasterKey: make([]byte, 32)}); err == nil {
		t.Fatal("missing known_hosts_file accepted")
	}
}
