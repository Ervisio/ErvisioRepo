package sftp

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/blobtest"
)

type rwc struct {
	io.Reader
	io.WriteCloser
}

// pipeDial serves dir with an in-process SFTP server.
func pipeDial(t *testing.T, dir string, dials *atomic.Int32) DialFunc {
	return func(context.Context) (*sftp.Client, func() error, error) {
		if dials != nil {
			dials.Add(1)
		}
		cr, sw := io.Pipe()
		sr, cw := io.Pipe()
		srv, err := sftp.NewServer(rwc{sr, sw}, sftp.WithServerWorkingDirectory(dir))
		if err != nil {
			return nil, nil, err
		}
		go func() { _ = srv.Serve() }()
		c, err := sftp.NewClientPipe(cr, cw)
		if err != nil {
			_ = srv.Close()
			return nil, nil, err
		}
		return c, func() error {
			// Closing the pipes first lets both the server loop and the
			// client receive loop return.
			_ = cw.Close()
			_ = sw.Close()
			_ = sr.Close()
			_ = cr.Close()
			_ = c.Close()
			return nil
		}, nil
	}
}

func newPipeStore(t *testing.T, dir string, maxConns int, dials *atomic.Int32) *Store {
	t.Helper()
	s, err := New(Options{Name: "box", Root: "data", MaxConns: maxConns, Dial: pipeDial(t, dir, dials)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestConformanceInProcess(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Store { return newPipeStore(t, t.TempDir(), 4, nil) }, blobtest.Options{})
}

func TestConformanceOpenSSH(t *testing.T) {
	srv := blobtest.StartSFTP(t)
	blobtest.Run(t, func(t *testing.T) blob.Store {
		s, err := New(Options{
			Name: "box", Addr: srv.Addr, User: srv.User,
			Auth: []ssh.AuthMethod{ssh.Password(srv.Password)},
			// Throwaway container with a random host key.
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // test container
			Root:            srv.Dir + "/erepo",
			MaxConns:        4,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}, blobtest.Options{})
}

func TestPoolReuseAndLimit(t *testing.T) {
	var dials atomic.Int32
	s := newPipeStore(t, t.TempDir(), 2, &dials)
	ctx := context.Background()
	for range 20 {
		if _, err := s.Put(ctx, "k", strings.NewReader("v"), 1, blob.PutOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	if n := dials.Load(); n != 1 {
		t.Fatalf("sequential use opened %d sessions", n)
	}
	// Two open readers hold both slots: a third operation must wait.
	r1, _, err := s.Get(ctx, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	r2, _, err := s.Get(ctx, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Stat(cctx, "k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("stat with exhausted pool: %v", err)
	}
	_ = r1.Close()
	_ = r2.Close()
	if n := dials.Load(); n > 2 {
		t.Fatalf("opened %d sessions with MaxConns=2", n)
	}
	if _, err := s.Stat(ctx, "k"); err != nil {
		t.Fatal(err)
	}
}

func TestReaderFailureKeepsSession(t *testing.T) {
	var dials atomic.Int32
	s := newPipeStore(t, t.TempDir(), 1, &dials)
	ctx := context.Background()
	r := &blobtest.FailingReader{R: blobtest.Reader(1, 1<<20), N: 1000, Err: blobtest.ErrSimulated}
	if _, err := s.Put(ctx, "k", r, 1<<20, blob.PutOpts{}); !errors.Is(err, blobtest.ErrSimulated) {
		t.Fatalf("put: %v", err)
	}
	if _, err := s.Put(ctx, "k", strings.NewReader("ok"), 2, blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
	if n := dials.Load(); n != 1 {
		t.Fatalf("a reader failure discarded the session (%d dials)", n)
	}
}

func TestStaleLockBroken(t *testing.T) {
	dir := t.TempDir()
	s := newPipeStore(t, dir, 2, nil)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(dir, "data", "m"), 0o750); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, "data", "m", blob.ReservedPrefix+"lock-manifest")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * StaleLockAge)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutConditional(ctx, "m/manifest", strings.NewReader("x"), 1, blob.Condition{IfNoneMatch: true}, blob.PutOpts{}); err != nil {
		t.Fatalf("stale lock not broken: %v", err)
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock left behind: %v", err)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Options{Addr: "h:22", User: "u"}); err == nil {
		t.Fatal("missing host key callback accepted")
	}
	if _, err := New(Options{HostKeyCallback: ssh.InsecureIgnoreHostKey()}); err == nil { //nolint:gosec // validation only
		t.Fatal("missing addr accepted")
	}
}
