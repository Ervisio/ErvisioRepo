package pulls

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// maxHunkContext bounds the diff lines stored with a comment, up to and
// including the commented line.
const maxHunkContext = 20

// CommentInput creates a review comment: a reply (InReplyTo set) or the
// first comment of a thread on Path at Line (and StartLine for a range).
type CommentInput struct {
	Body      string
	InReplyTo uuid.UUID
	Path      string
	Side      string // LEFT or RIGHT; empty means RIGHT
	Line      int
	StartLine int // 0 for a single line
	CommitSHA string
}

// ---- conversions ----

// comments converts rows, marking the comments of viewer's pending review.
func (s *Service) comments(ctx context.Context, viewer uuid.UUID, pullID uuid.UUID, rows []dbgen.PullReviewComment) ([]Comment, error) {
	pending := uuid.Nil
	if viewer != uuid.Nil {
		rv, err := s.q.GetPendingPullReview(ctx, dbgen.GetPendingPullReviewParams{PullID: pullID, ReviewerID: nullID(viewer)})
		switch {
		case err == nil:
			pending = rv.ID
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("get pending review: %w", err)
		}
	}
	var ids []uuid.UUID
	for _, c := range rows {
		ids = append(ids, c.AuthorID.UUID, c.ResolvedBy.UUID)
	}
	names, err := s.names(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Comment, 0, len(rows))
	for _, c := range rows {
		out = append(out, Comment{ID: c.ID, ReviewID: c.ReviewID.UUID, Pending: pending != uuid.Nil && c.ReviewID.UUID == pending,
			InReplyTo: c.InReplyTo.UUID, Author: names[c.AuthorID.UUID], AuthorID: c.AuthorID.UUID, Path: c.Path, Side: c.Side,
			Line: intPtr(c.Line), StartLine: intPtr(c.StartLine), OriginalLine: int(c.OriginalLine),
			OriginalStartLine: intPtr(c.OriginalStartLine), OriginalCommitSHA: c.OriginalCommitSha, CommitSHA: c.CommitSha,
			DiffHunk: c.DiffHunk, Body: c.Body, Outdated: c.Outdated, Resolved: c.ResolvedAt.Valid,
			ResolvedBy: names[c.ResolvedBy.UUID], CreatedAt: c.CreatedAt.Time, UpdatedAt: c.UpdatedAt.Time})
	}
	return out, nil
}

func (s *Service) comment(ctx context.Context, viewer uuid.UUID, c dbgen.PullReviewComment) (Comment, error) {
	out, err := s.comments(ctx, viewer, c.PullID, []dbgen.PullReviewComment{c})
	if err != nil {
		return Comment{}, err
	}
	return out[0], nil
}

func (s *Service) emitComment(ctx context.Context, q *dbgen.Queries, action string, actor uuid.UUID, p dbgen.PullRequest, c dbgen.PullReviewComment) error {
	pl := events.PullReviewCommentPayload{Number: p.Number, PullID: p.IssueID, CommentID: c.ID, Path: c.Path}
	if c.ReviewID.Valid {
		id := c.ReviewID.UUID
		pl.ReviewID = &id
	}
	if c.InReplyTo.Valid {
		id := c.InReplyTo.UUID
		pl.InReplyTo = &id
	}
	if c.Line.Valid {
		pl.Line = int(c.Line.Int32)
	}
	return events.Emit(ctx, q, events.Event{Kind: events.KindPullReviewComment, Action: action, RepoID: p.RepoID, ActorID: actor, SubjectID: c.ID}, pl)
}

// ---- reading ----

