package githooks

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// RunHook is the hook process ("ervisio-repo hook <name> [args]"): it
// forwards the hook to the server and returns the exit status for git.
// getenv reads the process environment; stderr reaches the pusher.
func RunHook(ctx context.Context, name string, args []string, stdin io.Reader, stderr io.Writer, getenv func(string) string) int {
	sock, tok := getenv(EnvSocket), getenv(EnvToken)
	switch name {
	case HookReferenceTransaction:
		// Only committed transactions are journaled; ref updates made
		// outside a session (hydration, maintenance) need no hook.
		if len(args) != 1 || args[0] != TxCommitted || tok == "" || sock == "" {
			_, _ = io.Copy(io.Discard, stdin)
			return 0
		}
	case HookPreReceive:
		if tok == "" || sock == "" {
			_, _ = io.Copy(io.Discard, stdin)
			fmt.Fprintln(stderr, "error: pushes must go through the ErvisioRepo server")
			return 1
		}
	default:
		fmt.Fprintf(stderr, "error: unknown hook %q\n", name)
		return 1
	}
	in, err := io.ReadAll(io.LimitReader(stdin, maxStdin+1))
	if err != nil || len(in) > maxStdin {
		fmt.Fprintln(stderr, "error: cannot read the hook input")
		return 1
	}
	ok, err := call(ctx, sock, request{
		Token: tok, Hook: name, Args: args, Stdin: string(in),
		Quarantine: getenv("GIT_QUARANTINE_PATH"),
	}, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: ErvisioRepo hook %s: %v\n", name, err)
		return 1
	}
	if !ok {
		return 1
	}
	return 0
}

func call(ctx context.Context, sock string, req request, stderr io.Writer) (bool, error) {
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, err := d.DialContext(dctx, "unix", sock)
	cancel()
	if err != nil {
		return false, fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return false, fmt.Errorf("send: %w", err)
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var rep reply
		if err := json.Unmarshal(sc.Bytes(), &rep); err != nil {
			return false, fmt.Errorf("bad reply: %w", err)
		}
		if rep.Done {
			if rep.Msg != "" {
				fmt.Fprintln(stderr, rep.Msg)
			}
			return rep.OK, nil
		}
		fmt.Fprintln(stderr, rep.Msg)
	}
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("receive: %w", err)
	}
	return false, fmt.Errorf("receive: %w", io.ErrUnexpectedEOF)
}
