package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

// historyTimeout bounds the history queries of the ref update checks.
const historyTimeout = 2 * time.Minute

// IsAncestor reports whether commit a is an ancestor of (or equal to) b.
// env holds extra variables, for example a push quarantine.
func IsAncestor(ctx context.Context, repoPath string, env []string, a, b string) (bool, error) {
	if err := ValidateObjectID(a); err != nil {
		return false, err
	}
	if err := ValidateObjectID(b); err != nil {
		return false, err
	}
	err := Exec(ctx, Cmd{RepoPath: repoPath, Env: env, Args: []string{"merge-base", "--is-ancestor", a, b}, Timeout: historyTimeout})
	var ee *sys.ExitError
	if errors.As(err, &ee) && ee.Code == 1 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// NewCommits lists the commits reachable from tip that no ref of the
// repository reaches yet (the commits a ref update introduces), newest
// first, at most limit of them; more reports whether there are others.
// merges limits the list to merge commits.
func NewCommits(ctx context.Context, repoPath string, env []string, tip string, merges bool, limit int) (ids []string, more bool, err error) {
	if err := ValidateObjectID(tip); err != nil {
		return nil, false, err
	}
	if limit <= 0 {
		limit = 1
	}
	args := []string{"rev-list", "--max-count=" + strconv.Itoa(limit+1)}
	if merges {
		args = append(args, "--merges")
	}
	args = append(args, tip, "--not", "--all")
	out, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Args: args, Timeout: historyTimeout})
	if err != nil {
		return nil, false, err
	}
	for line := range strings.Lines(string(out)) {
		if id := strings.TrimSpace(line); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) > limit {
		return ids[:limit], true, nil
	}
	return ids, false, nil
}

// ChangedPaths lists the paths that differ between the trees of two
// commits (base may be empty for "no base": every path of head).
func ChangedPaths(ctx context.Context, repoPath string, env []string, base, head string, limit int) ([]string, error) {
	if err := ValidateObjectID(head); err != nil {
		return nil, err
	}
	args := []string{"diff-tree", "-r", "-z", "--name-only", "--no-commit-id"}
	if base == "" {
		args = append(args, "--root", head)
	} else {
		if err := ValidateObjectID(base); err != nil {
			return nil, err
		}
		args = append(args, base, head)
	}
	out, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Args: args, Timeout: historyTimeout})
	if err != nil {
		return nil, err
	}
	var paths []string
	for p := range bytes.SplitSeq(out, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		paths = append(paths, string(p))
		if limit > 0 && len(paths) >= limit {
			break
		}
	}
	return paths, nil
}

// RawCommits reads the raw content of the given commits, in order, with
// one git cat-file process. A missing object or one that is not a commit
// is an error.
func RawCommits(ctx context.Context, repoPath string, env []string, ids []string) ([][]byte, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var in bytes.Buffer
	for _, id := range ids {
		if err := ValidateObjectID(id); err != nil {
			return nil, err
		}
		in.WriteString(id)
		in.WriteByte('\n')
	}
	out, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Args: []string{"cat-file", "--batch"}, Stdin: &in, Timeout: historyTimeout})
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(bytes.NewReader(out))
	res := make([][]byte, 0, len(ids))
	for range ids {
		hdr, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("git cat-file: %w", err)
		}
		f := strings.Fields(hdr)
		if len(f) != 3 || f[1] != "commit" {
			return nil, fmt.Errorf("git cat-file: %w: not a commit: %s", ErrInvalid, strings.TrimSpace(hdr))
		}
		n, err := strconv.Atoi(f[2])
		if err != nil || n < 0 {
			return nil, fmt.Errorf("git cat-file: bad size %q", f[2])
		}
		buf := make([]byte, n+1) // content and the trailing LF
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, fmt.Errorf("git cat-file: %w", err)
		}
		res = append(res, buf[:n])
	}
	return res, nil
}

// ResolveRef returns the object id a ref points to, or "" when it does not
// exist.
func ResolveRef(ctx context.Context, repoPath string, env []string, ref string) (string, error) {
	if err := ValidateRefName(ref); err != nil {
		return "", err
	}
	out, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Args: []string{"for-each-ref", "--format=%(objectname)", ref}, Timeout: historyTimeout})
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(out)) {
		return strings.TrimSpace(line), nil
	}
	return "", nil
}

// ReadBlobAt returns the content of path in commit rev, or nil when the
// path does not exist there. Content beyond max bytes is an error.
func ReadBlobAt(ctx context.Context, repoPath string, env []string, rev, path string, maxSize int64) ([]byte, error) {
	if err := ValidateRevision(rev); err != nil {
		return nil, err
	}
	if err := ValidateTreePath(path); err != nil {
		return nil, err
	}
	name := rev + ":" + path + "\n"
	out, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Args: []string{"cat-file", "--batch-check"},
		Stdin: strings.NewReader(name), Timeout: historyTimeout})
	if err != nil {
		return nil, err
	}
	f := strings.Fields(string(out))
	if len(f) != 3 || f[1] != "blob" {
		return nil, nil // missing, or not a file
	}
	n, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("git cat-file: bad object header %q", strings.TrimSpace(string(out)))
	}
	if maxSize > 0 && n > maxSize {
		return nil, fmt.Errorf("git cat-file: %s: %w: larger than %d bytes", path, ErrInvalid, maxSize)
	}
	body, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Args: []string{"cat-file", "blob", f[0]}, Timeout: historyTimeout})
	if err != nil {
		return nil, err
	}
	return body, nil
}

// Output runs c and returns its stdout.
func Output(ctx context.Context, c Cmd) ([]byte, error) {
	var out bytes.Buffer
	c.Stdout = &out
	err := Exec(ctx, c)
	return out.Bytes(), err
}

// MergeBase returns the best common ancestor of a and b, or "" when they
// have none.
func MergeBase(ctx context.Context, repoPath string, env []string, a, b string) (string, error) {
	if err := ValidateObjectID(a); err != nil {
		return "", err
	}
	if err := ValidateObjectID(b); err != nil {
		return "", err
	}
	out, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Args: []string{"merge-base", a, b}, Timeout: historyTimeout})
	var ee *sys.ExitError
	if errors.As(err, &ee) && ee.Code == 1 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
