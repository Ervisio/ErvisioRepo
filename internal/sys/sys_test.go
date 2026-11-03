package sys

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunStdout(t *testing.T) {
	out, err := Run(context.Background(), "echo", "a b", "$HOME")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "a b $HOME\n" {
		t.Fatalf("no shell expansion expected, got %q", got)
	}
}

func TestEnvNotInherited(t *testing.T) {
	t.Setenv("EREPO_SYS_SECRET", "x")
	var out bytes.Buffer
	if err := Exec(context.Background(), Cmd{Argv: []string{"env"}, Env: []string{"A=1"}, Stdout: &out}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.Contains(s, "EREPO_SYS_SECRET") {
		t.Fatal("environment leaked")
	}
	if !strings.Contains(s, "A=1\n") || !strings.Contains(s, "PATH=") {
		t.Fatalf("env = %q", s)
	}
}

func TestExitError(t *testing.T) {
	err := Exec(context.Background(), Cmd{Argv: []string{"sh", "-c", "echo boom >&2; exit 3"}})
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 || !strings.Contains(ee.Stderr, "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestTimeout(t *testing.T) {
	start := time.Now()
	err := Exec(context.Background(), Cmd{Argv: []string{"sleep", "10"}, Timeout: 100 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout not enforced")
	}
}

func TestValidate(t *testing.T) {
	for _, argv := range [][]string{nil, {""}, {"echo", "a\x00b"}} {
		if err := Validate(argv); !errors.Is(err, ErrInvalidArgv) {
			t.Errorf("Validate(%q) = %v", argv, err)
		}
	}
	if _, err := Run(context.Background()); !errors.Is(err, ErrInvalidArgv) {
		t.Fatal(err)
	}
}

func TestTailBuffer(t *testing.T) {
	tb := &tailBuffer{max: 4}
	_, _ = tb.Write([]byte("abc"))
	_, _ = tb.Write([]byte("defg"))
	if tb.String() != "defg" {
		t.Fatal(tb.String())
	}
}
