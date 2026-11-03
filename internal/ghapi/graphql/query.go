package graphql

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/pulls"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// queryFields are the fields of the Query root.
func queryFields() map[string]resolver {
	return map[string]resolver{
		"viewer": func(rq *request, src any, a args) (any, error) {
			return obj{"User", &userV{name: rq.who.Username, id: rq.who.UserID}}, nil
		},
		"repository": func(rq *request, src any, a args) (any, error) {
			owner, name := a.str("owner"), a.str("name")
			r, err := rq.repo(owner, name)
			if isNotFound(rq, err) {
				return nil, errorf("NOT_FOUND", "Could not resolve to a Repository with the name '%s/%s'.", owner, name)
			}
			if err != nil {
				return nil, err
			}
			return obj{"Repository", r}, nil
		},
		"repositoryOwner": func(rq *request, src any, a args) (any, error) {
			return rq.ownerByName(a.str("login"))
		},
		"user": func(rq *request, src any, a args) (any, error) {
			login := a.str("login")
			ns, err := rq.namespace(login)
			if err != nil {
				return nil, err
			}
			if ns == nil || ns.Kind == "org" {
				return nil, errorf("NOT_FOUND", "Could not resolve to a User with the login of '%s'.", login)
			}
			return obj{"User", &userV{name: ns.Name, id: ns.ID}}, nil
		},
		"organization": func(rq *request, src any, a args) (any, error) {
			login := a.str("login")
			ns, err := rq.namespace(login)
			if err != nil {
				return nil, err
			}
			if ns == nil || ns.Kind != "org" {
				return nil, errorf("NOT_FOUND", "Could not resolve to an Organization with the login of '%s'.", login)
			}
			return obj{"Organization", &orgV{name: ns.Name, id: ns.ID}}, nil
		},
		"node": func(rq *request, src any, a args) (any, error) { return rq.node(a.str("id")) },
		"nodes": func(rq *request, src any, a args) (any, error) {
			ids := a.list("ids")
			if len(ids) > maxPage {
				return nil, errorf("UNPROCESSABLE", "at most %d ids may be given", maxPage)
			}
			out := make([]any, len(ids))
			for i, id := range ids {
				n, err := rq.node(id)
				if err != nil {
					return nil, err
				}
				out[i] = n
			}
			return out, nil
		},
		"search": func(rq *request, src any, a args) (any, error) { return rq.search(a) },
		"rateLimit": func(rq *request, src any, a args) (any, error) {
			return obj{"RateLimit", time.Now().Add(time.Hour).Truncate(time.Second)}, nil
		},
		"__type": func(rq *request, src any, a args) (any, error) {
			t := rq.s.sch.lookup(a.str("name"))
			if t == nil {
				return nil, nil
			}
			return obj{"__Type", t}, nil
		},
		"__schema": func(rq *request, src any, a args) (any, error) { return obj{"__Schema", rq.s.sch}, nil },
	}
}

func rateLimitFields() map[string]resolver {
	return map[string]resolver{
		"limit":     constant(5000),
		"cost":      constant(1),
		"remaining": constant(5000),
		"used":      constant(0),
		"nodeCount": constant(0),
		"resetAt":   func(rq *request, src any, a args) (any, error) { return src.(time.Time), nil },
	}
}

// ---- nodes ----

// issueRow loads the issue row behind the global id of an issue or pull.
func (rq *request) issueRow(id uuid.UUID) (repos.Repo, int64, string, error) {
	row, err := rq.s.q.GetIssueByID(rq.ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return repos.Repo{}, 0, "", issues.ErrNotFound
	}
	if err != nil {
		return repos.Repo{}, 0, "", err
	}
	r, err := rq.repoByID(row.RepoID)
	if err != nil {
		return repos.Repo{}, 0, "", err
	}
	return r, row.Number, row.Kind, nil
}

// subjectByID resolves the global id of an issue or pull request; kinds
// limits the accepted node types.
func (rq *request) subjectByID(id string, kinds ...string) (repos.Repo, int64, string, error) {
	if len(kinds) == 0 {
		kinds = []string{"Issue", "PullRequest"}
	}
	_, u, err := decodeUUID(id, kinds...)
	if err != nil {
		return repos.Repo{}, 0, "", err
	}
	r, n, kind, err := rq.issueRow(u)
	if isNotFound(rq, err) {
		return repos.Repo{}, 0, "", errorf("NOT_FOUND", "Could not resolve to a node with the global id of '%s'", id)
	}
	return r, n, kind, err
}

