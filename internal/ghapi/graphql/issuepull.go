package graphql

import (
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/pulls"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/statuses"
)

// issueV is an issue.
type issueV struct {
	r repos.Repo
	i issues.Issue
}

// pullV is a pull request; files and reviews are loaded on demand.
type pullV struct {
	r       repos.Repo
	p       pulls.Pull
	files   []gitbrowse.FileChange
	fLoaded bool
	reviews []pulls.Review
	rLoaded bool
}

func issueObj(r repos.Repo, i issues.Issue) obj { return obj{"Issue", &issueV{r: r, i: i}} }
func pullObj(r repos.Repo, p pulls.Pull) obj    { return obj{"PullRequest", &pullV{r: r, p: p}} }

// subject returns the repository and the issue part of an issue or pull.
func subject(src any) (repos.Repo, issues.Issue, string) {
	switch v := src.(type) {
	case *issueV:
		return v.r, v.i, "Issue"
	case *pullV:
		return v.r, v.p.Issue, "PullRequest"
	}
	panic("graphql: not an issue")
}

func (rq *request) issueURL(r repos.Repo, i issues.Issue) string {
	if i.Kind == issues.KindPull {
		return rq.repoHTML(r) + "/pull/" + toString(i.Number)
	}
	return rq.repoHTML(r) + "/issues/" + toString(i.Number)
}

// association is the authorAssociation of user name on r.
func association(r repos.Repo, name string) string {
	if name != "" && strings.EqualFold(name, r.Owner) {
		return "OWNER"
	}
	return "NONE"
}

// issueFields are the fields shared by Issue and PullRequest.
func issueFields() map[string]resolver {
	f := func(fn func(rq *request, r repos.Repo, i issues.Issue) any) resolver {
		return func(rq *request, src any, a args) (any, error) {
			r, i, _ := subject(src)
			return fn(rq, r, i), nil
		}
	}
	return map[string]resolver{
		"id": func(rq *request, src any, a args) (any, error) {
			_, i, kind := subject(src)
			return nodeID(kind, i.ID), nil
		},
		"databaseId": func(rq *request, src any, a args) (any, error) {
			_, i, _ := subject(src)
			return rq.numID(i.ID)
		},
		"number":   f(func(rq *request, r repos.Repo, i issues.Issue) any { return i.Number }),
		"title":    f(func(rq *request, r repos.Repo, i issues.Issue) any { return i.Title }),
		"body":     f(func(rq *request, r repos.Repo, i issues.Issue) any { return i.Body }),
		"bodyText": f(func(rq *request, r repos.Repo, i issues.Issue) any { return i.Body }),
		"url":      f(func(rq *request, r repos.Repo, i issues.Issue) any { return rq.issueURL(r, i) }),
		"resourcePath": f(func(rq *request, r repos.Repo, i issues.Issue) any {
			return strings.TrimPrefix(rq.issueURL(r, i), rq.base())
		}),
		"closed": f(func(rq *request, r repos.Repo, i issues.Issue) any { return i.State == "closed" }),
		"locked": f(func(rq *request, r repos.Repo, i issues.Issue) any { return i.Locked }),
		"activeLockReason": f(func(rq *request, r repos.Repo, i issues.Issue) any {
			if !i.Locked || i.LockReason == "" {
				return nil
			}
			return strings.ToUpper(strings.ReplaceAll(i.LockReason, "-", "_"))
		}),
		"createdAt":    f(func(rq *request, r repos.Repo, i issues.Issue) any { return i.CreatedAt }),
		"updatedAt":    f(func(rq *request, r repos.Repo, i issues.Issue) any { return i.UpdatedAt }),
		"closedAt":     f(func(rq *request, r repos.Repo, i issues.Issue) any { return i.ClosedAt }),
		"lastEditedAt": f(func(rq *request, r repos.Repo, i issues.Issue) any { return i.EditedAt }),
		"includesCreatedEdit": f(func(rq *request, r repos.Repo, i issues.Issue) any {
			return i.EditedAt != nil
		}),
		"author":            f(func(rq *request, r repos.Repo, i issues.Issue) any { return userObj(i.Author) }),
		"authorAssociation": f(func(rq *request, r repos.Repo, i issues.Issue) any { return association(r, i.Author) }),
		"editor":            constant(nil),
		"viewerDidAuthor": f(func(rq *request, r repos.Repo, i issues.Issue) any {
			return strings.EqualFold(i.Author, rq.who.Username)
		}),
		"viewerCanUpdate": f(func(rq *request, r repos.Repo, i issues.Issue) any {
			return strings.EqualFold(i.Author, rq.who.Username) || r.Permission >= authz.PermWrite
		}),
		"viewerSubscription": constant("UNSUBSCRIBED"),
		"repository":         f(func(rq *request, r repos.Repo, i issues.Issue) any { return obj{"Repository", r} }),
		"labels": func(rq *request, src any, a args) (any, error) {
			r, i, _ := subject(src)
			items := make([]any, len(i.Labels))
			for k, l := range i.Labels {
				items[k] = labelObj(r, l)
			}
			return obj{"LabelConnection", staticConn("Label", a, items)}, nil
		},
		"assignees": func(rq *request, src any, a args) (any, error) {
			_, i, _ := subject(src)
			items := make([]any, len(i.Assignees))
			for k, n := range i.Assignees {
				items[k] = userObj(n)
			}
			return obj{"UserConnection", staticConn("User", a, items)}, nil
		},
		"participants": func(rq *request, src any, a args) (any, error) {
			_, i, _ := subject(src)
			var names []string
			for _, n := range append([]string{i.Author}, i.Assignees...) {
				if n != "" && !containsFold(names, n) {
					names = append(names, n)
				}
			}
			items := make([]any, len(names))
			for k, n := range names {
				items[k] = userObj(n)
			}
			return obj{"UserConnection", staticConn("User", a, items)}, nil
		},
		"milestone": f(func(rq *request, r repos.Repo, i issues.Issue) any {
			if i.Milestone == nil {
				return nil
			}
			return obj{"Milestone", &milestoneV{r: r, m: *i.Milestone}}
		}),
		"reactionGroups": f(func(rq *request, r repos.Repo, i issues.Issue) any { return reactionGroups(i.Reactions) }),
		"comments": func(rq *request, src any, a args) (any, error) {
			r, i, _ := subject(src)
			return rq.issueComments(r, i, a), nil
		},
		"projectCards":                   emptyConn("ProjectCard"),
		"projectItems":                   emptyConn("ProjectV2Item"),
		"closedByPullRequestsReferences": emptyConn("PullRequest"),
		"userContentEdits":               emptyConn("UserContentEdit"),
		"isPinned":                       constant(false),
	}
}

