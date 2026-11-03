package pulls

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// ---- cross-references ----

// recordRefs records the issue, user and commit references written in the
// body of pull request row (reviewComment = uuid.Nil) or in one of its
// published review comments, so the referenced issues show the pull
// request (LinkedPulls) and a cross-referenced timeline entry.
func (s *Service) recordRefs(ctx context.Context, q *dbgen.Queries, r repos.Repo, who auth.Principal, row dbgen.Issue, reviewComment uuid.UUID, text string) error {
	if s.o.Issues == nil || text == "" {
		return nil
	}
	if err := s.o.Issues.RecordRefs(ctx, q, r, who, row, reviewComment, text); err != nil {
		return fmt.Errorf("record references: %w", err)
	}
	return nil
}

// LinkedPull is a pull request that references an issue.
type LinkedPull struct {
	Repo   string // owner/name of the pull request's repository
	Number int64
	Title  string
	State  string // open, closed or merged
	Draft  bool
	Author string
}

// LinkedPulls returns the pull requests, readable by who, whose body or
// comments reference issue number of owner/name (newest first, at most 100).
func (s *Service) LinkedPulls(ctx context.Context, who auth.Principal, owner, name string, number int64) ([]LinkedPull, error) {
	r, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	i, err := s.q.GetIssueByNumber(ctx, dbgen.GetIssueByNumberParams{RepoID: r.ID, Number: number})
	if err != nil {
		return nil, notFound(err, issues.ErrNotFound, "get issue")
	}
	rows, err := s.q.LinkedPullsOfIssue(ctx, nullID(i.ID))
	if err != nil {
		return nil, fmt.Errorf("linked pull requests: %w", err)
	}
	full := map[uuid.UUID]string{r.ID: r.FullName()}
	logins := map[uuid.UUID]string{}
	out := []LinkedPull{}
	for _, row := range rows {
		name, seen := full[row.Issue.RepoID]
		if !seen {
			pr, err := s.o.Repos.GetByID(ctx, who, row.Issue.RepoID)
			if err != nil && !errors.Is(err, repos.ErrNotFound) {
				return nil, err
			}
			if err == nil {
				name = pr.FullName()
			}
			full[row.Issue.RepoID] = name
		}
		if name == "" {
			continue // not readable by who
		}
		state := row.Issue.State
		if row.MergedAt.Valid {
			state = StateMerged
		}
		lp := LinkedPull{Repo: name, Number: row.Issue.Number, Title: row.Issue.Title, State: state, Draft: row.Draft}
		if a := row.Issue.AuthorID; a.Valid {
			login, ok := logins[a.UUID]
			if !ok {
				if u, err := s.q.GetUserByID(ctx, a.UUID); err == nil {
					login = u.Username
				}
				logins[a.UUID] = login
			}
			lp.Author = login
		}
		out = append(out, lp)
	}
	return out, nil
}