// node resolves a global id; unknown and unreadable ids are NOT_FOUND.
func (rq *request) node(id string) (any, error) {
	notFound := errorf("NOT_FOUND", "Could not resolve to a node with the global id of '%s'", id)
	kind, key, ok := decodeID(id)
	if !ok {
		return nil, notFound
	}
	u, err := uuid.Parse(key)
	if err != nil {
		return nil, notFound
	}
	var v any
	switch kind {
	case "Repository":
		var r repos.Repo
		r, err = rq.repoByID(u)
		v = obj{"Repository", r}
	case "Issue", "PullRequest":
		var r repos.Repo
		var n int64
		var k string
		r, n, k, err = rq.issueRow(u)
		if err == nil {
			if (k == issues.KindPull) != (kind == "PullRequest") {
				return nil, notFound
			}
			v, err = rq.issueByNumber(r, n, true)
		}
	case "IssueComment":
		v, err = rq.commentNode(u)
	case "User":
		var p *userV
		p, err = rq.userNode(u)
		if p == nil && err == nil {
			return nil, notFound
		}
		v = obj{"User", p}
	default:
		return nil, notFound
	}
	if err != nil {
		var ge *Error
		if isNotFound(rq, err) || (errors.As(err, &ge) && ge.Type == "NOT_FOUND") {
			return nil, notFound
		}
		return nil, err
	}
	return v, nil
}

func (rq *request) userNode(id uuid.UUID) (*userV, error) {
	u, err := rq.identityUser(id)
	if err != nil || u == nil {
		return nil, err
	}
	return &userV{name: u.Username, id: u.ID}, nil
}

func (rq *request) commentNode(id uuid.UUID) (any, error) {
	rows, err := rq.s.q.GetIssueCommentsByIDs(rq.ctx, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, issues.ErrNotFound
	}
	r, _, kind, err := rq.issueRow(rows[0].IssueID)
	if err != nil {
		return nil, err
	}
	c, err := rq.s.d.Issues.GetComment(rq.ctx, rq.who, r.Owner, r.Name, id)
	if err != nil {
		return nil, err
	}
	return obj{"IssueComment", &commentV{r: r, kind: kind, c: c}}, nil
}

// ---- search ----

// searchV is a search result connection.
type searchV struct {
	c    *conn
	kind string // ISSUE, REPOSITORY, USER or DISCUSSION
}

func searchConnFields() map[string]resolver {
	base := connFields()
	m := map[string]resolver{}
	for k, r := range base {
		m[k] = func(rq *request, src any, a args) (any, error) { return r(rq, src.(*searchV).c, a) }
	}
	count := func(kind string) resolver {
		return func(rq *request, src any, a args) (any, error) {
			s := src.(*searchV)
			if s.kind != kind {
				return 0, nil
			}
			return s.c.count()
		}
	}
	m["issueCount"] = count("ISSUE")
	m["repositoryCount"] = count("REPOSITORY")
	m["userCount"] = count("USER")
	m["discussionCount"] = count("DISCUSSION")
	m["wikiCount"] = constant(0)
	m["codeCount"] = constant(0)
	return m
}

// searchTerm is a qualifier (key non-empty) or a word of a search query.
type searchTerm struct {
	key, val string
	neg      bool
}