// ListComments returns the review comments of a pull request in thread
// order, limited to one review when reviewID is set.
func (s *Service) ListComments(ctx context.Context, who auth.Principal, owner, name string, number int64, reviewID uuid.UUID, offset, limit int) ([]Comment, error) {
	r, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	_, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return nil, err
	}
	if reviewID != uuid.Nil {
		if _, err := s.loadReview(ctx, who, p, reviewID); err != nil {
			return nil, err
		}
	}
	off, lim := pageArgs(offset, limit)
	rows, err := s.q.ListPullReviewComments(ctx, dbgen.ListPullReviewCommentsParams{PullID: p.IssueID, ViewerID: nullID(who.UserID),
		ReviewID: reviewID, Off: off, Lim: lim})
	if err != nil {
		return nil, fmt.Errorf("list review comments: %w", err)
	}
	return s.comments(ctx, who.UserID, p.IssueID, rows)
}

// loadComment returns a review comment of repository r the caller can see,
// with its pull request.
func (s *Service) loadComment(ctx context.Context, who auth.Principal, r repos.Repo, id uuid.UUID) (dbgen.PullReviewComment, dbgen.Issue, dbgen.PullRequest, error) {
	c, err := s.q.GetPullReviewCommentInRepo(ctx, dbgen.GetPullReviewCommentInRepoParams{ID: id, RepoID: r.ID})
	if err != nil {
		return c, dbgen.Issue{}, dbgen.PullRequest{}, notFound(err, ErrCommentNotFound, "get review comment")
	}
	if c.ReviewID.Valid {
		rv, err := s.q.GetPullReview(ctx, dbgen.GetPullReviewParams{ID: c.ReviewID.UUID, PullID: c.PullID})
		if err != nil {
			return c, dbgen.Issue{}, dbgen.PullRequest{}, notFound(err, ErrCommentNotFound, "get review")
		}
		if rv.State == ReviewPending && (who.UserID == uuid.Nil || rv.ReviewerID.UUID != who.UserID) {
			return c, dbgen.Issue{}, dbgen.PullRequest{}, ErrCommentNotFound
		}
	}
	p, err := s.q.GetPullRequestByID(ctx, c.PullID)
	if err != nil {
		return c, dbgen.Issue{}, dbgen.PullRequest{}, notFound(err, ErrNotFound, "get pull request")
	}
	i, err := s.q.GetIssueByID(ctx, c.PullID)
	if err != nil {
		return c, dbgen.Issue{}, dbgen.PullRequest{}, notFound(err, ErrNotFound, "get pull request issue")
	}
	return c, i, p, nil
}

// GetComment returns one review comment.
func (s *Service) GetComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (Comment, error) {
	r, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return Comment{}, err
	}
	c, _, _, err := s.loadComment(ctx, who, r, id)
	if err != nil {
		return Comment{}, err
	}
	return s.comment(ctx, who.UserID, c)
}

// ---- anchoring ----

