package issues

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// maxRefsPerKind bounds the references recorded from one text.
const maxRefsPerKind = 50

// Ref is a reference found in Markdown text.
type Ref struct {
	// Kind is "user", "issue" or "commit".
	Kind string
	// User is the mentioned username (Kind user).
	User string
	// Owner and Repo are set for a cross-repository issue reference
	// (owner/repo#N); empty for #N.
	Owner, Repo string
	// Number is the issue or pull request number (Kind issue).
	Number int64
	// Commit is the lowercase commit id as written (Kind commit).
	Commit string
}

var (
	inlineRe  = regexp.MustCompile("`+[^`\n]*`+")
	mentionRe = regexp.MustCompile(`(?:^|[^\w/@.\-])@([A-Za-z0-9][A-Za-z0-9-]{0,38})\b`)
	issueRe   = regexp.MustCompile(`(?:^|[^\w/#&])(?:([A-Za-z0-9][A-Za-z0-9-]{0,38})/([A-Za-z0-9._-]{1,100}))?#(\d{1,18})\b`)
	shaRe     = regexp.MustCompile(`(?:^|[^\w/@#.\-])([0-9a-f]{7,64})\b`)
)

// stripCode blanks fenced code blocks and inline code spans: references
// inside code are not references.
func stripCode(text string) string {
	var b strings.Builder
	fence := ""
	for line := range strings.Lines(text) {
		t := strings.TrimLeft(line, " \t")
		indent := len(line) - len(t)
		t = strings.TrimRight(t, " \t\r\n")
		if fence != "" {
			if indent <= 3 && strings.HasPrefix(t, fence) && strings.Trim(t, fence[:1]) == "" {
				fence = ""
			}
			b.WriteString("\n")
			continue
		}
		if indent <= 3 && (strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")) {
			c := t[:1]
			n := len(t) - len(strings.TrimLeft(t, c))
			fence = strings.Repeat(c, n)
			b.WriteString("\n")
			continue
		}
		b.WriteString(inlineRe.ReplaceAllString(line, " "))
	}
	return b.String()
}

// ParseRefs returns the user mentions (@name), issue references (#N and
// owner/repo#N) and commit ids (7 to 64 lowercase hex digits with at least
// one digit and one letter) of a Markdown text, outside code, without
// duplicates and in order of appearance.
func ParseRefs(text string) []Ref {
	text = stripCode(text)
	var out []Ref
	seen := map[string]bool{}
	add := func(key string, r Ref, n *int) {
		if seen[key] || *n >= maxRefsPerKind {
			return
		}
		seen[key] = true
		*n++
		out = append(out, r)
	}
	var users, issues, commits int
	for _, m := range mentionRe.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if strings.HasSuffix(name, "-") {
			continue
		}
		add("u:"+strings.ToLower(name), Ref{Kind: "user", User: name}, &users)
	}
	for _, m := range issueRe.FindAllStringSubmatch(text, -1) {
		n, err := strconv.ParseInt(m[3], 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		owner, repo := m[1], strings.TrimSuffix(m[2], ".git")
		key := "i:" + strings.ToLower(owner+"/"+repo) + "#" + m[3]
		add(key, Ref{Kind: "issue", Owner: owner, Repo: repo, Number: n}, &issues)
	}
	for _, m := range shaRe.FindAllStringSubmatch(text, -1) {
		c := m[1]
		if !strings.ContainsAny(c, "0123456789") || !strings.ContainsAny(c, "abcdef") {
			continue
		}
		add("c:"+c, Ref{Kind: "commit", Commit: c}, &commits)
	}
	return out
}

// refSource is where references are written: an issue body (comment
// uuid.Nil), a comment, or a commit message (issue zero, repo and commit
// set).
type refSource struct {
	issue   dbgen.Issue
	comment uuid.UUID
	review  uuid.UUID // a pull request review comment (no issue_comments row)
	repo    uuid.UUID
	commit  string
}

func (src refSource) key() string {
	switch {
	case src.commit != "":
		return "commit:" + src.repo.String() + ":" + src.commit
	case src.comment != uuid.Nil:
		return "comment:" + src.comment.String()
	case src.review != uuid.Nil:
		return "review_comment:" + src.review.String()
	default:
		return "issue:" + src.issue.ID.String()
	}
}

func (src refSource) repoID() uuid.UUID {
	if src.commit != "" {
		return src.repo
	}
	return src.issue.RepoID
}

