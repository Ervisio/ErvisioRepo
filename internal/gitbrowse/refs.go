package gitbrowse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

// Ref kinds.
const (
	KindBranch = "branch"
	KindTag    = "tag"
	KindCommit = "commit"
)

// Resolved is a ref resolved to a commit.
type Resolved struct {
	Ref    string // as given (the default branch when empty)
	Kind   string // KindBranch, KindTag or KindCommit
	Name   string // full ref name; empty for KindCommit
	Commit string // full commit id
}

// Resolve resolves ref (a branch, a tag, a full or abbreviated commit id,
// a full ref name; empty = the default branch) to a commit.
func (s *Service) Resolve(ctx context.Context, repo Repo, ref string) (Resolved, error) {
	var r Resolved
	err := s.read(ctx, repo, func(path string) error {
		var err error
		r, err = s.resolve(ctx, path, repo, ref)
		return err
	})
	return r, err
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return s != ""
}

// exitCode returns the exit code of a failed git command, or -1.
func exitCode(err error) int {
	var ee *sys.ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

func (s *Service) resolve(ctx context.Context, path string, repo Repo, ref string) (Resolved, error) {
	if ref == "" {
		ref = repo.DefaultBranch
	}
	r := Resolved{Ref: ref}
	if len(ref) > 255 {
		return r, invalidf("ref too long")
	}
	var patterns []string
	switch {
	case strings.HasPrefix(ref, "refs/"):
		if err := git.ValidateRefName(ref); err != nil {
			return r, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		patterns = []string{ref}
	case git.ValidateBranchName(ref) == nil:
		patterns = []string{"refs/heads/" + ref, "refs/tags/" + ref}
	case !isHex(ref):
		return r, invalidf("invalid ref %q", ref)
	}
	if len(patterns) > 0 {
		out, err := git.Run(ctx, path, append([]string{"for-each-ref",
			"--format=%(refname)%00%(objecttype)%00%(objectname)%00%(*objectname)%00%(*objecttype)"}, patterns...)...)
		if err != nil {
			return r, fmt.Errorf("resolve ref: %w", err)
		}
		found := map[string][]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Split(line, "\x00")
			if len(f) == 5 {
				found[f[0]] = f[1:]
			}
		}
		for _, p := range patterns {
			f, ok := found[p]
			if !ok {
				continue
			}
			commit := f[1]
			if f[0] == "tag" {
				if f[3] != "commit" {
					continue // a tag of a tree or blob
				}
				commit = f[2]
			} else if f[0] != "commit" {
				continue
			}
			r.Name, r.Commit = p, commit
			switch {
			case strings.HasPrefix(p, "refs/heads/"):
				r.Kind = KindBranch
			case strings.HasPrefix(p, "refs/tags/"):
				r.Kind = KindTag
			default:
				r.Kind = KindCommit
			}
			return r, nil
		}
	}
	if !isHex(ref) || len(ref) < 4 || len(ref) > 64 {
		if ref == repo.DefaultBranch && !s.hasCommits(ctx, path) {
			return r, ErrEmpty
		}
		return r, notFoundf("ref %q", ref)
	}
	out, err := git.Run(ctx, path, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if exitCode(err) == 1 || exitCode(err) == 128 {
		return r, notFoundf("commit %q", ref)
	}
	if err != nil {
		return r, fmt.Errorf("resolve commit: %w", err)
	}
	r.Kind, r.Commit = KindCommit, string(bytes.TrimSpace(out))
	return r, nil
}

// hasCommits reports whether any branch exists.
func (s *Service) hasCommits(ctx context.Context, path string) bool {
	out, err := git.Run(ctx, path, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads")
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// branchTip returns the commit of refs/heads/branch, or "" if it does not
// exist.
func branchTip(ctx context.Context, path, branch string) (string, error) {
	out, err := git.Run(ctx, path, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
	if exitCode(err) == 1 {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read branch: %w", err)
	}
	return string(bytes.TrimSpace(out)), nil
}
