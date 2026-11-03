package issues

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// reactionTarget is an issue (comment uuid.Nil) or one of its comments.
type reactionTarget struct {
	issue   dbgen.Issue
	comment uuid.UUID
}

func (s *Service) issueTarget(ctx context.Context, r repos.Repo, number int64) (reactionTarget, error) {
	i, err := s.get(ctx, s.q, r, number)
	if err != nil {
		return reactionTarget{}, err
	}
	return reactionTarget{issue: i}, nil
}

func (s *Service) commentTarget(ctx context.Context, r repos.Repo, id uuid.UUID) (reactionTarget, error) {
	c, i, err := s.comment(ctx, s.q, r, id)
	if err != nil {
		return reactionTarget{}, err
	}
	return reactionTarget{issue: i, comment: c.ID}, nil
}

func checkContent(c string) error {
	if !ValidReaction(c) {
		return invalid("reaction must be +1, -1, laugh, hooray, confused, heart, rocket or eyes")
	}
	return nil
}

func (s *Service) listReactions(ctx context.Context, t reactionTarget, content string, offset, limit int) ([]Reaction, error) {
	if content != "" {
		if err := checkContent(content); err != nil {
			return nil, err
		}
	}
	out := []Reaction{}
	off, lim := pageArgs(offset, limit)
	// The content filter is applied in Go: reactions per target are few.
	skip := int(off)
	pos := int32(0)
	for len(out) < int(lim) {
		rows, err := s.q.ListIssueReactions(ctx, dbgen.ListIssueReactionsParams{IssueID: t.issue.ID, CommentID: nullID(t.comment),
			Off: pos, Lim: 100})
		if err != nil {
			return nil, fmt.Errorf("list reactions: %w", err)
		}
		for _, r := range rows {
			if content != "" && r.Content != content {
				continue
			}
			if skip > 0 {
				skip--
				continue
			}
			if len(out) < int(lim) {
				out = append(out, Reaction{ID: r.ID, Content: r.Content, User: r.Name, CreatedAt: r.CreatedAt.Time})
			}
		}
		if len(rows) < 100 {
			break
		}
		pos += 100
	}
	return out, nil
}

func (s *Service) groups(ctx context.Context, viewer uuid.UUID, t reactionTarget) ([]ReactionGroup, error) {
	out := []ReactionGroup{}
	if t.comment == uuid.Nil {
		rows, err := s.q.IssueReactionGroups(ctx, dbgen.IssueReactionGroupsParams{Viewer: viewer, IssueIds: []uuid.UUID{t.issue.ID}})
		if err != nil {
			return nil, fmt.Errorf("reaction groups: %w", err)
		}
		for _, g := range rows {
			out = append(out, ReactionGroup{Content: g.Content, Count: g.Total, ViewerReacted: g.ViewerReacted})
		}
	} else {
		rows, err := s.q.CommentReactionGroups(ctx, dbgen.CommentReactionGroupsParams{Viewer: viewer, CommentIds: []uuid.UUID{t.comment}})
		if err != nil {
			return nil, fmt.Errorf("reaction groups: %w", err)
		}
		for _, g := range rows {
			out = append(out, ReactionGroup{Content: g.Content, Count: g.Total, ViewerReacted: g.ViewerReacted})
		}
	}
	sortGroups(out)
	return out, nil
}

func (s *Service) react(ctx context.Context, who auth.Principal, r repos.Repo, t reactionTarget, content string) ([]ReactionGroup, error) {
	if err := checkContent(content); err != nil {
		return nil, err
	}
	if t.issue.Locked && !canTriage(r) {
		return nil, ErrLocked
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	if _, err := s.q.InsertIssueReaction(ctx, dbgen.InsertIssueReactionParams{ID: id, IssueID: t.issue.ID,
		CommentID: nullID(t.comment), UserID: who.UserID, Content: content}); err != nil {
		return nil, fmt.Errorf("add reaction: %w", err)
	}
	return s.groups(ctx, who.UserID, t)
}

func (s *Service) unreact(ctx context.Context, who auth.Principal, t reactionTarget, content string) ([]ReactionGroup, error) {
	if err := checkContent(content); err != nil {
		return nil, err
	}
	if _, err := s.q.DeleteIssueReaction(ctx, dbgen.DeleteIssueReactionParams{IssueID: t.issue.ID, CommentID: nullID(t.comment),
		UserID: who.UserID, Content: content}); err != nil {
		return nil, fmt.Errorf("remove reaction: %w", err)
	}
	return s.groups(ctx, who.UserID, t)
}

// ListIssueReactions returns the reactions to an issue (content filters).
func (s *Service) ListIssueReactions(ctx context.Context, who auth.Principal, owner, name string, number int64, content string, offset, limit int) ([]Reaction, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	t, err := s.issueTarget(ctx, r, number)
	if err != nil {
		return nil, err
	}
	return s.listReactions(ctx, t, content, offset, limit)
}

// AddIssueReaction reacts to an issue; the same reaction twice is a no-op.
func (s *Service) AddIssueReaction(ctx context.Context, who auth.Principal, owner, name string, number int64, content string) ([]ReactionGroup, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	t, err := s.issueTarget(ctx, r, number)
	if err != nil {
		return nil, err
	}
	return s.react(ctx, who, r, t, content)
}

// RemoveIssueReaction removes the caller's reaction to an issue.
func (s *Service) RemoveIssueReaction(ctx context.Context, who auth.Principal, owner, name string, number int64, content string) ([]ReactionGroup, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	t, err := s.issueTarget(ctx, r, number)
	if err != nil {
		return nil, err
	}
	return s.unreact(ctx, who, t, content)
}

// ListCommentReactions returns the reactions to a comment.
func (s *Service) ListCommentReactions(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, content string, offset, limit int) ([]Reaction, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	t, err := s.commentTarget(ctx, r, id)
	if err != nil {
		return nil, err
	}
	return s.listReactions(ctx, t, content, offset, limit)
}

// AddCommentReaction reacts to a comment.
func (s *Service) AddCommentReaction(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, content string) ([]ReactionGroup, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	t, err := s.commentTarget(ctx, r, id)
	if err != nil {
		return nil, err
	}
	return s.react(ctx, who, r, t, content)
}

// RemoveCommentReaction removes the caller's reaction to a comment.
func (s *Service) RemoveCommentReaction(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, content string) ([]ReactionGroup, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	t, err := s.commentTarget(ctx, r, id)
	if err != nil {
		return nil, err
	}
	return s.unreact(ctx, who, t, content)
}
