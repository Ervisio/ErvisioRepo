package gitbrowse

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// MaxGrepQuery bounds the length of a code search pattern.
const MaxGrepQuery = 256

// GrepMatch is one matching line.
type GrepMatch struct {
	Path string
	Line int
	Text string
}

// GrepResult is the outcome of a code search.
type GrepResult struct {
	Ref     Resolved
	Matches []GrepMatch
	// Truncated: the result limit was reached or the search timed out.
	Truncated bool
}

// GrepOptions refine a code search.
type GrepOptions struct {
	Regexp     bool   // extended regular expression instead of a fixed string
	IgnoreCase bool   //
	Path       string // restrict to this directory or file
	Limit      int    // 0 = MaxGrepResults
}

// maxGrepLine bounds the text of a match.
const maxGrepLine = 500

// Grep searches the text files of ref with git grep, stopping at the result
// limit or at the timeout.
func (s *Service) Grep(ctx context.Context, repo Repo, ref, query string, o GrepOptions) (GrepResult, error) {
	var res GrepResult
	if query == "" || len(query) > MaxGrepQuery || strings.ContainsAny(query, "\n\x00") {
		return res, invalidf("the query must be 1 to %d characters on one line", MaxGrepQuery)
	}
	if o.Path != "" {
		if err := git.ValidateTreePath(o.Path); err != nil {
			return res, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	limit := o.Limit
	if limit <= 0 || limit > s.o.MaxGrepResults {
		limit = s.o.MaxGrepResults
	}
	err := s.read(ctx, repo, func(rp string) error {
		var err error
		if res.Ref, err = s.resolve(ctx, rp, repo, ref); err != nil {
			return err
		}
		args := []string{"-c", "core.quotepath=false", "grep", "-z", "-n", "-I", "--no-color", "--full-name"}
		if o.Regexp {
			args = append(args, "-E")
		} else {
			args = append(args, "-F")
		}
		if o.IgnoreCase {
			args = append(args, "-i")
		}
		args = append(args, "-e", query, res.Ref.Commit, "--")
		if o.Path != "" {
			args = append(args, o.Path)
		}
		gctx, cancel := context.WithTimeout(ctx, s.o.GrepTimeout)
		defer cancel()
		st, err := startStream(gctx, rp, s.o.GrepTimeout, args...)
		if err != nil {
			return fmt.Errorf("git grep: %w", err)
		}
		prefix := res.Ref.Commit + ":"
		br := bufio.NewReaderSize(st, 64<<10)
		for {
			line, rerr := br.ReadString('\n')
			if m, ok := parseGrepLine(strings.TrimSuffix(line, "\n"), prefix); ok {
				if len(res.Matches) == limit {
					res.Truncated = true
					break
				}
				res.Matches = append(res.Matches, m)
			}
			if rerr != nil {
				break
			}
		}
		werr := st.Close()
		switch {
		case res.Truncated:
		case gctx.Err() != nil && ctx.Err() == nil:
			res.Truncated = true // timed out: partial results
		case werr == nil, exitCode(werr) == 1: // 1 = no match
		case exitCode(werr) == 128 && o.Regexp:
			return invalidf("invalid regular expression")
		case errors.Is(werr, context.Canceled):
			return werr
		default:
			return fmt.Errorf("git grep: %w", werr)
		}
		return nil
	})
	return res, err
}

// parseGrepLine parses "<commit>:<path>\0<line>\0<text>".
func parseGrepLine(line, prefix string) (GrepMatch, bool) {
	f := strings.SplitN(line, "\x00", 3)
	if len(f) != 3 || !strings.HasPrefix(f[0], prefix) {
		return GrepMatch{}, false
	}
	n, err := strconv.Atoi(f[1])
	if err != nil {
		return GrepMatch{}, false
	}
	text := f[2]
	if len(text) > maxGrepLine {
		text = strings.ToValidUTF8(text[:maxGrepLine], "")
	}
	return GrepMatch{Path: strings.TrimPrefix(f[0], prefix), Line: n, Text: text}, true
}