func issueOnlyFields() map[string]resolver {
	m := issueFields()
	m["state"] = func(rq *request, src any, a args) (any, error) {
		return strings.ToUpper(src.(*issueV).i.State), nil
	}
	m["stateReason"] = func(rq *request, src any, a args) (any, error) {
		i := src.(*issueV).i
		if i.StateReason == "" {
			return nil, nil
		}
		return strings.ToUpper(i.StateReason), nil
	}
	m["trackedIssues"] = emptyConn("Issue")
	m["timelineItems"] = emptyConn("IssueTimelineItems")
	return m
}

func (rq *request) issueComments(r repos.Repo, i issues.Issue, a args) obj {
	fetch := func(off, lim int) ([]any, bool, error) {
		list, err := rq.s.d.Issues.ListComments(rq.ctx, rq.who, r.Owner, r.Name, i.Number, off, lim)
		if err != nil {
			return nil, false, err
		}
		items := make([]any, len(list))
		for k, c := range list {
			items[k] = obj{"IssueComment", &commentV{r: r, kind: i.Kind, c: c}}
		}
		return items, len(list) == lim, nil
	}
	c := newConn("IssueComment", a, fetch)
	n := i.CommentCount
	c.total = func() (int, error) { return n, nil }
	return obj{"IssueCommentConnection", c}
}

// ---- comments ----

type commentV struct {
	r    repos.Repo
	kind string // of the issue
	c    issues.Comment
}

