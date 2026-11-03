package gitbrowse

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// FileChange is one changed file of a range, without its patch.
type FileChange struct {
	Status    string // StatusAdded, StatusModified, StatusDeleted, StatusRenamed, StatusCopied
	OldPath   string
	NewPath   string
	Additions int
	Deletions int
	Binary    bool
}

// DiffRange returns the diff between two commits of repo, limited to paths
// (literal paths; none = every file), within the configured limits.
func (s *Service) DiffRange(ctx context.Context, repo Repo, from, to string, paths []string) (Diff, error) {
	if !isOID(from) || !isOID(to) {
		return Diff{}, invalidf("diff range needs full commit ids")
	}
	var d Diff
	err := s.read(ctx, repo, func(rp string) error {
		args := []string{from, to}
		if len(paths) > 0 {
			args = append(args, "--")
			for _, p := range paths {
				args = append(args, ":(literal)"+p)
			}
		}
		var err error
		d, err = s.diff(ctx, rp, args...)
		return err
	})
	return d, err
}

// ChangedFiles lists the files changed between two commits of repo (rename
// detection on), in path order, with their line counts.
func (s *Service) ChangedFiles(ctx context.Context, repo Repo, from, to string) ([]FileChange, error) {
	if !isOID(from) || !isOID(to) {
		return nil, invalidf("changed files need full commit ids")
	}
	var out []FileChange
	err := s.read(ctx, repo, func(rp string) error {
		status, err := run(ctx, rp, readTimeout, "diff-tree", "-r", "-M", "-z", "--name-status", from, to)
		if err != nil {
			return fmt.Errorf("diff-tree: %w", err)
		}
		nums, err := run(ctx, rp, readTimeout, "diff-tree", "-r", "-M", "-z", "--numstat", from, to)
		if err != nil {
			return fmt.Errorf("diff-tree: %w", err)
		}
		out = parseNameStatus(status)
		counts := parseNumstat(nums)
		for i := range out {
			if c, ok := counts[out[i].NewPath]; ok {
				out[i].Additions, out[i].Deletions, out[i].Binary = c.Additions, c.Deletions, c.Binary
			}
		}
		return nil
	})
	return out, err
}

// parseNameStatus parses diff-tree -z --name-status.
func parseNameStatus(b []byte) []FileChange {
	f := bytes.Split(bytes.TrimSuffix(b, []byte{0}), []byte{0})
	var out []FileChange
	for i := 0; i < len(f); {
		code := string(f[i])
		i++
		if code == "" {
			continue
		}
		var c FileChange
		switch code[0] {
		case 'R', 'C':
			if i+1 >= len(f) {
				return out
			}
			c.OldPath, c.NewPath = string(f[i]), string(f[i+1])
			i += 2
			c.Status = StatusRenamed
			if code[0] == 'C' {
				c.Status = StatusCopied
			}
		default:
			if i >= len(f) {
				return out
			}
			c.OldPath, c.NewPath = string(f[i]), string(f[i])
			i++
			switch code[0] {
			case 'A':
				c.Status = StatusAdded
			case 'D':
				c.Status = StatusDeleted
			default:
				c.Status = StatusModified
			}
		}
		out = append(out, c)
	}
	return out
}

// parseNumstat parses diff-tree -z --numstat, keyed by the new path.
func parseNumstat(b []byte) map[string]FileChange {
	f := bytes.Split(bytes.TrimSuffix(b, []byte{0}), []byte{0})
	out := map[string]FileChange{}
	for i := 0; i < len(f); {
		parts := strings.SplitN(string(f[i]), "\t", 3)
		i++
		if len(parts) < 3 {
			continue
		}
		var c FileChange
		if parts[0] == "-" {
			c.Binary = true
		} else {
			c.Additions, _ = strconv.Atoi(parts[0])
			c.Deletions, _ = strconv.Atoi(parts[1])
		}
		path := parts[2]
		if path == "" { // rename: old and new path follow
			if i+1 >= len(f) {
				break
			}
			path = string(f[i+1])
			i += 2
		}
		out[path] = c
	}
	return out
}

// CommitRange lists the commits reachable from to and not from from,
// newest first, skipping offset of them; more is set when there are more
// than limit.
func (s *Service) CommitRange(ctx context.Context, repo Repo, from, to string, offset, limit int) ([]Commit, bool, error) {
	if !isOID(from) || !isOID(to) {
		return nil, false, invalidf("commit range needs full commit ids")
	}
	var list []Commit
	err := s.read(ctx, repo, func(rp string) error {
		var err error
		list, err = commitsOf(ctx, rp, limit+1, "--skip="+strconv.Itoa(offset), to, "^"+from)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if len(list) > limit {
		return list[:limit], true, nil
	}
	return list, false, nil
}

// Identity returns the GIT_AUTHOR_* and GIT_COMMITTER_* environment of a
// user, as used for web edits.
func (s *Service) Identity(ctx context.Context, userID uuid.UUID) ([]string, error) {
	if s.q == nil {
		return nil, fmt.Errorf("%w: identities need the database", ErrUnavailable)
	}
	return s.identity(ctx, userID)
}
