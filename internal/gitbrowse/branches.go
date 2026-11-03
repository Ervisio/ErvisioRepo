package gitbrowse

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// MaxRefLimit bounds a page of branches or tags.
const MaxRefLimit = 100

// Branch is a branch with its tip and its distance from the default branch.
type Branch struct {
	Name    string
	Commit  CommitSummary
	Default bool
	// Ahead and Behind count the commits of the branch missing from the
	// default branch and the other way round; nil for the default branch
	// or when the default branch does not exist.
	Ahead  *int
	Behind *int
}

// Tag is a tag with the commit it points to.
type Tag struct {
	Name      string
	Commit    CommitSummary
	Annotated bool
	Message   string // subject of an annotated tag
	Tagger    *Person
}

// refPage pages a ref listing in process (for-each-ref has no offset).
func refPage(offset, limit int) (int, int) {
	if limit <= 0 {
		limit = 30
	}
	return max(offset, 0), min(limit, MaxRefLimit)
}

// Branches lists the branches, most recently committed first, with
// ahead/behind against the default branch. HasMore reports a next page.
func (s *Service) Branches(ctx context.Context, repo Repo, offset, limit int) (list []Branch, hasMore bool, err error) {
	offset, limit = refPage(offset, limit)
	err = s.read(ctx, repo, func(rp string) error {
		out, err := run(ctx, rp, readTimeout, "for-each-ref", "--sort=-committerdate",
			"--format=%(refname)%00%(objectname)%00%(committerdate:unix)%00%(authorname)%00%(contents:subject)",
			"--count="+strconv.Itoa(offset+limit+1), "refs/heads")
		if err != nil {
			return fmt.Errorf("list branches: %w", err)
		}
		rows := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		def, err := branchTip(ctx, rp, repo.DefaultBranch)
		if err != nil {
			return err
		}
		for i, row := range rows {
			f := strings.SplitN(row, "\x00", 5)
			if len(f) != 5 || i < offset {
				continue
			}
			if len(list) == limit {
				hasMore = true
				break
			}
			b := Branch{Name: strings.TrimPrefix(f[0], "refs/heads/"),
				Commit: CommitSummary{SHA: f[1], Date: unixTime(f[2]), Author: f[3], Summary: f[4]}}
			b.Default = b.Name == repo.DefaultBranch
			if !b.Default && def != "" {
				if ahead, behind, err := aheadBehind(ctx, rp, def, b.Commit.SHA); err == nil {
					b.Ahead, b.Behind = &ahead, &behind
				} else {
					s.log.WarnContext(ctx, "branch ahead/behind", "repo", repo.ID, "error", err)
				}
			}
			list = append(list, b)
		}
		return nil
	})
	return list, hasMore, err
}

// aheadBehind counts the commits of head not in base and of base not in head.
func aheadBehind(ctx context.Context, rp, base, head string) (ahead, behind int, err error) {
	out, err := run(ctx, rp, readTimeout, "rev-list", "--left-right", "--count", base+"..."+head, "--")
	if err != nil {
		return 0, 0, fmt.Errorf("rev-list: %w", err)
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("rev-list: unexpected output %q", out)
	}
	behind, err1 := strconv.Atoi(f[0])
	ahead, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("rev-list: unexpected output %q", out)
	}
	return ahead, behind, nil
}

// Tags lists the tags that point to commits, newest first.
func (s *Service) Tags(ctx context.Context, repo Repo, offset, limit int) (list []Tag, hasMore bool, err error) {
	offset, limit = refPage(offset, limit)
	err = s.read(ctx, repo, func(rp string) error {
		out, err := run(ctx, rp, readTimeout, "for-each-ref", "--sort=-creatordate",
			"--format=%(refname)%00%(objecttype)%00%(objectname)%00%(*objecttype)%00%(*objectname)%00"+
				"%(taggername)%00%(taggeremail:trim)%00%(taggerdate:unix)%00%(contents:subject)%00"+
				"%(*committerdate:unix)%00%(committerdate:unix)%00%(*authorname)%00%(authorname)%00"+
				"%(*contents:subject)",
			"refs/tags")
		if err != nil {
			return fmt.Errorf("list tags: %w", err)
		}
		n := 0
		for _, row := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			f := strings.Split(row, "\x00")
			if len(f) != 14 {
				continue
			}
			t := Tag{Name: strings.TrimPrefix(f[0], "refs/tags/")}
			switch {
			case f[1] == "commit":
				t.Commit = CommitSummary{SHA: f[2], Date: unixTime(f[10]), Author: f[12], Summary: f[8]}
			case f[1] == "tag" && f[3] == "commit":
				t.Annotated, t.Message = true, f[8]
				t.Commit = CommitSummary{SHA: f[4], Date: unixTime(f[9]), Author: f[11], Summary: f[13]}
				if f[5] != "" {
					t.Tagger = &Person{Name: f[5], Email: f[6], Date: unixTime(f[7])}
				}
			default:
				continue // a tag of a tree or blob
			}
			if n++; n <= offset {
				continue
			}
			if len(list) == limit {
				hasMore = true
				break
			}
			list = append(list, t)
		}
		return nil
	})
	return list, hasMore, err
}

// Comparison is the difference between two refs from their merge base.
type Comparison struct {
	Base      Resolved
	Head      Resolved
	MergeBase string
	Ahead     int // commits of head not in base
	Behind    int // commits of base not in head
	Commits   []Commit
	// CommitsTruncated: more than MaxCompareCommits commits.
	CommitsTruncated bool
	Diff             Diff
}

// MaxCompareCommits bounds the commit list of a comparison.
const MaxCompareCommits = 250

// Compare compares head with base: the commits reachable from head and not
// from base, and the diff from their merge base to head (three-dot).
func (s *Service) Compare(ctx context.Context, repo Repo, base, head string) (Comparison, error) {
	var c Comparison
	err := s.read(ctx, repo, func(rp string) error {
		var err error
		if c.Base, err = s.resolve(ctx, rp, repo, base); err != nil {
			return err
		}
		if c.Head, err = s.resolve(ctx, rp, repo, head); err != nil {
			return err
		}
		out, err := run(ctx, rp, readTimeout, "merge-base", c.Base.Commit, c.Head.Commit)
		if exitCode(err) == 1 {
			return invalidf("%s and %s have no common history", base, head)
		}
		if err != nil {
			return fmt.Errorf("merge-base: %w", err)
		}
		c.MergeBase = strings.TrimSpace(string(out))
		if c.Ahead, c.Behind, err = aheadBehind(ctx, rp, c.Base.Commit, c.Head.Commit); err != nil {
			return err
		}
		if c.Commits, err = commitsOf(ctx, rp, MaxCompareCommits+1, c.Head.Commit, "^"+c.Base.Commit); err != nil {
			return err
		}
		if len(c.Commits) > MaxCompareCommits {
			c.Commits, c.CommitsTruncated = c.Commits[:MaxCompareCommits], true
		}
		c.Diff, err = s.diff(ctx, rp, c.MergeBase, c.Head.Commit)
		return err
	})
	return c, err
}