func commentFields() map[string]resolver {
	cm := func(src any) *commentV { return src.(*commentV) }
	f := func(fn func(rq *request, v *commentV) any) resolver {
		return func(rq *request, src any, a args) (any, error) { return fn(rq, cm(src)), nil }
	}
	url := func(rq *request, src any, a args) (any, error) {
		v := cm(src)
		n, err := rq.numID(v.c.ID)
		if err != nil {
			return nil, err
		}
		seg := "/issues/"
		if v.kind == issues.KindPull {
			seg = "/pull/"
		}
		return rq.repoHTML(v.r) + seg + toString(v.c.IssueNumber) + "#issuecomment-" + toString(n), nil
	}
	return map[string]resolver{
		"id":                  f(func(rq *request, v *commentV) any { return nodeID("IssueComment", v.c.ID) }),
		"databaseId":          func(rq *request, src any, a args) (any, error) { return rq.numID(cm(src).c.ID) },
		"author":              f(func(rq *request, v *commentV) any { return userObj(v.c.Author) }),
		"authorAssociation":   f(func(rq *request, v *commentV) any { return association(v.r, v.c.Author) }),
		"body":                f(func(rq *request, v *commentV) any { return v.c.Body }),
		"bodyText":            f(func(rq *request, v *commentV) any { return v.c.Body }),
		"createdAt":           f(func(rq *request, v *commentV) any { return v.c.CreatedAt }),
		"updatedAt":           f(func(rq *request, v *commentV) any { return v.c.UpdatedAt }),
		"publishedAt":         f(func(rq *request, v *commentV) any { return v.c.CreatedAt }),
		"lastEditedAt":        f(func(rq *request, v *commentV) any { return v.c.EditedAt }),
		"includesCreatedEdit": f(func(rq *request, v *commentV) any { return v.c.EditedAt != nil }),
		"editor":              f(func(rq *request, v *commentV) any { return userObj(v.c.EditedBy) }),
		"isMinimized":         constant(false),
		"minimizedReason":     constant(nil),
		"viewerDidAuthor":     f(func(rq *request, v *commentV) any { return strings.EqualFold(v.c.Author, rq.who.Username) }),
		"viewerCanUpdate":     f(func(rq *request, v *commentV) any { return strings.EqualFold(v.c.Author, rq.who.Username) }),
		"viewerCanDelete":     f(func(rq *request, v *commentV) any { return strings.EqualFold(v.c.Author, rq.who.Username) }),
		"reactionGroups":      f(func(rq *request, v *commentV) any { return reactionGroups(v.c.Reactions) }),
		"url":                 url,
		"repository":          f(func(rq *request, v *commentV) any { return obj{"Repository", v.r} }),
		"issue": func(rq *request, src any, a args) (any, error) {
			v := cm(src)
			return rq.issueByNumber(v.r, v.c.IssueNumber, true)
		},
		"pullRequest": func(rq *request, src any, a args) (any, error) {
			v := cm(src)
			if v.kind != issues.KindPull {
				return nil, nil
			}
			return rq.pullByNumber(v.r, v.c.IssueNumber)
		},
	}
}

// ---- pull requests ----

func (rq *request) pullFiles(v *pullV) ([]gitbrowse.FileChange, error) {
	if v.fLoaded {
		return v.files, nil
	}
	files, err := rq.s.d.Pulls.ListFiles(rq.ctx, rq.who, v.r.Owner, v.r.Name, v.p.Issue.Number)
	if err != nil {
		return nil, err
	}
	v.files, v.fLoaded = files, true
	return files, nil
}

func (rq *request) pullReviews(v *pullV) ([]pulls.Review, error) {
	if v.rLoaded {
		return v.reviews, nil
	}
	var all []pulls.Review
	for off := 0; off < maxScan; off += maxPage {
		list, err := rq.s.d.Pulls.ListReviews(rq.ctx, rq.who, v.r.Owner, v.r.Name, v.p.Issue.Number, off, maxPage)
		if err != nil {
			return nil, err
		}
		all = append(all, list...)
		if len(list) < maxPage {
			break
		}
	}
	v.reviews, v.rLoaded = all, true
	return all, nil
}

// latestReviews returns the latest submitted review of each reviewer.
func latestReviews(list []pulls.Review) []pulls.Review {
	idx := map[string]int{}
	var out []pulls.Review
	for _, r := range list {
		if r.State == "pending" || r.State == "dismissed" {
			continue
		}
		k := strings.ToLower(r.Reviewer)
		if i, ok := idx[k]; ok {
			// A comment does not replace an approval or a change request.
			if r.State == "commented" && out[i].State != "commented" {
				continue
			}
			out[i] = r
			continue
		}
		idx[k] = len(out)
		out = append(out, r)
	}
	return out
}

func (rq *request) reviewConn(v *pullV, a args, latest bool) (any, error) {
	list, err := rq.pullReviews(v)
	if err != nil {
		return nil, err
	}
	if latest {
		list = latestReviews(list)
	}
	states := enumList(a, "states")
	items := make([]any, 0, len(list))
	for _, r := range list {
		if len(states) > 0 && !containsFold(states, r.State) {
			continue
		}
		if r.State == "pending" && !strings.EqualFold(r.Reviewer, rq.who.Username) {
			continue
		}
		items = append(items, obj{"PullRequestReview", &reviewV{v: v, rv: r}})
	}
	return obj{"PullRequestReviewConnection", staticConn("PullRequestReview", a, items)}, nil
}

