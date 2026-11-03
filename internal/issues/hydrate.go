package issues

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Reaction contents, in display order.
var reactionContents = []string{"+1", "-1", "laugh", "hooray", "confused", "heart", "rocket", "eyes"}

func reactionRank(c string) int {
	for i, v := range reactionContents {
		if v == c {
			return i
		}
	}
	return len(reactionContents)
}

// ValidReaction reports whether c is a supported reaction content.
func ValidReaction(c string) bool { return reactionRank(c) < len(reactionContents) }

func sortGroups(g []ReactionGroup) {
	sort.Slice(g, func(a, b int) bool { return reactionRank(g[a].Content) < reactionRank(g[b].Content) })
}

func milestoneFrom(m dbgen.Milestone, open, closed int64) Milestone {
	return Milestone{ID: m.ID, Title: m.Title, Description: m.Description, State: m.State, DueOn: tsPtr(m.DueOn),
		OpenIssues: open, ClosedIssues: closed, CreatedAt: m.CreatedAt.Time, UpdatedAt: m.UpdatedAt.Time,
		ClosedAt: tsPtr(m.ClosedAt)}
}

func labelFrom(l dbgen.Label) Label {
	return Label{ID: l.ID, Name: l.Name, Color: l.Color, Description: l.Description}
}

// hydrate turns issue rows into Issues with labels, assignees, milestone,
// reactions (as seen by viewer) and user names.
func (s *Service) hydrate(ctx context.Context, viewer uuid.UUID, rows []dbgen.Issue) ([]Issue, error) {
	if len(rows) == 0 {
		return []Issue{}, nil
	}
	ids := make([]uuid.UUID, 0, len(rows))
	users := []uuid.UUID{}
	msByRepo := map[uuid.UUID][]uuid.UUID{}
	for _, r := range rows {
		ids = append(ids, r.ID)
		users = append(users, r.AuthorID.UUID, r.ClosedBy.UUID)
		if r.MilestoneID.Valid {
			msByRepo[r.RepoID] = append(msByRepo[r.RepoID], r.MilestoneID.UUID)
		}
	}
	labels := map[uuid.UUID][]Label{}
	lrows, err := s.q.IssueLabelsOf(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("issue labels: %w", err)
	}
	for _, l := range lrows {
		labels[l.IssueID] = append(labels[l.IssueID], Label{ID: l.ID, Name: l.Name, Color: l.Color, Description: l.Description})
	}
	assignees := map[uuid.UUID][]string{}
	arows, err := s.q.IssueAssigneesOf(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("issue assignees: %w", err)
	}
	for _, a := range arows {
		assignees[a.IssueID] = append(assignees[a.IssueID], a.Name)
	}
	reactions := map[uuid.UUID][]ReactionGroup{}
	rrows, err := s.q.IssueReactionGroups(ctx, dbgen.IssueReactionGroupsParams{Viewer: viewer, IssueIds: ids})
	if err != nil {
		return nil, fmt.Errorf("issue reactions: %w", err)
	}
	for _, g := range rrows {
		reactions[g.IssueID] = append(reactions[g.IssueID], ReactionGroup{Content: g.Content, Count: g.Total, ViewerReacted: g.ViewerReacted})
	}
	milestones := map[uuid.UUID]Milestone{}
	for repo, mids := range msByRepo {
		ms, err := s.q.GetMilestonesByIDs(ctx, dbgen.GetMilestonesByIDsParams{RepoID: repo, Ids: mids})
		if err != nil {
			return nil, fmt.Errorf("issue milestones: %w", err)
		}
		for _, m := range ms {
			milestones[m.ID] = milestoneFrom(dbgen.Milestone{ID: m.ID, RepoID: m.RepoID, Title: m.Title, Description: m.Description,
				State: m.State, DueOn: m.DueOn, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, ClosedAt: m.ClosedAt},
				m.OpenIssues, m.ClosedIssues)
		}
	}
	names, err := s.names(ctx, users)
	if err != nil {
		return nil, err
	}
	out := make([]Issue, 0, len(rows))
	for _, r := range rows {
		i := Issue{ID: r.ID, RepoID: r.RepoID, Number: r.Number, Kind: r.Kind, Title: r.Title, Body: r.Body, State: r.State,
			StateReason: r.StateReason, Locked: r.Locked, LockReason: r.LockReason, AuthorID: r.AuthorID.UUID,
			Author: names[r.AuthorID.UUID], Labels: labels[r.ID], Assignees: assignees[r.ID], CommentCount: int(r.CommentCount),
			Reactions: reactions[r.ID], CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time, ClosedAt: tsPtr(r.ClosedAt),
			ClosedBy: names[r.ClosedBy.UUID], EditedAt: tsPtr(r.EditedAt)}
		if i.Labels == nil {
			i.Labels = []Label{}
		}
		if i.Assignees == nil {
			i.Assignees = []string{}
		}
		if i.Reactions == nil {
			i.Reactions = []ReactionGroup{}
		}
		sortGroups(i.Reactions)
		if r.MilestoneID.Valid {
			if m, ok := milestones[r.MilestoneID.UUID]; ok {
				i.Milestone = &m
			}
		}
		out = append(out, i)
	}
	return out, nil
}

