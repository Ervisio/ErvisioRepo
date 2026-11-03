package gitbrowse

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// BlameRange is a run of consecutive lines last changed by one commit.
type BlameRange struct {
	Commit    CommitSummary
	StartLine int // 1-based
	Lines     int
}

// Blame is the line-by-line authorship of a file.
type Blame struct {
	Ref    Resolved
	Path   string
	Lines  []string
	Ranges []BlameRange
}

// Blame annotates the file at path of ref. Binary files and files larger
// than MaxBlobSize are refused.
func (s *Service) Blame(ctx context.Context, repo Repo, ref, path string) (Blame, error) {
	if path == "" {
		return Blame{}, invalidf("path is required")
	}
	if err := git.ValidateTreePath(path); err != nil {
		return Blame{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	bl := Blame{Path: path}
	err := s.read(ctx, repo, func(rp string) error {
		r, err := s.resolve(ctx, rp, repo, ref)
		if err != nil {
			return err
		}
		bl.Ref = r
		b, err := s.blob(ctx, rp, r, path)
		if err != nil {
			return err
		}
		if b.Truncated {
			return fmt.Errorf("%w: the file exceeds %d bytes", ErrTooLarge, s.o.MaxBlobSize)
		}
		if b.Binary {
			return invalidf("%q is a binary file", path)
		}
		out, err := run(ctx, rp, readTimeout, "blame", "--porcelain", r.Commit, "--", path)
		if err != nil {
			return fmt.Errorf("blame: %w", err)
		}
		bl.Lines, bl.Ranges = parseBlame(out)
		return nil
	})
	return bl, err
}

func parseBlame(out []byte) ([]string, []BlameRange) {
	commits := map[string]*CommitSummary{}
	var (
		lines  []string
		ranges []BlameRange
		cur    *CommitSummary
		final  int
	)
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "\t") {
			if cur == nil {
				continue
			}
			lines = append(lines, line[1:])
			if n := len(ranges); n > 0 && ranges[n-1].Commit.SHA == cur.SHA && ranges[n-1].StartLine+ranges[n-1].Lines == final {
				ranges[n-1].Lines++
			} else {
				ranges = append(ranges, BlameRange{Commit: *cur, StartLine: final, Lines: 1})
			}
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 3 && isHex(f[0]) && len(f[0]) >= 40 {
			c, ok := commits[f[0]]
			if !ok {
				c = &CommitSummary{SHA: f[0]}
				commits[f[0]] = c
			}
			cur = c
			final, _ = strconv.Atoi(f[2])
			continue
		}
		if cur == nil {
			continue
		}
		k, v, _ := strings.Cut(line, " ")
		switch k {
		case "author":
			cur.Author = v
		case "author-time":
			cur.Date = unixTime(v)
		case "summary":
			cur.Summary = v
		}
	}
	// Header fields of a commit follow its first line only: refresh the
	// ranges with the complete summaries.
	for i := range ranges {
		ranges[i].Commit = *commits[ranges[i].Commit.SHA]
	}
	return lines, ranges
}
