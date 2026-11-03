package graphql

import (
	"strconv"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/pulls"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// orderBy reads an IssueOrder/PullRequestOrder argument.
func orderBy(a args) (sort string, asc bool) {
	o := a.obj("orderBy")
	switch strings.ToUpper(o.str("field")) {
	case "UPDATED_AT":
		sort = "updated"
	case "COMMENTS":
		sort = "comments"
	default:
		sort = "created"
	}
	// GitHub lists in ascending order unless told otherwise.
	asc = !strings.EqualFold(o.str("direction"), "DESC")
	return sort, asc
}

// me resolves the @me qualifier.
func (rq *request) me(s string) string {
	if strings.EqualFold(s, "@me") {
		return rq.who.Username
	}
	return s
}

// milestoneFilter maps a milestone argument (database id, number, title,
// "*" or "none") to the filter of issues.ListInput; ok false when it
// names no milestone.
func (rq *request) milestoneFilter(r repos.Repo, m string) (string, bool, error) {
	switch m {
	case "", "*", "none":
		return m, true, nil
	}
	if n, err := strconv.ParseInt(m, 10, 64); err == nil {
		items, _, err := filtered(rq.milestoneFetch(r, "all"), func(it any) bool {
			id, err := rq.numID(it.(obj).v.(*milestoneV).m.ID)
			return err == nil && id == n
		})(0, 1)
		if err != nil {
			return "", false, err
		}
		if len(items) == 1 {
			return items[0].(obj).v.(*milestoneV).m.ID.String(), true, nil
		}
	}
	ms, err := rq.milestoneByTitle(r, m)
	if err != nil || ms == nil {
		return "", false, err
	}
	return ms.ID.String(), true, nil
}

// issueInput builds the filter of an issues connection.
func (rq *request) issueInput(r repos.Repo, a args) (issues.ListInput, bool, error) {
	in := issues.ListInput{State: "all"}
	switch st := enumList(a, "states"); {
	case len(st) == 1 && st[0] == "OPEN":
		in.State = "open"
	case len(st) == 1 && st[0] == "CLOSED":
		in.State = "closed"
	}
	f := a.obj("filterBy")
	in.Labels = append(a.list("labels"), f.list("labels")...)
	in.Assignee = rq.me(f.str("assignee"))
	in.Author = rq.me(f.str("createdBy"))
	in.Mentioned = rq.me(f.str("mentioned"))
	if st := enumList(f, "states"); len(st) == 1 {
		in.State = strings.ToLower(st[0])
	}
	m := f.str("milestone")
	if m == "" {
		m = f.str("milestoneNumber")
	}
	ms, ok, err := rq.milestoneFilter(r, m)
	if err != nil || !ok {
		return in, false, err
	}
	in.Milestone = ms
	in.Sort, in.Ascending = orderBy(a)
	return in, true, nil
}

func (rq *request) repoIssues(r repos.Repo, a args) (any, error) {
	in, ok, err := rq.issueInput(r, a)
	if err != nil {
		return nil, err
	}
	if !ok {
		return obj{"IssueConnection", staticConn("Issue", a, nil)}, nil
	}
	fetch := func(off, lim int) ([]any, bool, error) {
		q := in
		q.Offset, q.Limit = off, lim
		list, err := rq.s.d.Issues.List(rq.ctx, rq.who, r.Owner, r.Name, q)
		if err != nil {
			return nil, false, err
		}
		items := make([]any, len(list))
		for i, x := range list {
			items[i] = issueObj(r, x)
		}
		return items, len(list) == lim, nil
	}
	return obj{"IssueConnection", newConn("Issue", a, fetch)}, nil
}

// pullOptions maps the states of a pullRequests connection to the filter
// of the service and a post-filter on the state.
func pullOptions(states []string) (string, func(pulls.Pull) bool) {
	has := map[string]bool{}
	for _, s := range states {
		has[s] = true
	}
	all := func(pulls.Pull) bool { return true }
	switch {
	case len(has) == 0 || (has["OPEN"] && has["CLOSED"] && has["MERGED"]):
		return "all", all
	case len(has) == 1 && has["OPEN"]:
		return "open", all
	case len(has) == 1 && has["MERGED"]:
		return "merged", all
	case len(has) == 2 && has["CLOSED"] && has["MERGED"]:
		return "closed", all
	case len(has) == 1 && has["CLOSED"]:
		return "closed", func(p pulls.Pull) bool { return p.State == "closed" }
	}
	return "all", func(p pulls.Pull) bool { return has[strings.ToUpper(p.State)] }
}

func (rq *request) repoPulls(r repos.Repo, a args) (any, error) {
	state, keepState := pullOptions(enumList(a, "states"))
	o := pulls.ListOptions{State: state, Base: a.str("baseRefName"), Head: a.str("headRefName")}
	if sort, _ := orderBy(a); sort == "updated" {
		o.Sort = "updated"
	}
	labels := a.list("labels")
	keep := func(it any) bool {
		p := it.(obj).v.(*pullV).p
		if !keepState(p) {
			return false
		}
		if len(labels) == 0 {
			return true
		}
		for _, l := range p.Issue.Labels {
			if containsFold(labels, l.Name) {
				return true
			}
		}
		return false
	}
	return obj{"PullRequestConnection", newConn("PullRequest", a, filtered(rq.pullFetch(r, o), keep))}, nil
}

// pullFetch pages over the pull requests of r matching o.
func (rq *request) pullFetch(r repos.Repo, o pulls.ListOptions) fetchFn {
	return func(off, lim int) ([]any, bool, error) {
		q := o
		q.Offset, q.Limit = off, lim
		list, err := rq.s.d.Pulls.List(rq.ctx, rq.who, r.Owner, r.Name, q)
		if err != nil {
			return nil, false, err
		}
		items := make([]any, len(list))
		for i, p := range list {
			items[i] = pullObj(r, p)
		}
		return items, len(list) == lim, nil
	}
}

// issueByNumber is repository.issue (orPull false) or
// repository.issueOrPullRequest (orPull true).
func (rq *request) issueByNumber(r repos.Repo, n int64, orPull bool) (any, error) {
	i, err := rq.s.d.Issues.Get(rq.ctx, rq.who, r.Owner, r.Name, n)
	if isNotFound(rq, err) {
		if orPull {
			return nil, errorf("NOT_FOUND", "Could not resolve to an issue or pull request with the number of %d.", n)
		}
		return nil, errorf("NOT_FOUND", "Could not resolve to an Issue with the number of %d.", n)
	}
	if err != nil {
		return nil, err
	}
	if i.Kind == issues.KindPull {
		if !orPull {
			return nil, errorf("NOT_FOUND", "Could not resolve to an Issue with the number of %d.", n)
		}
		return rq.pullByNumber(r, n)
	}
	return issueObj(r, i), nil
}

func (rq *request) pullByNumber(r repos.Repo, n int64) (any, error) {
	p, err := rq.s.d.Pulls.Get(rq.ctx, rq.who, r.Owner, r.Name, n)
	if isNotFound(rq, err) {
		return nil, errorf("NOT_FOUND", "Could not resolve to a PullRequest with the number of %d.", n)
	}
	if err != nil {
		return nil, err
	}
	return pullObj(r, p), nil
}
