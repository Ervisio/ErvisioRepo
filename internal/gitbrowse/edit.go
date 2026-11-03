package gitbrowse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
)

// Web edit operations.
const (
	OpCreate = "create"
	OpUpdate = "update"
	OpDelete = "delete"
)

// ErrQuota: a web edit would exceed the owner's storage quota.
var ErrQuota = errors.New("storage quota exceeded")

// MaxCommitMessage bounds the message of a web edit.
const MaxCommitMessage = 64 << 10

// editTimeout bounds each git command of a web edit.
const editTimeout = time.Minute

// Edit is a web edit of one file.
type Edit struct {
	Op   string // OpCreate, OpUpdate or OpDelete
	Path string
	// Content is the new content (create, update).
	Content []byte
	// Branch is the branch the edit starts from; empty = the default branch.
	Branch string
	// NewBranch, when set, receives the commit instead of Branch.
	NewBranch string
	// ExpectedOID is the blob the editor started from (update, delete);
	// a different current blob is a conflict. Empty skips the check.
	ExpectedOID string
	Message     string // empty = "<Op> <Path>"
	AuthorID    uuid.UUID

	fileMode string
}

// EditResult is the commit of a web edit.
type EditResult struct {
	Branch string
	Commit Commit
}

// treeEntry is one line of ls-tree/mktree.
type treeEntry struct {
	mode, typ, oid, name string
}

// Edit commits e without a working tree (hash-object, mktree, commit-tree)
// and moves the branch with a compare-and-swap update-ref. It holds the
// Write lease and runs update-ref inside a githooks session, so that the
// journal records the change like a push.
func (s *Service) Edit(ctx context.Context, repo Repo, e Edit) (EditResult, error) {
	var res EditResult
	if err := s.checkEdit(&e, repo); err != nil {
		return res, err
	}
	if s.q == nil {
		return res, fmt.Errorf("%w: web edits need the database", ErrUnavailable)
	}
	author, err := s.identity(ctx, e.AuthorID)
	if err != nil {
		return res, err
	}
	l, err := s.o.Tier.Acquire(ctx, repo.ID, gitstore.Write)
	if err != nil {
		return res, fmt.Errorf("acquire repository: %w", err)
	}
	defer l.Release()
	rp := l.Path

	parent, err := branchTip(ctx, rp, e.Branch)
	if err != nil {
		return res, err
	}
	if parent == "" && (e.Branch != repo.DefaultBranch || e.NewBranch != "" || s.hasCommits(ctx, rp)) {
		return res, notFoundf("branch %q", e.Branch)
	}
	target := e.Branch
	if e.NewBranch != "" {
		target = e.NewBranch
		if tip, err := branchTip(ctx, rp, target); err != nil {
			return res, err
		} else if tip != "" {
			return res, fmt.Errorf("%w: branch %q already exists", ErrConflict, target)
		}
	}
	if err := s.checkPath(ctx, rp, parent, &e); err != nil {
		return res, err
	}

	var entry *treeEntry
	if e.Op != OpDelete {
		if n := int64(len(e.Content)); n > 0 {
			err := quota.Check(ctx, s.q, repo.OwnerID, quota.Delta{Bytes: n})
			var ex *quota.ExceededError
			if errors.As(err, &ex) {
				return res, fmt.Errorf("%w: %d of %d bytes used", ErrQuota, ex.Used, ex.Limit)
			}
			if err != nil {
				return res, fmt.Errorf("check quota: %w", err)
			}
		}
		oid, err := gitIn(ctx, rp, e.Content, nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return res, fmt.Errorf("write blob: %w", err)
		}
		entry = &treeEntry{mode: e.mode(), typ: "blob", oid: oid}
	}
	baseTree := ""
	if parent != "" {
		baseTree = parent + "^{tree}"
	}
	tree, err := writeTree(ctx, rp, baseTree, strings.Split(e.Path, "/"), entry)
	if err != nil {
		return res, err
	}
	if tree == "" {
		if tree, err = mktree(ctx, rp, nil); err != nil {
			return res, err
		}
	}
	if parent != "" {
		old, err := run(ctx, rp, editTimeout, "rev-parse", "--verify", parent+"^{tree}")
		if err != nil {
			return res, fmt.Errorf("read tree: %w", err)
		}
		if strings.TrimSpace(string(old)) == tree {
			return res, invalidf("the edit changes nothing")
		}
	}

	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	commit, err := gitIn(ctx, rp, []byte(e.Message), author, append(args, "-F", "-")...)
	if err != nil {
		return res, fmt.Errorf("write commit: %w", err)
	}

	ref := "refs/heads/" + target
	cmd := "create " + ref + " " + commit + "\n"
	if e.NewBranch == "" && parent != "" {
		cmd = "update " + ref + " " + commit + " " + parent + "\n"
	}
	var env []string
	if s.o.Hooks != nil {
		sess, err := s.o.Hooks.Begin(ctx, l, e.AuthorID, "web")
		if err != nil {
			return res, fmt.Errorf("begin hook session: %w", err)
		}
		defer sess.Close()
		env = sess.Env()
	}
	if _, err := gitIn(ctx, rp, []byte(cmd), env, "update-ref", "--stdin"); err != nil {
		if exitCode(err) == 128 {
			return res, fmt.Errorf("%w: branch %q changed, reload and retry", ErrConflict, target)
		}
		return res, fmt.Errorf("update ref: %w", err)
	}
	s.log.InfoContext(ctx, "web edit committed", "repo", repo.ID, "user", e.AuthorID,
		"op", e.Op, "branch", target, "commit", commit)

	cs, err := commitsOf(ctx, rp, 1, commit)
	if err != nil || len(cs) == 0 {
		return EditResult{Branch: target, Commit: Commit{SHA: commit}}, nil //nolint:nilerr // the edit is committed
	}
	return EditResult{Branch: target, Commit: cs[0]}, nil
}

