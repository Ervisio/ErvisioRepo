package graphql

import (
	"context"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/pulls"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Hit is one result of Search: a repository, or an issue or pull request
// with its repository.
type Hit struct {
	Repo  repos.Repo
	Issue *issues.Issue
	Pull  *pulls.Pull
}

// Search runs the query of the search field with type ISSUE or REPOSITORY
// for the REST search endpoints, which share its qualifiers. It returns
// the hits off..off+lim, the total count and whether more hits follow. The
// caller checks the token scope.
func (s *Server) Search(ctx context.Context, who auth.Principal, kind, query string, off, lim int) ([]Hit, int, bool, error) {
	rq := newRequest(ctx, s, who)
	terms := splitQuery(query)
	var fetch fetchFn
	var err error
	switch strings.ToUpper(kind) {
	case "ISSUE":
		fetch, err = rq.searchIssues(terms)
	case "REPOSITORY":
		fetch, err = rq.searchRepos(terms)
	default:
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	total, err := (&conn{fetch: fetch}).count()
	if err != nil {
		return nil, 0, false, err
	}
	items, more, err := fetch(off, lim)
	if err != nil {
		return nil, 0, false, err
	}
	out := make([]Hit, 0, len(items))
	for _, it := range items {
		o, ok := it.(obj)
		if !ok {
			continue
		}
		switch v := o.v.(type) {
		case repos.Repo:
			out = append(out, Hit{Repo: v})
		case *issueV:
			i := v.i
			out = append(out, Hit{Repo: v.r, Issue: &i})
		case *pullV:
			p := v.p
			out = append(out, Hit{Repo: v.r, Pull: &p})
		}
	}
	return out, total, more, nil
}