func mergeableEnum(m string) string {
	switch m {
	case pulls.MergeClean:
		return "MERGEABLE"
	case pulls.MergeConflict:
		return "CONFLICTING"
	}
	return "UNKNOWN"
}

func changeType(s string) string {
	switch s {
	case gitbrowse.StatusAdded:
		return "ADDED"
	case gitbrowse.StatusDeleted:
		return "DELETED"
	case gitbrowse.StatusRenamed:
		return "RENAMED"
	case gitbrowse.StatusCopied:
		return "COPIED"
	}
	return "MODIFIED"
}

func pullFields() map[string]resolver {
	m := issueFields()
	p := func(src any) *pullV { return src.(*pullV) }
	f := func(fn func(rq *request, v *pullV) any) resolver {
		return func(rq *request, src any, a args) (any, error) { return fn(rq, p(src)), nil }
	}
	files := func(fn func(files []gitbrowse.FileChange) any) resolver {
		return func(rq *request, src any, a args) (any, error) {
			fs, err := rq.pullFiles(p(src))
			if err != nil {
				return nil, err
			}
			return fn(fs), nil
		}
	}
	extra := map[string]resolver{
		"state":    f(func(rq *request, v *pullV) any { return strings.ToUpper(v.p.State) }),
		"isDraft":  f(func(rq *request, v *pullV) any { return v.p.Draft }),
		"merged":   f(func(rq *request, v *pullV) any { return v.p.State == "merged" }),
		"mergedAt": f(func(rq *request, v *pullV) any { return v.p.MergedAt }),
		"mergedBy": f(func(rq *request, v *pullV) any { return userObj(v.p.MergedBy) }),
		"mergeCommit": f(func(rq *request, v *pullV) any {
			if v.p.MergeCommit == "" {
				return nil
			}
			return commitObj(v.r, v.p.MergeCommit, nil)
		}),
		"potentialMergeCommit": constant(nil),
		"mergeable":            f(func(rq *request, v *pullV) any { return mergeableEnum(v.p.Mergeable) }),
		"mergeStateStatus": f(func(rq *request, v *pullV) any {
			switch {
			case v.p.State != "open":
				return "UNKNOWN"
			case v.p.Draft:
				return "DRAFT"
			case v.p.Mergeable == pulls.MergeClean:
				return "CLEAN"
			case v.p.Mergeable == pulls.MergeConflict:
				return "DIRTY"
			}
			return "UNKNOWN"
		}),
		"baseRefName": f(func(rq *request, v *pullV) any { return v.p.Base.Ref }),
		"headRefName": f(func(rq *request, v *pullV) any { return v.p.Head.Ref }),
		"baseRefOid":  f(func(rq *request, v *pullV) any { return v.p.Base.SHA }),
		"headRefOid":  f(func(rq *request, v *pullV) any { return v.p.Head.SHA }),
		"baseRef": f(func(rq *request, v *pullV) any {
			return obj{"Ref", &refV{r: v.r, name: v.p.Base.Ref, sha: v.p.Base.SHA}}
		}),
		"headRef": func(rq *request, src any, a args) (any, error) {
			v := p(src)
			hr, err := rq.headRepo(v)
			if err != nil || hr == nil || v.p.State != "open" {
				return nil, err
			}
			return obj{"Ref", &refV{r: *hr, name: v.p.Head.Ref, sha: v.p.Head.SHA}}, nil
		},
		"baseRepository": f(func(rq *request, v *pullV) any { return obj{"Repository", v.r} }),
		"headRepository": func(rq *request, src any, a args) (any, error) {
			hr, err := rq.headRepo(p(src))
			if err != nil || hr == nil {
				return nil, err
			}
			return obj{"Repository", *hr}, nil
		},
		"headRepositoryOwner": func(rq *request, src any, a args) (any, error) {
			owner, _, ok := strings.Cut(p(src).p.Head.Repo, "/")
			if !ok {
				return nil, nil
			}
			return rq.ownerByName(owner)
		},
		"isCrossRepository":   f(func(rq *request, v *pullV) any { return v.p.Head.RepoID != v.r.ID }),
		"maintainerCanModify": constant(false),
		"autoMergeRequest": f(func(rq *request, v *pullV) any {
			if v.p.AutoMerge == nil {
				return nil
			}
			return obj{"AutoMergeRequest", v.p.AutoMerge}
		}),
		"reviewDecision": func(rq *request, src any, a args) (any, error) {
			v := p(src)
			list, err := rq.pullReviews(v)
			if err != nil {
				return nil, err
			}
			approved := false
			for _, r := range latestReviews(list) {
				switch r.State {
				case "changes_requested":
					return "CHANGES_REQUESTED", nil
				case "approved":
					approved = true
				}
			}
			if approved {
				return "APPROVED", nil
			}
			if len(v.p.RequestedReviewers) > 0 {
				return "REVIEW_REQUIRED", nil
			}
			return nil, nil
		},
		"reviews": func(rq *request, src any, a args) (any, error) { return rq.reviewConn(p(src), a, false) },
		"latestReviews": func(rq *request, src any, a args) (any, error) {
			return rq.reviewConn(p(src), a, true)
		},
		"latestOpinionatedReviews": func(rq *request, src any, a args) (any, error) {
			return rq.reviewConn(p(src), a, true)
		},
		"reviewRequests": func(rq *request, src any, a args) (any, error) {
			v := p(src)
			items := make([]any, len(v.p.RequestedReviewers))
			for i, n := range v.p.RequestedReviewers {
				items[i] = obj{"ReviewRequest", n}
			}
			return obj{"ReviewRequestConnection", staticConn("ReviewRequest", a, items)}, nil
		},
		"additions": files(func(fs []gitbrowse.FileChange) any {
			n := 0
			for _, x := range fs {
				n += x.Additions
			}
			return n
		}),
		"deletions": files(func(fs []gitbrowse.FileChange) any {
			n := 0
			for _, x := range fs {
				n += x.Deletions
			}
			return n
		}),
		"changedFiles": files(func(fs []gitbrowse.FileChange) any { return len(fs) }),
		"files": func(rq *request, src any, a args) (any, error) {
			fs, err := rq.pullFiles(p(src))
			if err != nil {
				return nil, err
			}
			items := make([]any, len(fs))
			for i, x := range fs {
				items[i] = obj{"PullRequestChangedFile", x}
			}
			return obj{"PullRequestChangedFileConnection", staticConn("PullRequestChangedFile", a, items)}, nil
		},
		"commits": func(rq *request, src any, a args) (any, error) { return rq.pullCommits(p(src), a), nil },
		"statusCheckRollup": func(rq *request, src any, a args) (any, error) {
			v := p(src)
			return rq.rollup(v.r, v.p.Head.SHA)
		},
		"closingIssuesReferences": emptyConn("Issue"),
		"timelineItems":           emptyConn("PullRequestTimelineItems"),
	}
	for k, r := range extra {
		m[k] = r
	}
	return m
}