// anchorNew checks that a new thread comment sits on a line of the pull
// request diff and returns its insert arguments (without ID, ReviewID and
// AuthorID).
func (s *Service) anchorNew(ctx context.Context, r repos.Repo, p dbgen.PullRequest, in CommentInput) (dbgen.InsertPullReviewCommentParams, error) {
	var arg dbgen.InsertPullReviewCommentParams
	if strings.TrimSpace(in.Body) == "" {
		return arg, invalid("body is required")
	}
	if err := checkText("body", in.Body, MaxBody); err != nil {
		return arg, err
	}
	if err := git.ValidateTreePath(in.Path); err != nil {
		return arg, invalid("invalid path %q", in.Path)
	}
	side := strings.ToUpper(in.Side)
	if side == "" {
		side = SideRight
	}
	if side != SideLeft && side != SideRight {
		return arg, invalid("side must be LEFT or RIGHT")
	}
	if in.Line < 1 {
		return arg, invalid("line must be positive")
	}
	if in.StartLine < 0 || in.StartLine > in.Line {
		return arg, invalid("start_line must be between 1 and line")
	}
	if in.StartLine == in.Line {
		in.StartLine = 0
	}
	if in.CommitSHA != "" && in.CommitSHA != p.HeadSha {
		return arg, fmt.Errorf("%w: the head moved to %s", ErrConflict, p.HeadSha)
	}
	if p.MergeBaseSha == "" {
		return arg, fmt.Errorf("%w: the pull request has no common history with its base", ErrConflict)
	}
	d, err := s.o.Browse.DiffRange(ctx, gitRepo(r), p.MergeBaseSha, p.HeadSha, []string{in.Path})
	if err != nil {
		return arg, fmt.Errorf("pull request diff: %w", err)
	}
	var file *gitbrowse.FileDiff
	for k := range d.Files {
		if f := &d.Files[k]; f.NewPath == in.Path || f.OldPath == in.Path {
			file = f
			break
		}
	}
	if file == nil {
		return arg, invalid("%s is not changed by the pull request", in.Path)
	}
	lineOf := func(l gitbrowse.DiffLine) int {
		if side == SideLeft {
			if l.Type == "add" {
				return 0
			}
			return l.OldLine
		}
		if l.Type == "delete" {
			return 0
		}
		return l.NewLine
	}
	hunk := ""
	for _, h := range file.Hunks {
		at, start := -1, -1
		for k, l := range h.Lines {
			switch lineOf(l) {
			case in.Line:
				at = k
			case in.StartLine:
				start = k
			}
		}
		if at < 0 {
			continue
		}
		if in.StartLine > 0 && (start < 0 || start > at) {
			return arg, invalid("start_line and line must be in the same hunk")
		}
		from := max(0, at-maxHunkContext+1)
		var b strings.Builder
		b.WriteString(h.Header)
		for _, l := range h.Lines[from : at+1] {
			b.WriteByte('\n')
			switch l.Type {
			case "add":
				b.WriteByte('+')
			case "delete":
				b.WriteByte('-')
			case "context":
				b.WriteByte(' ')
			default:
				b.WriteString("\\ ")
			}
			b.WriteString(l.Text)
		}
		hunk = b.String()
		break
	}
	if hunk == "" {
		return arg, invalid("line %d of %s is not part of the diff", in.Line, in.Path)
	}
	arg = dbgen.InsertPullReviewCommentParams{PullID: p.IssueID, RepoID: r.ID, Path: in.Path, Side: side,
		Line: int4(in.Line), OriginalLine: int4(in.Line).Int32, OriginalCommitSha: p.HeadSha, CommitSha: p.HeadSha,
		DiffHunk: hunk, Body: in.Body}
	if in.StartLine > 0 {
		arg.StartLine, arg.OriginalStartLine = int4(in.StartLine), int4(in.StartLine)
	}
	return arg, nil
}

// shift maps line of the old head onto the new head through the changed
// hunks of the file; ok is false when the line itself changed.
func shift(hunks []git.DiffHunk, line int) (int, bool) {
	delta := 0
	for _, h := range hunks {
		if h.OldLines == 0 { // insertion after OldStart
			if line > h.OldStart {
				delta += h.NewLines
				continue
			}
			break
		}
		end := h.OldStart + h.OldLines - 1
		if line < h.OldStart {
			break
		}
		if line <= end {
			return 0, false
		}
		delta += h.NewLines - h.OldLines
	}
	return line + delta, true
}

// reanchor moves the RIGHT side threads of p from oldHead to newHead: the
// lines shift with the changes above them, and a thread whose lines changed
// becomes outdated. LEFT side threads follow the merge base (reanchorLeft).
func (s *Service) reanchor(ctx context.Context, l *gitstore.Lease, p dbgen.PullRequest, oldHead, newHead string) error {
	return s.reanchorSide(ctx, l, p, SideRight, oldHead, newHead, func(c dbgen.PullReviewComment) bool {
		return c.CommitSha == oldHead
	}, func(dbgen.PullReviewComment) string { return newHead })
}

// reanchorLeft moves the LEFT side threads of p, whose lines are lines of
// the merge base, from the old merge base to the new one (the head was
// rebased or the base merged into it).
func (s *Service) reanchorLeft(ctx context.Context, l *gitstore.Lease, p dbgen.PullRequest, oldBase, newBase string) error {
	return s.reanchorSide(ctx, l, p, SideLeft, oldBase, newBase, func(dbgen.PullReviewComment) bool { return true },
		func(c dbgen.PullReviewComment) string { return c.CommitSha })
}

