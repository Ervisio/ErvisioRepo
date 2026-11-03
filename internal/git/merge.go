package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

// mergeTimeout bounds one merge, commit or ref operation.
const mergeTimeout = 5 * time.Minute

// MaxConflictFiles bounds the conflicting paths a MergeResult lists.
const MaxConflictFiles = 500

// MergeResult is the outcome of a three-way merge computed without a
// worktree (MergeTree).
type MergeResult struct {
	Tree      string   // the merged tree; with conflicts, the tree with conflict markers
	Clean     bool     // no conflicts
	Conflicts []string // conflicting paths, at most MaxConflictFiles
}

// MergeTree merges the commits ours and theirs with git merge-tree
// --write-tree, writing the resulting tree (and blobs) into the object
// database but no ref. base forces the merge base (a commit); empty lets
// git compute it.
func MergeTree(ctx context.Context, repoPath string, env []string, ours, theirs, base string) (MergeResult, error) {
	for _, id := range []string{ours, theirs} {
		if err := ValidateObjectID(id); err != nil {
			return MergeResult{}, err
		}
	}
	args := []string{"merge-tree", "--write-tree", "-z", "--name-only", "--no-messages"}
	if base != "" {
		if err := ValidateObjectID(base); err != nil {
			return MergeResult{}, err
		}
		args = append(args, "--merge-base="+base)
	}
	args = append(args, ours, theirs)
	out, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Args: args, Timeout: mergeTimeout})
	var ee *sys.ExitError
	conflict := errors.As(err, &ee) && ee.Code == 1
	if err != nil && !conflict {
		return MergeResult{}, err
	}
	fields := bytes.Split(bytes.TrimRight(out, "\x00"), []byte{0})
	tree := strings.TrimSpace(string(fields[0]))
	if ValidateObjectID(tree) != nil {
		return MergeResult{}, fmt.Errorf("git merge-tree: unexpected output %q", truncate(out, 200))
	}
	res := MergeResult{Tree: tree, Clean: !conflict}
	if conflict {
		seen := map[string]bool{}
		for _, f := range fields[1:] {
			p := string(f)
			if p == "" {
				break // the informational messages follow an empty field
			}
			if !seen[p] && len(res.Conflicts) < MaxConflictFiles {
				seen[p] = true
				res.Conflicts = append(res.Conflicts, p)
			}
		}
		if res.Conflicts == nil {
			res.Conflicts = []string{}
		}
	}
	return res, nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}

// CommitTree writes a commit of tree with parents and message. env carries
// the GIT_AUTHOR_* and GIT_COMMITTER_* variables of the identities.
func CommitTree(ctx context.Context, repoPath string, env []string, tree string, parents []string, message string) (string, error) {
	if err := ValidateObjectID(tree); err != nil {
		return "", err
	}
	args := []string{"commit-tree", tree}
	for _, p := range parents {
		if err := ValidateObjectID(p); err != nil {
			return "", err
		}
		args = append(args, "-p", p)
	}
	args = append(args, "-F", "-")
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}
	out, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Args: args, Stdin: strings.NewReader(message), Timeout: mergeTimeout})
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	if err := ValidateObjectID(id); err != nil {
		return "", fmt.Errorf("git commit-tree: %w", err)
	}
	return id, nil
}

// RefChange is one ref update of UpdateRefs. An empty New deletes the ref;
// an empty Old skips the check of the current value, a zero Old requires the
// ref not to exist.
type RefChange struct {
	Ref string
	New string
	Old string
}

// UpdateRefs applies changes atomically with git update-ref --stdin. A
// failed compare-and-swap (or a hook refusal) fails the whole transaction
// with a *sys.ExitError.
func UpdateRefs(ctx context.Context, repoPath string, env []string, changes []RefChange) error {
	var in strings.Builder
	in.WriteString("start\n")
	for _, c := range changes {
		if err := ValidateRefName(c.Ref); err != nil {
			return err
		}
		for _, id := range []string{c.New, c.Old} {
			if id != "" {
				if err := ValidateObjectID(id); err != nil {
					return err
				}
			}
		}
		if c.New == "" {
			fmt.Fprintf(&in, "delete %s", c.Ref)
		} else {
			fmt.Fprintf(&in, "update %s %s", c.Ref, c.New)
		}
		if c.Old != "" {
			fmt.Fprintf(&in, " %s", c.Old)
		}
		in.WriteString("\n")
	}
	in.WriteString("prepare\ncommit\n")
	return Exec(ctx, Cmd{RepoPath: repoPath, Env: env, Args: []string{"update-ref", "--stdin"},
		Stdin: strings.NewReader(in.String()), Timeout: mergeTimeout})
}

// FetchBranches fetches refspecs from the repository at srcPath (another
// leased bare repository) into the one at repoPath, without tags unless a
// refspec names them. Each refspec is <src>[:<dst>], optionally prefixed by
// +, with full ref names (wildcards allowed); a refspec without <dst> only
// copies the objects.
func FetchBranches(ctx context.Context, repoPath string, env []string, srcPath string, refspecs ...string) error {
	if !filepath.IsAbs(srcPath) {
		return ErrNotRepoPath
	}
	if len(refspecs) == 0 {
		return fmt.Errorf("%w refspec: none given", ErrInvalid)
	}
	for _, rs := range refspecs {
		src, dst, ok := strings.Cut(strings.TrimPrefix(rs, "+"), ":")
		names := []string{src}
		if ok {
			names = append(names, dst)
		}
		for _, r := range names {
			if err := ValidateRefName(strings.ReplaceAll(r, "*", "x")); err != nil {
				return err
			}
		}
	}
	args := append([]string{"fetch", "--no-tags", "--no-write-fetch-head", "--quiet", "--no-auto-gc", "--", srcPath}, refspecs...)
	return Exec(ctx, Cmd{RepoPath: repoPath, Env: env, Args: args, Timeout: DefaultTimeout})
}

