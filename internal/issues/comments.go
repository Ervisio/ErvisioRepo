package issues

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

func checkCommentBody(b string) error {
	if b == "" {
		return invalid("body is required")
	}
	return checkText("body", b, MaxBody)
}

func (s *Service) comment(ctx context.Context, q *dbgen.Queries, r repos.Repo, id uuid.UUID) (dbgen.IssueComment, dbgen.Issue, error) {
	c, err := q.GetIssueComment(ctx, dbgen.GetIssueCommentParams{ID: id, RepoID: r.ID})
	if err != nil {
		return dbgen.IssueComment{}, dbgen.Issue{}, notFound(err, ErrCommentNotFound, "get comment")
	}
	i, err := q.GetIssueByID(ctx, c.IssueID)
	if err != nil {
		return dbgen.IssueComment{}, dbgen.Issue{}, notFound(err, ErrCommentNotFound, "get comment issue")
	}
	return c, i, nil
}

func commentPayload(i dbgen.Issue, id uuid.UUID) EventPayload {
	p := issuePayload(i)
	p.CommentID = &id
	return p
}

// ListComments returns the comments of an issue, oldest first.
func (s *Service) ListComments(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]Comment, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	i, err := s.get(ctx, s.q, r, number)
	if err != nil {
		return nil, err
	}
	off, lim := pageArgs(offset, limit)
	rows, err := s.q.ListIssueComments(ctx, dbgen.ListIssueCommentsParams{IssueID: i.ID, Off: off, Lim: lim})
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	return s.hydrateComments(ctx, who.UserID, rows)
}

// GetComment returns a comment of owner/name.
func (s *Service) GetComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (Comment, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return Comment{}, err
	}
	c, _, err := s.comment(ctx, s.q, r, id)
	if err != nil {
		return Comment{}, err
	}
	out, err := s.hydrateComments(ctx, who.UserID, []dbgen.IssueComment{c})
	if err != nil {
		return Comment{}, err
	}
	return out[0], nil
}

// CreateComment comments on an issue. On a locked issue only users with the
// triage permission can comment.
func (s *Service) CreateComment(ctx context.Context, who auth.Principal, owner, name string, number int64, body string) (Comment, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Comment{}, err
	}
	if err := checkCommentBody(body); err != nil {
		return Comment{}, err
	}
	var row dbgen.IssueComment
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		i, err := s.lockIssue(ctx, q, r, number)
		if err != nil {
			return err
		}
		if i.Locked && !canTriage(r) {
			return ErrLocked
		}
		id, err := newID()
		if err != nil {
			return err
		}
		if row, err = q.InsertIssueComment(ctx, dbgen.InsertIssueCommentParams{ID: id, IssueID: i.ID, RepoID: r.ID,
			AuthorID: nullID(who.UserID), Body: body}); err != nil {
			return fmt.Errorf("insert comment: %w", err)
		}
		if err := q.AddIssueCommentCount(ctx, dbgen.AddIssueCommentCountParams{ID: i.ID, Delta: 1}); err != nil {
			return fmt.Errorf("count comment: %w", err)
		}
		if err := s.event(ctx, q, i, who.UserID, "commented", row.ID, nil); err != nil {
			return err
		}
		if err := s.recordRefs(ctx, q, r, who, refSource{issue: i, comment: row.ID}, body); err != nil {
			return err
		}
		return s.emit(ctx, q, events.KindIssueComment, "created", r.ID, who.UserID, row.ID, commentPayload(i, row.ID))
	})
	if err != nil {
		return Comment{}, err
	}
	out, err := s.hydrateComments(ctx, who.UserID, []dbgen.IssueComment{row})
	if err != nil {
		return Comment{}, err
	}
	return out[0], nil
}

// UpdateComment edits a comment (its author or triage). The previous body
// goes to the edit history.
func (s *Service) UpdateComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, body string) (Comment, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Comment{}, err
	}
	if err := checkCommentBody(body); err != nil {
		return Comment{}, err
	}
	cur, i, err := s.comment(ctx, s.q, r, id)
	if err != nil {
		return Comment{}, err
	}
	author := cur.AuthorID.Valid && cur.AuthorID.UUID == who.UserID
	if !author && !canTriage(r) {
		return Comment{}, ErrForbidden
	}
	if i.Locked && !canTriage(r) {
		return Comment{}, ErrLocked
	}
	row := cur
	if body != cur.Body {
		err = s.tx(ctx, func(q *dbgen.Queries) error {
			eid, err := newID()
			if err != nil {
				return err
			}
			if err := q.InsertIssueEdit(ctx, dbgen.InsertIssueEditParams{ID: eid, IssueID: i.ID, CommentID: nullID(cur.ID),
				EditorID: nullID(who.UserID), Body: cur.Body}); err != nil {
				return fmt.Errorf("record comment edit: %w", err)
			}
			if row, err = q.UpdateIssueCommentBody(ctx, dbgen.UpdateIssueCommentBodyParams{ID: cur.ID, Body: body,
				EditedBy: nullID(who.UserID)}); err != nil {
				return notFound(err, ErrCommentNotFound, "update comment")
			}
			if err := s.recordRefs(ctx, q, r, who, refSource{issue: i, comment: row.ID}, body); err != nil {
				return err
			}
			return s.emit(ctx, q, events.KindIssueComment, "edited", r.ID, who.UserID, row.ID, commentPayload(i, row.ID))
		})
		if err != nil {
			return Comment{}, err
		}
	}
	out, err := s.hydrateComments(ctx, who.UserID, []dbgen.IssueComment{row})
	if err != nil {
		return Comment{}, err
	}
	return out[0], nil
}

// DeleteComment deletes a comment (its author or triage), with its edit
// history, reactions and references.
func (s *Service) DeleteComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, c identity.Client) error {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return err
	}
	cur, i, err := s.comment(ctx, s.q, r, id)
	if err != nil {
		return err
	}
	author := cur.AuthorID.Valid && cur.AuthorID.UUID == who.UserID
	if !author && !canTriage(r) {
		return ErrForbidden
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteIssueComment(ctx, dbgen.DeleteIssueCommentParams{ID: id, RepoID: r.ID})
		if err != nil {
			return fmt.Errorf("delete comment: %w", err)
		}
		if n == 0 {
			return ErrCommentNotFound
		}
		if err := q.AddIssueCommentCount(ctx, dbgen.AddIssueCommentCountParams{ID: i.ID, Delta: -1}); err != nil {
			return fmt.Errorf("count comment: %w", err)
		}
		if err := s.emit(ctx, q, events.KindIssueComment, "deleted", r.ID, who.UserID, id, commentPayload(i, id)); err != nil {
			return err
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionCommentDeleted, r, "issue_comment", id, c,
			map[string]any{"number": i.Number, "author_id": cur.AuthorID.UUID}))
	})
}

// ListCommentEdits returns the previous bodies of a comment, newest first.
func (s *Service) ListCommentEdits(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, offset, limit int) ([]BodyEdit, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	cur, i, err := s.comment(ctx, s.q, r, id)
	if err != nil {
		return nil, err
	}
	off, lim := pageArgs(offset, limit)
	rows, err := s.q.ListIssueEdits(ctx, dbgen.ListIssueEditsParams{IssueID: i.ID, CommentID: nullID(cur.ID), Off: off, Lim: lim})
	if err != nil {
		return nil, fmt.Errorf("list comment edits: %w", err)
	}
	return s.edits(ctx, rows)
}