// splitQuery splits a search query into terms, honouring double quotes.
func splitQuery(q string) []searchTerm {
	var words []string
	var b strings.Builder
	quoted := false
	for _, r := range q {
		switch {
		case r == '"':
			quoted = !quoted
		case (r == ' ' || r == '\t' || r == '\n') && !quoted:
			if b.Len() > 0 {
				words = append(words, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		words = append(words, b.String())
	}
	out := make([]searchTerm, 0, len(words))
	for _, w := range words {
		t := searchTerm{}
		if strings.HasPrefix(w, "-") && strings.Contains(w, ":") {
			t.neg, w = true, w[1:]
		}
		if k, v, ok := strings.Cut(w, ":"); ok && k != "" && !strings.Contains(k, "/") {
			t.key, t.val = strings.ToLower(k), v
		} else {
			t.val = w
		}
		out = append(out, t)
	}
	return out
}

func (rq *request) search(a args) (any, error) {
	if err := rq.read(); err != nil {
		return nil, err
	}
	kind := strings.ToUpper(a.str("type"))
	terms := splitQuery(a.str("query"))
	var fetch fetchFn
	var err error
	switch kind {
	case "ISSUE", "ISSUE_ADVANCED":
		kind = "ISSUE"
		fetch, err = rq.searchIssues(terms)
	case "REPOSITORY":
		fetch, err = rq.searchRepos(terms)
	default:
		fetch = func(int, int) ([]any, bool, error) { return nil, false, nil }
	}
	if err != nil {
		return nil, err
	}
	return obj{"SearchResultItemConnection", &searchV{c: newConn("SearchResultItem", a, fetch), kind: kind}}, nil
}

// chain pages over the items of a, then of b.
func chain(a, b fetchFn) fetchFn {
	na := -1
	return func(off, lim int) ([]any, bool, error) {
		if na < 0 {
			n, err := (&conn{fetch: a}).count()
			if err != nil {
				return nil, false, err
			}
			na = n
		}
		var out []any
		if off < na {
			items, _, err := a(off, lim)
			if err != nil {
				return nil, false, err
			}
			out = items
		}
		rest := lim - len(out)
		if rest <= 0 {
			return out, true, nil
		}
		items, more, err := b(max(off-na, 0), rest)
		if err != nil {
			return nil, false, err
		}
		return append(out, items...), more, nil
	}
}

// issueSearch is a parsed ISSUE search.
type issueSearch struct {
	repo                         string
	wantIssues, wantPulls        bool
	state                        string // open, closed, merged, unmerged or ""
	author, assignee, mentions   string
	labels                       []string
	milestone                    string
	base, head, reviewRequested  string
	draft                        *bool
	sort                         string
	asc                          bool
	text                         []string
	noAssignee, noLabel, noMiles bool
}

func (rq *request) parseIssueSearch(terms []searchTerm) issueSearch {
	s := issueSearch{wantIssues: true, wantPulls: true, sort: "created"}
	kindSet := false
	setKind := func(issue bool) {
		if !kindSet {
			s.wantIssues, s.wantPulls, kindSet = false, false, true
		}
		if issue {
			s.wantIssues = true
		} else {
			s.wantPulls = true
		}
	}
	for _, t := range terms {
		v := strings.ToLower(t.val)
		switch t.key {
		case "":
			s.text = append(s.text, t.val)
		case "repo":
			s.repo = t.val
		case "is", "type":
			switch v {
			case "issue":
				setKind(true)
			case "pr", "pull-request", "pullrequest":
				setKind(false)
			case "open", "closed", "merged", "unmerged":
				s.state = v
			case "draft":
				b := !t.neg
				s.draft = &b
			}
		case "state":
			s.state = v
		case "author":
			s.author = rq.me(t.val)
		case "assignee":
			s.assignee = rq.me(t.val)
		case "mentions":
			s.mentions = rq.me(t.val)
		case "label":
			s.labels = append(s.labels, t.val)
		case "milestone":
			s.milestone = t.val
		case "base":
			s.base = t.val
		case "head":
			s.head = t.val
		case "draft":
			b := v == "true"
			s.draft = &b
		case "review-requested":
			s.reviewRequested = rq.me(t.val)
		case "no":
			switch v {
			case "assignee":
				s.noAssignee = true
			case "label":
				s.noLabel = true
			case "milestone":
				s.noMiles = true
			}
		case "sort":
			field, dir, _ := strings.Cut(v, "-")
			switch field {
			case "updated":
				s.sort = "updated"
			case "comments":
				s.sort = "comments"
			default:
				s.sort = "created"
			}
			s.asc = dir == "asc"
		}
	}
	return s
}

func (rq *request) searchIssues(terms []searchTerm) (fetchFn, error) {
	s := rq.parseIssueSearch(terms)
	empty := func(int, int) ([]any, bool, error) { return nil, false, nil }
	if s.repo == "" {
		return empty, nil
	}
	r, err := rq.repoFull(s.repo)
	if isNotFound(rq, err) {
		return empty, nil
	}
	if err != nil {
		return nil, err
	}
	var fetchers []fetchFn
	if s.wantIssues && s.state != "merged" && s.state != "unmerged" && s.base == "" && s.head == "" &&
		s.reviewRequested == "" && s.draft == nil {
		f, ok, err := rq.issueSearchFetch(r, s)
		if err != nil {
			return nil, err
		}
		if ok {
			fetchers = append(fetchers, f)
		}
	}
	if s.wantPulls {
		fetchers = append(fetchers, rq.pullSearchFetch(r, s))
	}
	switch len(fetchers) {
	case 0:
		return empty, nil
	case 1:
		return fetchers[0], nil
	}
	return chain(fetchers[0], fetchers[1]), nil
}

func (rq *request) issueSearchFetch(r repos.Repo, s issueSearch) (fetchFn, bool, error) {
	in := issues.ListInput{State: "all", Labels: s.labels, Author: s.author, Assignee: s.assignee,
		Mentioned: s.mentions, Query: strings.Join(s.text, " "), Sort: s.sort, Ascending: s.asc}
	switch s.state {
	case "open", "closed":
		in.State = s.state
	}
	if s.noAssignee {
		in.Assignee = "none"
	}
	m := s.milestone
	if s.noMiles {
		m = "none"
	}
	ms, ok, err := rq.milestoneFilter(r, m)
	if err != nil || !ok {
		return nil, false, err
	}
	in.Milestone = ms
	return func(off, lim int) ([]any, bool, error) {
		q := in
		q.Offset, q.Limit = off, lim
		list, err := rq.s.d.Issues.List(rq.ctx, rq.who, r.Owner, r.Name, q)
		if err != nil {
			return nil, false, err
		}
		items := make([]any, 0, len(list))
		for _, x := range list {
			if s.noLabel && len(x.Labels) > 0 {
				continue
			}
			items = append(items, issueObj(r, x))
		}
		return items, len(list) == lim, nil
	}, true, nil
}

func (rq *request) pullSearchFetch(r repos.Repo, s issueSearch) fetchFn {
	o := pulls.ListOptions{State: "all", Author: s.author, Base: s.base}
	switch s.state {
	case "open":
		o.State = "open"
	case "closed":
		o.State = "closed"
	case "merged":
		o.State = "merged"
	}
	if s.sort == "updated" {
		o.Sort = "updated"
	}
	head := s.head
	if owner, branch, ok := strings.Cut(head, ":"); ok {
		o.HeadRepo, head = owner+"/"+r.Name, branch
	}
	o.Head = head
	keep := func(it any) bool {
		p := it.(obj).v.(*pullV).p
		i := p.Issue
		switch {
		case s.state == "unmerged" && p.State != "closed":
			return false
		case s.draft != nil && p.Draft != *s.draft:
			return false
		case s.assignee != "" && !containsFold(i.Assignees, s.assignee):
			return false
		case s.noAssignee && len(i.Assignees) > 0:
			return false
		case s.noLabel && len(i.Labels) > 0:
			return false
		case s.noMiles && i.Milestone != nil:
			return false
		case s.milestone != "" && (i.Milestone == nil || !strings.EqualFold(i.Milestone.Title, s.milestone)):
			return false
		case s.reviewRequested != "" && !containsFold(p.RequestedReviewers, s.reviewRequested):
			return false
		case s.mentions != "" && !strings.Contains(strings.ToLower(i.Body), "@"+strings.ToLower(s.mentions)):
			return false
		}
		for _, l := range s.labels {
			found := false
			for _, x := range i.Labels {
				if strings.EqualFold(x.Name, l) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		for _, w := range s.text {
			w = strings.ToLower(w)
			if !strings.Contains(strings.ToLower(i.Title), w) && !strings.Contains(strings.ToLower(i.Body), w) {
				return false
			}
		}
		return true
	}
	return filtered(rq.pullFetch(r, o), keep)
}

func (rq *request) searchRepos(terms []searchTerm) (fetchFn, error) {
	var owner, privacy string
	var text []string
	empty := false
	for _, t := range terms {
		v := strings.ToLower(t.val)
		switch t.key {
		case "":
			text = append(text, strings.ToLower(t.val))
		case "user", "org", "owner":
			owner = t.val
		case "repo":
			o, n, _ := strings.Cut(t.val, "/")
			owner, text = o, append(text, strings.ToLower(n))
		case "is":
			switch v {
			case "public", "private", "internal":
				privacy = v
			case "fork", "archived", "template", "mirror":
				empty = !t.neg
			}
		case "fork":
			empty = empty || v == "only"
		case "archived":
			empty = empty || v == "true"
		case "language", "topic", "topics", "stars", "forks", "license":
			empty = true
		}
	}
	if empty {
		return func(int, int) ([]any, bool, error) { return nil, false, nil }, nil
	}
	var src fetchFn
	switch {
	case owner != "":
		src = func(off, lim int) ([]any, bool, error) {
			list, err := rq.s.d.Repos.ListOwner(rq.ctx, rq.who, owner, off, lim)
			if err != nil {
				return nil, false, err
			}
			return repoItems(rq, list), len(list) == lim, nil
		}
	case len(text) > 0:
		q := text[0]
		text = text[1:]
		src = func(off, lim int) ([]any, bool, error) {
			list, err := rq.s.d.Repos.Search(rq.ctx, rq.who, q, off, lim)
			if err != nil {
				return nil, false, err
			}
			return repoItems(rq, list), len(list) == lim, nil
		}
	default:
		src = func(off, lim int) ([]any, bool, error) {
			list, err := rq.s.d.Repos.ListMine(rq.ctx, rq.who, off, lim)
			if err != nil {
				return nil, false, err
			}
			return repoItems(rq, list), len(list) == lim, nil
		}
	}
	return filtered(src, func(it any) bool {
		r := it.(obj).v.(repos.Repo)
		if privacy != "" && !strings.EqualFold(string(r.Visibility), privacy) {
			return false
		}
		for _, w := range text {
			if !strings.Contains(strings.ToLower(r.Name), w) && !strings.Contains(strings.ToLower(r.Description), w) {
				return false
			}
		}
		return true
	}), nil
}