// CommitMeta is the identity and message of a commit, as needed to replay
// it (rebase).
type CommitMeta struct {
	AuthorName  string
	AuthorEmail string
	AuthorDate  string // raw: <unix seconds> <tz offset>
	Message     string
	Parents     []string
}

// ReadCommitMeta returns the metadata of commit id.
func ReadCommitMeta(ctx context.Context, repoPath string, env []string, id string) (CommitMeta, error) {
	if err := ValidateObjectID(id); err != nil {
		return CommitMeta{}, err
	}
	out, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Timeout: historyTimeout,
		Args: []string{"log", "-1", "--no-show-signature", "--date=raw", "--format=%an%x00%ae%x00%ad%x00%P%x00%B", id}})
	if err != nil {
		return CommitMeta{}, err
	}
	f := strings.SplitN(string(out), "\x00", 5)
	if len(f) != 5 {
		return CommitMeta{}, fmt.Errorf("git log: unexpected output for %s", id)
	}
	return CommitMeta{AuthorName: f[0], AuthorEmail: f[1], AuthorDate: f[2], Parents: strings.Fields(f[3]),
		Message: strings.TrimRight(f[4], "\n") + "\n"}, nil
}

// RevList returns the commits reachable from tip and not from exclude,
// oldest first (topological), at most limit (more reports a truncation).
func RevList(ctx context.Context, repoPath string, env []string, exclude, tip string, limit int) (ids []string, more bool, err error) {
	for _, id := range []string{exclude, tip} {
		if err := ValidateObjectID(id); err != nil {
			return nil, false, err
		}
	}
	out, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Timeout: historyTimeout,
		Args: []string{"rev-list", "--reverse", "--topo-order", tip, "^" + exclude}})
	if err != nil {
		return nil, false, err
	}
	for line := range strings.Lines(string(out)) {
		if id := strings.TrimSpace(line); id != "" {
			if limit > 0 && len(ids) >= limit {
				return ids, true, nil
			}
			ids = append(ids, id)
		}
	}
	return ids, false, nil
}

// TreeOf returns the tree id of commit id.
func TreeOf(ctx context.Context, repoPath string, env []string, id string) (string, error) {
	if err := ValidateObjectID(id); err != nil {
		return "", err
	}
	out, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Timeout: historyTimeout,
		Args: []string{"rev-parse", "--verify", "--end-of-options", id + "^{tree}"}})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// DiffHunk is the position of one hunk of a unified diff.
type DiffHunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
}

// MaxDiffHunkFiles bounds the paths of one DiffHunks call.
const MaxDiffHunkFiles = 1000

// DiffHunks returns, for each of paths, the hunks (without context lines)
// that turn its content in commit from into its content in commit to.
// Paths without changes are absent from the result.
func DiffHunks(ctx context.Context, repoPath string, env []string, from, to string, paths []string) (map[string][]DiffHunk, error) {
	for _, id := range []string{from, to} {
		if err := ValidateObjectID(id); err != nil {
			return nil, err
		}
	}
	if len(paths) == 0 {
		return map[string][]DiffHunk{}, nil
	}
	if len(paths) > MaxDiffHunkFiles {
		return nil, fmt.Errorf("%w paths: more than %d", ErrInvalid, MaxDiffHunkFiles)
	}
	args := []string{"diff", "--no-color", "--no-ext-diff", "--no-renames", "-U0", from, to, "--"}
	for _, p := range paths {
		if err := ValidateTreePath(p); err != nil {
			return nil, err
		}
		args = append(args, ":(literal)"+p)
	}
	out, err := Output(ctx, Cmd{RepoPath: repoPath, Env: env, Args: args, Timeout: historyTimeout})
	if err != nil {
		return nil, err
	}
	res := map[string][]DiffHunk{}
	cur := ""
	for line := range strings.Lines(string(out)) {
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			cur = ""
		case strings.HasPrefix(line, "--- "):
			if p, ok := strings.CutPrefix(line, "--- a/"); ok && cur == "" {
				cur = p
			}
		case strings.HasPrefix(line, "+++ "):
			if p, ok := strings.CutPrefix(line, "+++ b/"); ok {
				cur = p
			}
		case strings.HasPrefix(line, "@@ ") && cur != "":
			if h, ok := parseHunkHeader(line); ok {
				res[cur] = append(res[cur], h)
			}
		}
	}
	return res, nil
}

// parseHunkHeader parses "@@ -a[,b] +c[,d] @@...".
func parseHunkHeader(line string) (DiffHunk, bool) {
	f := strings.Fields(line)
	if len(f) < 3 || !strings.HasPrefix(f[1], "-") || !strings.HasPrefix(f[2], "+") {
		return DiffHunk{}, false
	}
	parse := func(s string) (int, int, bool) {
		a, b, found := strings.Cut(s[1:], ",")
		start, err := strconv.Atoi(a)
		if err != nil {
			return 0, 0, false
		}
		n := 1
		if found {
			if n, err = strconv.Atoi(b); err != nil {
				return 0, 0, false
			}
		}
		return start, n, true
	}
	os, ol, ok1 := parse(f[1])
	ns, nl, ok2 := parse(f[2])
	if !ok1 || !ok2 {
		return DiffHunk{}, false
	}
	return DiffHunk{OldStart: os, OldLines: ol, NewStart: ns, NewLines: nl}, true
}