// reanchorSide shifts the threads of one side of p through the changes
// from..to of their files; commit gives the commit_sha a moved thread keeps.
func (s *Service) reanchorSide(ctx context.Context, l *gitstore.Lease, p dbgen.PullRequest, side, from, to string,
	match func(dbgen.PullReviewComment) bool, commit func(dbgen.PullReviewComment) string) error {
	roots, err := s.q.ThreadCommentsOfPull(ctx, p.IssueID)
	if err != nil {
		return fmt.Errorf("thread comments: %w", err)
	}
	var moved []dbgen.PullReviewComment
	seen := map[string]bool{}
	var paths []string
	for _, c := range roots {
		if c.Side != side || !c.Line.Valid || !match(c) {
			continue
		}
		moved = append(moved, c)
		if !seen[c.Path] && len(paths) < git.MaxDiffHunkFiles {
			seen[c.Path] = true
			paths = append(paths, c.Path)
		}
	}
	if len(moved) == 0 {
		return nil
	}
	hunks, err := git.DiffHunks(ctx, l.Path, nil, from, to, paths)
	if err != nil {
		return fmt.Errorf("diff hunks: %w", err)
	}
	for _, hs := range hunks {
		sort.Slice(hs, func(a, b int) bool { return hs[a].OldStart < hs[b].OldStart })
	}
	for _, c := range moved {
		if !seen[c.Path] {
			continue
		}
		arg := dbgen.ReanchorPullReviewCommentParams{CommitSha: commit(c), ID: c.ID}
		line, ok := shift(hunks[c.Path], int(c.Line.Int32))
		start, sok := 0, true
		if c.StartLine.Valid {
			start, sok = shift(hunks[c.Path], int(c.StartLine.Int32))
		}
		if ok && sok && c.StartLine.Valid {
			// A change inside the range also outdates the thread.
			ok = line-start == int(c.Line.Int32-c.StartLine.Int32)
		}
		if ok && sok {
			arg.Line = int4(line)
			if c.StartLine.Valid {
				arg.StartLine = int4(start)
			}
		} else {
			arg.Outdated, arg.Line, arg.StartLine = true, pgtype.Int4{}, pgtype.Int4{}
		}
		if err := s.q.ReanchorPullReviewComment(ctx, arg); err != nil {
			return fmt.Errorf("re-anchor comment: %w", err)
		}
	}
	return nil
}

// ---- writing ----

// CreateComment adds a review comment. It joins the caller's pending
// review when there is one, and is published at once otherwise.
func (s *Service) CreateComment(ctx context.Context, who auth.Principal, owner, name string, number int64, in CommentInput) (Comment, error) {
	r, i, p, err := s.openForReview(ctx, who, owner, name, number)
	if err != nil {
		return Comment{}, err
	}
	var arg dbgen.InsertPullReviewCommentParams
	if in.InReplyTo != uuid.Nil {
		root, _, rp, err := s.loadComment(ctx, who, r, in.InReplyTo)
		if err != nil {
			return Comment{}, err
		}
		if rp.IssueID != p.IssueID {
			return Comment{}, ErrCommentNotFound
		}
		if root.InReplyTo.Valid {
			if root, _, _, err = s.loadComment(ctx, who, r, root.InReplyTo.UUID); err != nil {
				return Comment{}, err
			}
		}
		if strings.TrimSpace(in.Body) == "" {
			return Comment{}, invalid("body is required")
		}
		if err := checkText("body", in.Body, MaxBody); err != nil {
			return Comment{}, err
		}
		arg = dbgen.InsertPullReviewCommentParams{PullID: p.IssueID, RepoID: r.ID, InReplyTo: nullID(root.ID), Path: root.Path,
			Side: root.Side, Line: root.Line, StartLine: root.StartLine, OriginalLine: root.OriginalLine,
			OriginalStartLine: root.OriginalStartLine, OriginalCommitSha: root.OriginalCommitSha, CommitSha: root.CommitSha,
			DiffHunk: root.DiffHunk, Body: in.Body}
	} else if arg, err = s.anchorNew(ctx, r, p, in); err != nil {
		return Comment{}, err
	}
	var c dbgen.PullReviewComment
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		rv, err := q.GetPendingPullReview(ctx, dbgen.GetPendingPullReviewParams{PullID: p.IssueID, ReviewerID: nullID(who.UserID)})
		switch {
		case err == nil:
			arg.ReviewID = nullID(rv.ID)
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("get pending review: %w", err)
		}
		if arg.ID, err = newID(); err != nil {
			return err
		}
		arg.AuthorID = nullID(who.UserID)
		if c, err = q.InsertPullReviewComment(ctx, arg); err != nil {
			return fmt.Errorf("insert review comment: %w", err)
		}
		if arg.ReviewID.Valid {
			return nil // published with the review
		}
		if err := s.recordRefs(ctx, q, r, who, i, c.ID, c.Body); err != nil {
			return err
		}
		return s.emitComment(ctx, q, "created", who.UserID, p, c)
	})
	if err != nil {
		return Comment{}, err
	}
	return s.comment(ctx, who.UserID, c)
}