func (s *Service) hydrateOne(ctx context.Context, viewer uuid.UUID, row dbgen.Issue) (Issue, error) {
	out, err := s.hydrate(ctx, viewer, []dbgen.Issue{row})
	if err != nil {
		return Issue{}, err
	}
	return out[0], nil
}

// hydrateComments turns comment rows into Comments.
func (s *Service) hydrateComments(ctx context.Context, viewer uuid.UUID, rows []dbgen.IssueComment) ([]Comment, error) {
	if len(rows) == 0 {
		return []Comment{}, nil
	}
	ids := make([]uuid.UUID, 0, len(rows))
	users := []uuid.UUID{}
	issueIDs := map[uuid.UUID]bool{}
	for _, c := range rows {
		ids = append(ids, c.ID)
		users = append(users, c.AuthorID.UUID, c.EditedBy.UUID)
		issueIDs[c.IssueID] = true
	}
	numbers := map[uuid.UUID]int64{}
	for id := range issueIDs {
		i, err := s.q.GetIssueByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("comment issue: %w", err)
		}
		numbers[id] = i.Number
	}
	groups := map[uuid.UUID][]ReactionGroup{}
	grows, err := s.q.CommentReactionGroups(ctx, dbgen.CommentReactionGroupsParams{Viewer: viewer, CommentIds: ids})
	if err != nil {
		return nil, fmt.Errorf("comment reactions: %w", err)
	}
	for _, g := range grows {
		groups[g.CommentID] = append(groups[g.CommentID], ReactionGroup{Content: g.Content, Count: g.Total, ViewerReacted: g.ViewerReacted})
	}
	names, err := s.names(ctx, users)
	if err != nil {
		return nil, err
	}
	out := make([]Comment, 0, len(rows))
	for _, c := range rows {
		g := groups[c.ID]
		if g == nil {
			g = []ReactionGroup{}
		}
		sortGroups(g)
		out = append(out, Comment{ID: c.ID, IssueID: c.IssueID, IssueNumber: numbers[c.IssueID], AuthorID: c.AuthorID.UUID,
			Author: names[c.AuthorID.UUID], Body: c.Body, CreatedAt: c.CreatedAt.Time, UpdatedAt: c.UpdatedAt.Time,
			EditedAt: tsPtr(c.EditedAt), EditedBy: names[c.EditedBy.UUID], Reactions: g})
	}
	return out, nil
}

func (s *Service) edits(ctx context.Context, rows []dbgen.IssueEdit) ([]BodyEdit, error) {
	users := make([]uuid.UUID, 0, len(rows))
	for _, e := range rows {
		users = append(users, e.EditorID.UUID)
	}
	names, err := s.names(ctx, users)
	if err != nil {
		return nil, err
	}
	out := make([]BodyEdit, 0, len(rows))
	for _, e := range rows {
		out = append(out, BodyEdit{ID: e.ID, Editor: names[e.EditorID.UUID], Body: e.Body, CreatedAt: e.CreatedAt.Time})
	}
	return out, nil
}