// targetRepo resolves the repository of an issue reference for who; ok is
// false when it does not exist or who cannot read it.
func (s *Service) targetRepo(ctx context.Context, who auth.Principal, r repos.Repo, ref Ref) (repos.Repo, bool, error) {
	if ref.Owner == "" || (strings.EqualFold(ref.Owner, r.Owner) && strings.EqualFold(ref.Repo, r.Name)) {
		return r, true, nil
	}
	t, err := s.o.Repos.Get(ctx, who, ref.Owner, ref.Repo)
	var moved *repos.MovedError
	switch {
	case errors.Is(err, repos.ErrNotFound), errors.Is(err, repos.ErrForbidden), errors.As(err, &moved):
		return repos.Repo{}, false, nil
	case err != nil:
		return repos.Repo{}, false, fmt.Errorf("resolve referenced repository: %w", err)
	}
	return t, true, nil
}

// RecordRefs records the references written by who in the title or body
// of issue (a pull request, in the pulls service) or, when reviewComment is
// set, in that review comment of it. It runs in the caller's transaction q.
func (s *Service) RecordRefs(ctx context.Context, q *dbgen.Queries, r repos.Repo, who auth.Principal, issue dbgen.Issue, reviewComment uuid.UUID, text string) error {
	return s.recordRefs(ctx, q, r, who, refSource{issue: issue, review: reviewComment}, text)
}

// recordRefs records the references of text written in src of repository r
// by who. A new reference to another issue adds a cross-referenced (or,
// from a commit, referenced) entry to the timeline of that issue. Existing
// references are kept when text changes.
func (s *Service) recordRefs(ctx context.Context, q *dbgen.Queries, r repos.Repo, who auth.Principal, src refSource, text string) error {
	refs := ParseRefs(text)
	if len(refs) == 0 {
		return nil
	}
	var names []string
	for _, ref := range refs {
		if ref.Kind == "user" {
			names = append(names, strings.ToLower(ref.User))
		}
	}
	users := map[string]uuid.UUID{}
	if len(names) > 0 {
		rows, err := q.IssueUsersByNames(ctx, names)
		if err != nil {
			return fmt.Errorf("resolve mentions: %w", err)
		}
		for _, u := range rows {
			users[strings.ToLower(u.Name)] = u.ID
		}
	}
	base := dbgen.InsertIssueReferenceParams{SourceKey: src.key(), SourceRepoID: src.repoID(),
		SourceIssueID: nullID(src.issue.ID), SourceCommentID: nullID(src.comment), SourceCommit: src.commit,
		ActorID: nullID(who.UserID)}
	for _, ref := range refs {
		p := base
		id, err := newID()
		if err != nil {
			return err
		}
		p.ID = id
		var target *dbgen.Issue
		switch ref.Kind {
		case "user":
			uid, ok := users[strings.ToLower(ref.User)]
			if !ok {
				continue
			}
			p.TargetKind, p.TargetKey, p.TargetUserID = "user", uid.String(), nullID(uid)
		case "commit":
			p.TargetKind, p.TargetKey, p.TargetCommit = "commit", src.repoID().String()+":"+ref.Commit, ref.Commit
		case "issue":
			tr, ok, err := s.targetRepo(ctx, who, r, ref)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			t, err := q.GetIssueByNumber(ctx, dbgen.GetIssueByNumberParams{RepoID: tr.ID, Number: ref.Number})
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("resolve issue reference: %w", err)
			}
			if t.ID == src.issue.ID {
				continue
			}
			target = &t
			p.TargetKind, p.TargetKey, p.TargetIssueID = "issue", t.ID.String(), nullID(t.ID)
		}
		n, err := q.InsertIssueReference(ctx, p)
		if err != nil {
			return fmt.Errorf("record reference: %w", err)
		}
		if n == 0 || target == nil {
			continue
		}
		if src.commit != "" {
			err = s.event(ctx, q, *target, who.UserID, "referenced", uuid.Nil,
				map[string]any{"repo_id": src.repo, "commit": src.commit})
		} else {
			err = s.event(ctx, q, *target, who.UserID, "cross-referenced", uuid.Nil,
				map[string]any{"source_repo_id": src.issue.RepoID, "source_issue_id": src.issue.ID, "source_comment_id": nullID(src.comment)})
		}
		if err != nil {
			return err
		}
		if err := q.TouchIssue(ctx, target.ID); err != nil {
			return fmt.Errorf("touch referenced issue: %w", err)
		}
	}
	return nil
}
