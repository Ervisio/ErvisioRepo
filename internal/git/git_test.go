package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testIdent = []string{
	"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

// newRepo creates a bare repository with one commit on main holding
// README with content, and returns its path and the commit id.
func newRepo(t *testing.T, content string) (string, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ab", "repo.git")
	if err := InitBare(ctx, path, "main"); err != nil {
		t.Fatal(err)
	}
	var blob bytes.Buffer
	if err := Exec(ctx, Cmd{RepoPath: path, Args: []string{"hash-object", "-w", "--stdin"},
		Stdin: strings.NewReader(content), Stdout: &blob}); err != nil {
		t.Fatal(err)
	}
	var tree bytes.Buffer
	if err := Exec(ctx, Cmd{RepoPath: path, Args: []string{"mktree"},
		Stdin: strings.NewReader("100644 blob " + strings.TrimSpace(blob.String()) + "\tREADME\n"), Stdout: &tree}); err != nil {
		t.Fatal(err)
	}
	var commit bytes.Buffer
	if err := Exec(ctx, Cmd{RepoPath: path, Args: []string{"commit-tree", "-m", "init", strings.TrimSpace(tree.String())},
		Env: testIdent, Stdout: &commit}); err != nil {
		t.Fatal(err)
	}
	oid := strings.TrimSpace(commit.String())
	if _, err := Run(ctx, path, "update-ref", "refs/heads/main", oid); err != nil {
		t.Fatal(err)
	}
	return path, oid
}

func TestInitBare(t *testing.T) {
	path, oid := newRepo(t, "hello\n")
	out, err := Run(context.Background(), path, "rev-parse", "--is-bare-repository", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "true\n"+oid+"\n" {
		t.Fatalf("rev-parse = %q", got)
	}
	if _, err := os.Stat(filepath.Join(path, "hooks", "pre-receive.sample")); err == nil {
		t.Fatal("sample hooks installed")
	}
	if err := InitBare(context.Background(), path, "main"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second init = %v", err)
	}
	if err := InitBare(context.Background(), "rel/path.git", "main"); !errors.Is(err, ErrNotRepoPath) {
		t.Fatalf("relative = %v", err)
	}
	if err := InitBare(context.Background(), filepath.Join(t.TempDir(), "x.git"), "-bad"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad branch = %v", err)
	}
}

func TestEnvIsolation(t *testing.T) {
	path, _ := newRepo(t, "x")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'user.name'='leak'")
	t.Setenv("GIT_DIR", "/nonexistent")
	out, err := Run(context.Background(), path, "config", "--show-origin", "--list")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "file:config") && !strings.HasPrefix(line, "file:"+path) {
			t.Errorf("config from outside the repository: %q", line)
		}
	}
	env := strings.Join(Env(path), "\n")
	for _, want := range []string{"GIT_CONFIG_NOSYSTEM=1", "HOME=", "GIT_DIR=" + path} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
}

func TestRunError(t *testing.T) {
	path, _ := newRepo(t, "x")
	_, err := Run(context.Background(), path, "rev-parse", "--verify", "refs/heads/nope")
	if err == nil || !strings.Contains(err.Error(), "git rev-parse") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Run(context.Background(), "relative", "status"); !errors.Is(err, ErrNotRepoPath) {
		t.Fatal(err)
	}
}

func TestCatFilePool(t *testing.T) {
	path, oid := newRepo(t, "hello\n")
	p := NewPool(PoolOptions{IdleTimeout: 50 * time.Millisecond})
	defer p.Close()
	ctx := context.Background()

	info, err := p.Info(ctx, path, oid)
	if err != nil || info.Type != "commit" || info.OID != oid {
		t.Fatalf("info = %+v, %v", info, err)
	}
	info, data, err := p.Object(ctx, path, "main:README", 0)
	if err != nil || info.Type != "blob" || string(data) != "hello\n" {
		t.Fatalf("object = %+v %q %v", info, data, err)
	}
	if _, _, err := p.Object(ctx, path, "main:README", 3); !errors.Is(err, ErrObjectTooLarge) {
		t.Fatalf("too large = %v", err)
	}
	if _, err := p.Info(ctx, path, "main:missing"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing = %v", err)
	}
	if _, err := p.Info(ctx, path, "a\nb"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("newline = %v", err)
	}

	// The processes are reused.
	b, _ := p.Get(path)
	if b.check == nil || b.full == nil {
		t.Fatal("batch not reused")
	}
	p.Put(b)

	// A cancelled context breaks the batch, which is not pooled again.
	b, _ = p.Get(path)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := b.Info(cctx, oid); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	p.Put(b)
	if b2, _ := p.Get(path); b2 == b {
		t.Fatal("broken batch reused")
	}

	// Idle processes are reaped.
	b, _ = p.Get(path)
	_, _ = b.Info(ctx, oid)
	p.Put(b)
	time.Sleep(200 * time.Millisecond)
	p.mu.Lock()
	n := len(p.idle[path])
	p.mu.Unlock()
	if n != 0 {
		t.Fatal("idle batch not reaped")
	}
	p.Forget(path)
}

func TestPoolClosed(t *testing.T) {
	p := NewPool(PoolOptions{})
	p.Close()
	p.Close()
	if _, err := p.Get("/x"); !errors.Is(err, ErrPoolClosed) {
		t.Fatal(err)
	}
}