func (rq *request) headRepo(v *pullV) (*repos.Repo, error) {
	if v.p.Head.Repo == "" {
		return nil, nil
	}
	if v.p.Head.RepoID == v.r.ID {
		return &v.r, nil
	}
	hr, err := rq.repoFull(v.p.Head.Repo)
	if isNotFound(rq, err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &hr, nil
}

func (rq *request) pullCommits(v *pullV, a args) obj {
	fetch := func(off, lim int) ([]any, bool, error) {
		list, more, err := rq.s.d.Pulls.ListCommits(rq.ctx, rq.who, v.r.Owner, v.r.Name, v.p.Issue.Number, off, lim)
		if err != nil {
			return nil, false, err
		}
		items := make([]any, len(list))
		for i := range list {
			items[i] = obj{"PullRequestCommit", &prCommitV{pull: v, c: commitV{r: v.r, sha: list[i].SHA, c: &list[i]}}}
		}
		return items, more || len(list) == lim, nil
	}
	return obj{"PullRequestCommitConnection", newConn("PullRequestCommit", a, fetch)}
}

func changedFileFields() map[string]resolver {
	fc := func(src any) gitbrowse.FileChange { return src.(gitbrowse.FileChange) }
	return map[string]resolver{
		"path": func(rq *request, src any, a args) (any, error) {
			if fc(src).NewPath != "" {
				return fc(src).NewPath, nil
			}
			return fc(src).OldPath, nil
		},
		"additions":         func(rq *request, src any, a args) (any, error) { return fc(src).Additions, nil },
		"deletions":         func(rq *request, src any, a args) (any, error) { return fc(src).Deletions, nil },
		"changeType":        func(rq *request, src any, a args) (any, error) { return changeType(fc(src).Status), nil },
		"viewerViewedState": constant("UNVIEWED"),
	}
}

func reviewRequestFields() map[string]resolver {
	return map[string]resolver{
		"id":                constant(nil),
		"asCodeOwner":       constant(false),
		"requestedReviewer": func(rq *request, src any, a args) (any, error) { return userObj(src.(string)), nil },
	}
}

func autoMergeFields() map[string]resolver {
	am := func(src any) *pulls.AutoMerge { return src.(*pulls.AutoMerge) }
	return map[string]resolver{
		"mergeMethod":    func(rq *request, src any, a args) (any, error) { return strings.ToUpper(am(src).Method), nil },
		"enabledBy":      func(rq *request, src any, a args) (any, error) { return userObj(am(src).EnabledBy), nil },
		"commitHeadline": func(rq *request, src any, a args) (any, error) { return am(src).Title, nil },
		"commitBody":     func(rq *request, src any, a args) (any, error) { return am(src).Message, nil },
		"enabledAt":      constant(nil),
	}
}

// ---- reviews ----

type reviewV struct {
	v  *pullV
	rv pulls.Review
}

func reviewFields() map[string]resolver {
	r := func(src any) *reviewV { return src.(*reviewV) }
	f := func(fn func(rq *request, v *reviewV) any) resolver {
		return func(rq *request, src any, a args) (any, error) { return fn(rq, r(src)), nil }
	}
	return map[string]resolver{
		"id":                  f(func(rq *request, v *reviewV) any { return nodeID("PullRequestReview", v.rv.ID) }),
		"databaseId":          func(rq *request, src any, a args) (any, error) { return rq.numID(r(src).rv.ID) },
		"author":              f(func(rq *request, v *reviewV) any { return userObj(v.rv.Reviewer) }),
		"authorAssociation":   f(func(rq *request, v *reviewV) any { return association(v.v.r, v.rv.Reviewer) }),
		"body":                f(func(rq *request, v *reviewV) any { return v.rv.Body }),
		"bodyText":            f(func(rq *request, v *reviewV) any { return v.rv.Body }),
		"state":               f(func(rq *request, v *reviewV) any { return strings.ToUpper(v.rv.State) }),
		"submittedAt":         f(func(rq *request, v *reviewV) any { return v.rv.SubmittedAt }),
		"createdAt":           f(func(rq *request, v *reviewV) any { return v.rv.CreatedAt }),
		"updatedAt":           f(func(rq *request, v *reviewV) any { return v.rv.CreatedAt }),
		"publishedAt":         f(func(rq *request, v *reviewV) any { return v.rv.SubmittedAt }),
		"lastEditedAt":        constant(nil),
		"includesCreatedEdit": constant(false),
		"isMinimized":         constant(false),
		"minimizedReason":     constant(nil),
		"reactionGroups":      constant([]any{}),
		"viewerDidAuthor":     f(func(rq *request, v *reviewV) any { return strings.EqualFold(v.rv.Reviewer, rq.who.Username) }),
		"pullRequest":         f(func(rq *request, v *reviewV) any { return obj{"PullRequest", v.v} }),
		"commit": f(func(rq *request, v *reviewV) any {
			if v.rv.CommitSHA == "" {
				return nil
			}
			return commitObj(v.v.r, v.rv.CommitSHA, nil)
		}),
		"url": func(rq *request, src any, a args) (any, error) {
			v := r(src)
			n, err := rq.numID(v.rv.ID)
			if err != nil {
				return nil, err
			}
			return rq.issueURL(v.v.r, v.v.p.Issue) + "#pullrequestreview-" + toString(n), nil
		},
		"comments": emptyConn("PullRequestReviewComment"),
	}
}

// ---- commits ----

// commitV is a commit; c is nil when only the SHA is known.
type commitV struct {
	r   repos.Repo
	sha string
	c   *gitbrowse.Commit
}

type prCommitV struct {
	pull *pullV
	c    commitV
}

func commitObj(r repos.Repo, sha string, c *gitbrowse.Commit) obj {
	return obj{"Commit", &commitV{r: r, sha: sha, c: c}}
}

func personObj(p gitbrowse.Person) obj { return obj{"GitActor", p} }

func commitFields() map[string]resolver {
	c := func(src any) *commitV { return src.(*commitV) }
	f := func(fn func(rq *request, v *commitV) any) resolver {
		return func(rq *request, src any, a args) (any, error) { return fn(rq, c(src)), nil }
	}
	data := func(fn func(gc *gitbrowse.Commit) any) resolver {
		return func(rq *request, src any, a args) (any, error) {
			if c(src).c == nil {
				return nil, nil
			}
			return fn(c(src).c), nil
		}
	}
	return map[string]resolver{
		"id": f(func(rq *request, v *commitV) any {
			return nodeIDString("Commit", v.r.Owner+"/"+v.r.Name+"@"+v.sha)
		}),
		"oid": f(func(rq *request, v *commitV) any { return v.sha }),
		"abbreviatedOid": f(func(rq *request, v *commitV) any {
			if len(v.sha) > 7 {
				return v.sha[:7]
			}
			return v.sha
		}),
		"url":             f(func(rq *request, v *commitV) any { return rq.repoHTML(v.r) + "/commit/" + v.sha }),
		"commitUrl":       f(func(rq *request, v *commitV) any { return rq.repoHTML(v.r) + "/commit/" + v.sha }),
		"message":         data(func(gc *gitbrowse.Commit) any { return gc.Message }),
		"messageHeadline": data(func(gc *gitbrowse.Commit) any { return gc.Summary }),
		"messageBody": data(func(gc *gitbrowse.Commit) any {
			_, body, _ := strings.Cut(gc.Message, "\n")
			return strings.TrimSpace(body)
		}),
		"authoredDate":  data(func(gc *gitbrowse.Commit) any { return gc.Author.Date }),
		"committedDate": data(func(gc *gitbrowse.Commit) any { return gc.Committer.Date }),
		"author":        data(func(gc *gitbrowse.Commit) any { return personObj(gc.Author) }),
		"committer":     data(func(gc *gitbrowse.Commit) any { return personObj(gc.Committer) }),
		"authors": data(func(gc *gitbrowse.Commit) any {
			return obj{"GitActorConnection", staticConn("GitActor", args{}, []any{personObj(gc.Author)})}
		}),
		"repository": f(func(rq *request, v *commitV) any { return obj{"Repository", v.r} }),
		"statusCheckRollup": func(rq *request, src any, a args) (any, error) {
			return rq.rollup(c(src).r, c(src).sha)
		},
		"signature": constant(nil),
	}
}

func prCommitFields() map[string]resolver {
	return map[string]resolver{
		"id": func(rq *request, src any, a args) (any, error) {
			v := src.(*prCommitV)
			return nodeIDString("PullRequestCommit", v.pull.p.Issue.ID.String()+":"+v.c.sha), nil
		},
		"commit": func(rq *request, src any, a args) (any, error) {
			v := src.(*prCommitV)
			return obj{"Commit", &v.c}, nil
		},
		"pullRequest": func(rq *request, src any, a args) (any, error) { return obj{"PullRequest", src.(*prCommitV).pull}, nil },
		"url": func(rq *request, src any, a args) (any, error) {
			v := src.(*prCommitV)
			return rq.issueURL(v.pull.r, v.pull.p.Issue) + "/commits/" + v.c.sha, nil
		},
	}
}

func gitActorFields() map[string]resolver {
	p := func(src any) gitbrowse.Person { return src.(gitbrowse.Person) }
	return map[string]resolver{
		"name":  func(rq *request, src any, a args) (any, error) { return p(src).Name, nil },
		"email": func(rq *request, src any, a args) (any, error) { return p(src).Email, nil },
		"date":  func(rq *request, src any, a args) (any, error) { return p(src).Date, nil },
		"user": func(rq *request, src any, a args) (any, error) {
			return nil, nil
		},
		"avatarUrl": constant(""),
	}
}

// ---- status checks ----

type rollupV struct {
	state    string
	contexts []any
}

// rollup combines the commit statuses and the latest check runs of sha;
// nil when there are none or no status source is configured.
func (rq *request) rollup(r repos.Repo, sha string) (any, error) {
	if rq.s.d.Checks == nil || sha == "" {
		return nil, nil
	}
	comb, err := rq.s.d.Checks.CombinedStatus(rq.ctx, rq.who, r.Owner, r.Name, sha)
	if err != nil {
		if isNotFound(rq, err) {
			return nil, nil
		}
		return nil, err
	}
	runs, _, _, err := rq.s.d.Checks.ListCheckRuns(rq.ctx, rq.who, r.Owner, r.Name, sha, statuses.CheckRunFilter{Limit: maxPage})
	if err != nil {
		if isNotFound(rq, err) {
			return nil, nil
		}
		return nil, err
	}
	var items []any
	failed, pending := false, false
	for _, s := range comb.Statuses {
		items = append(items, obj{"StatusContext", s})
		switch s.State {
		case "error", "failure":
			failed = true
		case "pending":
			pending = true
		}
	}
	for _, c := range runs {
		items = append(items, obj{"CheckRun", c})
		switch {
		case c.Status != "completed":
			pending = true
		case c.Conclusion == "failure" || c.Conclusion == "timed_out" || c.Conclusion == "cancelled" ||
			c.Conclusion == "action_required" || c.Conclusion == "startup_failure":
			failed = true
		}
	}
	if len(items) == 0 {
		return nil, nil
	}
	state := "SUCCESS"
	switch {
	case failed:
		state = "FAILURE"
	case pending:
		state = "PENDING"
	}
	return obj{"StatusCheckRollup", &rollupV{state: state, contexts: items}}, nil
}

func rollupFields() map[string]resolver {
	return map[string]resolver{
		"state": func(rq *request, src any, a args) (any, error) { return src.(*rollupV).state, nil },
		"contexts": func(rq *request, src any, a args) (any, error) {
			return obj{"StatusCheckRollupContextConnection", staticConn("StatusCheckRollupContext", a, src.(*rollupV).contexts)}, nil
		},
	}
}

func statusContextFields() map[string]resolver {
	s := func(src any) statuses.Status { return src.(statuses.Status) }
	return map[string]resolver{
		"id":          func(rq *request, src any, a args) (any, error) { return nodeID("StatusContext", s(src).ID), nil },
		"context":     func(rq *request, src any, a args) (any, error) { return s(src).Context, nil },
		"state":       func(rq *request, src any, a args) (any, error) { return strings.ToUpper(s(src).State), nil },
		"description": func(rq *request, src any, a args) (any, error) { return optString(s(src).Description), nil },
		"targetUrl":   func(rq *request, src any, a args) (any, error) { return optString(s(src).TargetURL), nil },
		"createdAt":   func(rq *request, src any, a args) (any, error) { return s(src).CreatedAt, nil },
		"creator":     func(rq *request, src any, a args) (any, error) { return userObj(s(src).Creator), nil },
		"avatarUrl":   constant(nil),
		"isRequired":  constant(false),
	}
}

func checkRunFields() map[string]resolver {
	c := func(src any) statuses.CheckRun { return src.(statuses.CheckRun) }
	return map[string]resolver{
		"id":         func(rq *request, src any, a args) (any, error) { return nodeID("CheckRun", c(src).ID), nil },
		"databaseId": func(rq *request, src any, a args) (any, error) { return rq.numID(c(src).ID) },
		"name":       func(rq *request, src any, a args) (any, error) { return c(src).Name, nil },
		"status":     func(rq *request, src any, a args) (any, error) { return strings.ToUpper(c(src).Status), nil },
		"conclusion": func(rq *request, src any, a args) (any, error) {
			if c(src).Conclusion == "" {
				return nil, nil
			}
			return strings.ToUpper(c(src).Conclusion), nil
		},
		"startedAt":   func(rq *request, src any, a args) (any, error) { return c(src).StartedAt, nil },
		"completedAt": func(rq *request, src any, a args) (any, error) { return c(src).CompletedAt, nil },
		"detailsUrl":  func(rq *request, src any, a args) (any, error) { return optString(c(src).DetailsURL), nil },
		"title":       func(rq *request, src any, a args) (any, error) { return optString(c(src).Title), nil },
		"summary":     func(rq *request, src any, a args) (any, error) { return optString(c(src).Summary), nil },
		"text":        func(rq *request, src any, a args) (any, error) { return optString(c(src).Text), nil },
		"isRequired":  constant(false),
		"checkSuite":  func(rq *request, src any, a args) (any, error) { return obj{"CheckSuite", c(src)}, nil },
	}
}

func checkSuiteFields() map[string]resolver {
	return map[string]resolver{
		"app": func(rq *request, src any, a args) (any, error) {
			if src.(statuses.CheckRun).App == "" {
				return nil, nil
			}
			return obj{"App", src.(statuses.CheckRun).App}, nil
		},
		"workflowRun": constant(nil),
	}
}

func appFields() map[string]resolver {
	return map[string]resolver{
		"name": func(rq *request, src any, a args) (any, error) { return src.(string), nil },
		"slug": func(rq *request, src any, a args) (any, error) { return src.(string), nil },
	}
}