// UpdateComment edits the body of the caller's review comment.
func (s *Service) UpdateComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, body string) (Comment, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Comment{}, err
	}
	c, i, p, err := s.loadComment(ctx, who, r, id)
	if err != nil {
		return Comment{}, err
	}
	if !c.AuthorID.Valid || c.AuthorID.UUID != who.UserID {
		return Comment{}, ErrForbidden
	}
	if i.Locked && !canTriage(r) {
		return Comment{}, ErrLocked
	}
	if strings.TrimSpace(body) == "" {
		return Comment{}, invalid("body is required")
	}
	if err := checkText("body", body, MaxBody); err != nil {
		return Comment{}, err
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		nc, err := q.UpdatePullReviewCommentBody(ctx, dbgen.UpdatePullReviewCommentBodyParams{Body: body, ID: c.ID})
		if err != nil {
			return notFound(err, ErrCommentNotFound, "update review comment")
		}
		c = nc
		if c.ReviewID.Valid {
			if rv, err := q.GetPullReview(ctx, dbgen.GetPullReviewParams{ID: c.ReviewID.UUID, PullID: c.PullID}); err == nil && rv.State == ReviewPending {
				return nil
			}
		}
		return s.emitComment(ctx, q, "edited", who.UserID, p, c)
	})
	if err != nil {
		return Comment{}, err
	}
	return s.comment(ctx, who.UserID, c)
}

// DeleteComment deletes a review comment (with its replies when it starts
// a thread). The author or a triager may delete it.
func (s *Service) DeleteComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) error {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return err
	}
	c, _, p, err := s.loadComment(ctx, who, r, id)
	if err != nil {
		return err
	}
	if !canTriage(r) && (!c.AuthorID.Valid || c.AuthorID.UUID != who.UserID) {
		return ErrForbidden
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeletePullReviewComment(ctx, c.ID)
		if err != nil {
			return fmt.Errorf("delete review comment: %w", err)
		}
		if n == 0 {
			return ErrCommentNotFound
		}
		return s.emitComment(ctx, q, "deleted", who.UserID, p, c)
	})
}

// ResolveThread marks the thread of a comment resolved or unresolved. The
// pull request author or a triager may do it.
func (s *Service) ResolveThread(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, resolved bool) (Comment, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Comment{}, err
	}
	c, i, _, err := s.loadComment(ctx, who, r, id)
	if err != nil {
		return Comment{}, err
	}
	if !canTriage(r) && !isUser(i.AuthorID, who.UserID) && !isUser(c.AuthorID, who.UserID) {
		return Comment{}, ErrForbidden
	}
	root := c.ID
	if c.InReplyTo.Valid {
		root = c.InReplyTo.UUID
	}
	arg := dbgen.SetPullThreadResolvedParams{PullID: c.PullID, RootID: root}
	if resolved {
		arg.ResolvedAt, arg.ResolvedBy = ts(s.o.Now()), nullID(who.UserID)
	}
	if _, err := s.q.SetPullThreadResolved(ctx, arg); err != nil {
		return Comment{}, fmt.Errorf("resolve thread: %w", err)
	}
	nc, err := s.q.GetPullReviewCommentInRepo(ctx, dbgen.GetPullReviewCommentInRepoParams{ID: root, RepoID: r.ID})
	if err != nil {
		return Comment{}, notFound(err, ErrCommentNotFound, "get review comment")
	}
	return s.comment(ctx, who.UserID, nc)
}

