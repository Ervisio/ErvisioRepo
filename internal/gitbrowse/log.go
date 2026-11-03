package gitbrowse

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// commitFormat is the pretty format parsed by parseCommits (with -z).
const commitFormat = "--format=format:%H%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%cn%x1f%ce%x1f%ct%x1f%B"

// MaxLogLimit bounds a history page.
const MaxLogLimit = 100

func unixTime(s string) time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0).UTC()
}

func parseCommit(rec string) (Commit, bool) {
	f := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x1f", 9)
	if len(f) != 9 || !isHex(f[0]) {
		return Commit{}, false
	}
	c := Commit{
		SHA:       f[0],
		Parents:   strings.Fields(f[1]),
		Author:    Person{Name: f[2], Email: f[3], Date: unixTime(f[4])},
		Committer: Person{Name: f[5], Email: f[6], Date: unixTime(f[7])},
		Message:   strings.TrimRight(f[8], "\n"),
	}
	if c.Parents == nil {
		c.Parents = []string{}
	}
	c.Summary, _, _ = strings.Cut(c.Message, "\n")
	return c, true
}

func parseCommits(out []byte) []Commit {
	var list []Commit
	for _, rec := range bytes.Split(out, []byte{0}) {
		if c, ok := parseCommit(string(rec)); ok {
			list = append(list, c)
		}
	}
	return list
}

// LogPage is a page of history.
type LogPage struct {
	Ref     Resolved
	Commits []Commit
	HasMore bool
}

// Log lists the history of ref, newest first, optionally limited to the
// commits that touch path.
func (s *Service) Log(ctx context.Context, repo Repo, ref, path string, offset, limit int) (LogPage, error) {
	if err := git.ValidateTreePath(path); err != nil {
		return LogPage{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if limit <= 0 || limit > MaxLogLimit {
		limit = min(max(limit, 30), MaxLogLimit)
	}
	offset = max(offset, 0)
	var page LogPage
	err := s.read(ctx, repo, func(rp string) error {
		r, err := s.resolve(ctx, rp, repo, ref)
		if err != nil {
			return err
		}
		page.Ref = r
		args := []string{"log", "-z", commitFormat, "--skip=" + strconv.Itoa(offset),
			"--max-count=" + strconv.Itoa(limit+1), r.Commit, "--"}
		if path != "" {
			args = append(args, path)
		}
		out, err := run(ctx, rp, readTimeout, args...)
		if err != nil {
			return fmt.Errorf("log: %w", err)
		}
		page.Commits = parseCommits(out)
		if len(page.Commits) > limit {
			page.Commits, page.HasMore = page.Commits[:limit], true
		}
		return nil
	})
	return page, err
}

// commits returns up to limit commits of a revision range (git log args).
func commitsOf(ctx context.Context, path string, limit int, revs ...string) ([]Commit, error) {
	args := append([]string{"log", "-z", commitFormat, "--max-count=" + strconv.Itoa(limit)}, revs...)
	out, err := run(ctx, path, readTimeout, append(args, "--")...)
	if err != nil {
		return nil, fmt.Errorf("log: %w", err)
	}
	return parseCommits(out), nil
}