// checkEdit validates and normalises the arguments of an edit.
func (s *Service) checkEdit(e *Edit, repo Repo) error {
	switch e.Op {
	case OpCreate, OpUpdate, OpDelete:
	default:
		return invalidf("unknown operation %q", e.Op)
	}
	if e.AuthorID == uuid.Nil {
		return invalidf("an author is required")
	}
	if e.Path == "" {
		return invalidf("path is required")
	}
	if err := git.ValidateTreePath(e.Path); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if e.Branch == "" {
		e.Branch = repo.DefaultBranch
	}
	if err := git.ValidateBranchName(e.Branch); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if e.NewBranch != "" {
		if err := git.ValidateBranchName(e.NewBranch); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	if e.ExpectedOID != "" {
		if err := git.ValidateObjectID(e.ExpectedOID); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	if e.Op == OpDelete {
		e.Content = nil
	} else if int64(len(e.Content)) > s.o.MaxEditSize {
		return fmt.Errorf("%w: the file exceeds %d bytes; use git or Git LFS", ErrTooLarge, s.o.MaxEditSize)
	}
	e.Message = strings.TrimSpace(strings.ReplaceAll(e.Message, "\r\n", "\n"))
	if e.Message == "" {
		e.Message = map[string]string{OpCreate: "Create ", OpUpdate: "Update ", OpDelete: "Delete "}[e.Op] + e.Path
	}
	if len(e.Message) > MaxCommitMessage || strings.ContainsRune(e.Message, 0) || !utf8.ValidString(e.Message) {
		return invalidf("the commit message must be valid UTF-8 up to %d bytes", MaxCommitMessage)
	}
	e.Message += "\n"
	return nil
}

// mode is the mode of the written file: the mode of the replaced file
// (set by checkPath), 100644 for a new one.
func (e *Edit) mode() string { return e.fileMode }

// checkPath checks the current state of the path in the parent commit.
func (s *Service) checkPath(ctx context.Context, rp, parent string, e *Edit) error {
	e.fileMode = "100644"
	cur := treeEntry{}
	if parent != "" {
		out, err := run(ctx, rp, editTimeout, "ls-tree", "-z", parent, "--", e.Path)
		if err != nil {
			return fmt.Errorf("read tree: %w", err)
		}
		if line := strings.TrimSuffix(string(out), "\x00"); line != "" {
			if ent, ok := parseTreeLine(line); ok && ent.name == e.Path {
				cur = ent
			}
		}
	}
	switch e.Op {
	case OpCreate:
		if cur.oid != "" {
			return fmt.Errorf("%w: %q already exists", ErrConflict, e.Path)
		}
		return nil
	}
	if cur.oid == "" {
		return notFoundf("path %q", e.Path)
	}
	if e.ExpectedOID != "" && e.ExpectedOID != cur.oid {
		return fmt.Errorf("%w: %q changed since it was opened", ErrConflict, e.Path)
	}
	switch {
	case cur.typ == "blob" && (cur.mode == "100644" || cur.mode == "100755"):
		e.fileMode = cur.mode
	case e.Op == OpDelete && cur.typ == "blob": // a symlink
	default:
		return invalidf("%q is not a regular file", e.Path)
	}
	return nil
}

// parseTreeLine parses "<mode> <type> <oid>\t<name>".
func parseTreeLine(line string) (treeEntry, bool) {
	meta, name, ok := strings.Cut(line, "\t")
	f := strings.Fields(meta)
	if !ok || len(f) != 3 {
		return treeEntry{}, false
	}
	return treeEntry{mode: f[0], typ: f[1], oid: f[2], name: name}, true
}

// writeTree writes the tree base (a tree-ish, "" = empty) with the entry at
// parts replaced by entry (nil removes it) and returns its id, or "" when
// the result is empty. Empty directories are pruned.
func writeTree(ctx context.Context, rp, base string, parts []string, entry *treeEntry) (string, error) {
	var entries []treeEntry
	if base != "" {
		out, err := run(ctx, rp, editTimeout, "ls-tree", "-z", base)
		if err != nil {
			return "", fmt.Errorf("read tree: %w", err)
		}
		for _, line := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
			if ent, ok := parseTreeLine(line); ok {
				entries = append(entries, ent)
			}
		}
	}
	name := parts[0]
	idx := -1
	for i, ent := range entries {
		if ent.name == name {
			idx = i
			break
		}
	}
	var repl *treeEntry
	if len(parts) == 1 {
		if entry != nil {
			repl = &treeEntry{mode: entry.mode, typ: entry.typ, oid: entry.oid, name: name}
		}
	} else {
		sub := ""
		if idx >= 0 {
			if entries[idx].typ != "tree" {
				return "", invalidf("%q is not a directory", name)
			}
			sub = entries[idx].oid
		}
		oid, err := writeTree(ctx, rp, sub, parts[1:], entry)
		if err != nil {
			return "", err
		}
		if oid != "" {
			repl = &treeEntry{mode: "040000", typ: "tree", oid: oid, name: name}
		}
	}
	switch {
	case idx >= 0 && repl != nil:
		entries[idx] = *repl
	case idx >= 0:
		entries = append(entries[:idx], entries[idx+1:]...)
	case repl != nil:
		entries = append(entries, *repl)
	}
	if len(entries) == 0 {
		return "", nil
	}
	return mktree(ctx, rp, entries)
}

// mktree writes a tree from its entries (git mktree sorts them).
func mktree(ctx context.Context, rp string, entries []treeEntry) (string, error) {
	var in bytes.Buffer
	for _, ent := range entries {
		fmt.Fprintf(&in, "%s %s %s\t%s\x00", ent.mode, ent.typ, ent.oid, ent.name)
	}
	oid, err := gitIn(ctx, rp, in.Bytes(), nil, "mktree", "-z")
	if err != nil {
		return "", fmt.Errorf("write tree: %w", err)
	}
	return oid, nil
}

// gitIn runs git with stdin and returns the trimmed stdout.
func gitIn(ctx context.Context, rp string, stdin []byte, env []string, args ...string) (string, error) {
	var out bytes.Buffer
	err := git.Exec(ctx, git.Cmd{RepoPath: rp, Args: args, Env: env, Stdin: bytes.NewReader(stdin),
		Stdout: &out, Timeout: editTimeout})
	return strings.TrimSpace(out.String()), err
}

// identity returns the author and committer environment of a user: the
// display name and the verified e-mail, or a no-reply address.
func (s *Service) identity(ctx context.Context, userID uuid.UUID) ([]string, error) {
	u, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load author: %w", err)
	}
	name := identClean(u.DisplayName)
	if name == "" {
		name = u.Username
	}
	email := u.Username + "@" + s.o.NoReplyDomain
	if u.Email.Valid && u.EmailVerifiedAt.Valid {
		if e := identClean(u.Email.String); e != "" {
			email = e
		}
	}
	return []string{
		"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email,
	}, nil
}

// identClean drops the characters git does not accept in an identity.
func identClean(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '<' || r == '>' || r < ' ' {
			return -1
		}
		return r
	}, s))
}