// ---- suggestions ----

// suggestion returns the replacement text of the first ```suggestion block
// of body.
func suggestion(body string) (string, bool) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	const open = "```suggestion"
	k := strings.Index(body, open)
	if k < 0 {
		return "", false
	}
	rest := body[k+len(open):]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 || strings.TrimSpace(rest[:nl]) != "" {
		return "", false
	}
	rest = rest[nl+1:]
	if strings.HasPrefix(rest, "```") {
		return "", true // empty suggestion: delete the lines
	}
	end := strings.Index(rest, "\n```")
	if end < 0 {
		return "", false
	}
	return rest[:end+1], true
}

// ApplySuggestion commits the suggestion of a review comment to the head
// branch, as the caller. It returns the new head commit.
func (s *Service) ApplySuggestion(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, message string) (string, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return "", err
	}
	c, i, p, err := s.loadComment(ctx, who, r, id)
	if err != nil {
		return "", err
	}
	if i.State != "open" || p.MergedAt.Valid {
		return "", fmt.Errorf("%w: the pull request is not open", ErrConflict)
	}
	text, ok := suggestion(c.Body)
	if !ok {
		return "", invalid("the comment has no suggestion")
	}
	if c.Side != SideRight || c.Outdated || !c.Line.Valid {
		return "", fmt.Errorf("%w: the suggestion is outdated", ErrConflict)
	}
	if c.CommitSha != p.HeadSha {
		return "", fmt.Errorf("%w: the head moved to %s", ErrConflict, p.HeadSha)
	}
	if !p.HeadRepoID.Valid {
		return "", fmt.Errorf("%w: the head repository was deleted", ErrConflict)
	}
	hr, err := s.o.Repos.GetByID(ctx, who, p.HeadRepoID.UUID)
	if err != nil {
		return "", err
	}
	if !canWrite(hr) {
		return "", ErrForbidden
	}
	if len(message) > MaxTitle*4 {
		return "", invalid("message is too long")
	}
	if strings.TrimSpace(message) == "" {
		message = "Apply suggestion to " + c.Path
	}
	blob, err := s.o.Browse.Blob(ctx, gitRepo(hr), p.HeadSha, c.Path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", c.Path, err)
	}
	if blob.Binary || blob.Content == nil {
		return "", invalid("%s cannot be edited", c.Path)
	}
	content := string(blob.Content)
	trailing := strings.HasSuffix(content, "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	end := int(c.Line.Int32)
	start := end
	if c.StartLine.Valid {
		start = int(c.StartLine.Int32)
	}
	if start < 1 || end > len(lines) {
		return "", fmt.Errorf("%w: the suggestion is outdated", ErrConflict)
	}
	var repl []string
	if text != "" {
		repl = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	out := append(append(append([]string{}, lines[:start-1]...), repl...), lines[end:]...)
	next := strings.Join(out, "\n")
	if trailing && len(out) > 0 {
		next += "\n"
	}
	res, err := s.o.Browse.Edit(ctx, gitRepo(hr), gitbrowse.Edit{Op: gitbrowse.OpUpdate, Path: c.Path, Content: []byte(next),
		Branch: p.HeadRef, ExpectedOID: blob.OID, Message: message, AuthorID: who.UserID})
	if err != nil {
		return "", fmt.Errorf("apply suggestion: %w", err)
	}
	return res.Commit.SHA, nil
}
