package graphql

import (
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/pulls"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// payload is the value of a mutation payload: its fields by name.
type payload map[string]any

// payloadTypes are the payload types and their fields.
var payloadTypes = map[string][]string{
	"CreateIssuePayload":                   {"issue"},
	"UpdateIssuePayload":                   {"issue"},
	"CloseIssuePayload":                    {"issue"},
	"ReopenIssuePayload":                   {"issue"},
	"AddCommentPayload":                    {"commentEdge", "subject", "timelineEdge"},
	"UpdateIssueCommentPayload":            {"issueComment"},
	"DeleteIssueCommentPayload":            {},
	"CreatePullRequestPayload":             {"pullRequest"},
	"UpdatePullRequestPayload":             {"pullRequest"},
	"ClosePullRequestPayload":              {"pullRequest"},
	"ReopenPullRequestPayload":             {"pullRequest"},
	"MergePullRequestPayload":              {"pullRequest"},
	"MarkPullRequestReadyForReviewPayload": {"pullRequest"},
	"ConvertPullRequestToDraftPayload":     {"pullRequest"},
	"RequestReviewsPayload":                {"pullRequest"},
	"AddPullRequestReviewPayload":          {"pullRequestReview"},
	"EnablePullRequestAutoMergePayload":    {"pullRequest"},
	"DisablePullRequestAutoMergePayload":   {"pullRequest"},
	"AddLabelsToLabelablePayload":          {"labelable"},
	"RemoveLabelsFromLabelablePayload":     {"labelable"},
	"AddAssigneesToAssignablePayload":      {"assignable"},
	"RemoveAssigneesFromAssignablePayload": {"assignable"},
	"UpdatePullRequestBranchPayload":       {"pullRequest"},
	"UpdatePullRequestBaseRefPayload":      {"pullRequest"},
}

// inputTypes are the input types of the mutations and their fields, for
// introspection.
var inputTypes = map[string][]string{
	"CreateIssueInput":                   {"repositoryId", "title", "body", "assigneeIds", "labelIds", "milestoneId", "projectIds", "issueTemplate", "clientMutationId"},
	"UpdateIssueInput":                   {"id", "title", "body", "assigneeIds", "labelIds", "milestoneId", "projectIds", "state", "clientMutationId"},
	"CloseIssueInput":                    {"issueId", "stateReason", "duplicateIssueId", "clientMutationId"},
	"ReopenIssueInput":                   {"issueId", "clientMutationId"},
	"AddCommentInput":                    {"subjectId", "body", "clientMutationId"},
	"UpdateIssueCommentInput":            {"id", "body", "clientMutationId"},
	"DeleteIssueCommentInput":            {"id", "clientMutationId"},
	"CreatePullRequestInput":             {"repositoryId", "baseRefName", "headRefName", "headRepositoryId", "title", "body", "draft", "maintainerCanModify", "clientMutationId"},
	"UpdatePullRequestInput":             {"pullRequestId", "title", "body", "baseRefName", "state", "assigneeIds", "labelIds", "milestoneId", "maintainerCanModify", "clientMutationId"},
	"ClosePullRequestInput":              {"pullRequestId", "clientMutationId"},
	"ReopenPullRequestInput":             {"pullRequestId", "clientMutationId"},
	"MergePullRequestInput":              {"pullRequestId", "mergeMethod", "commitHeadline", "commitBody", "expectedHeadOid", "authorEmail", "clientMutationId"},
	"MarkPullRequestReadyForReviewInput": {"pullRequestId", "clientMutationId"},
	"ConvertPullRequestToDraftInput":     {"pullRequestId", "clientMutationId"},
	"RequestReviewsInput":                {"pullRequestId", "userIds", "teamIds", "union", "clientMutationId"},
	"AddPullRequestReviewInput":          {"pullRequestId", "event", "body", "commitOID", "clientMutationId"},
	"EnablePullRequestAutoMergeInput":    {"pullRequestId", "mergeMethod", "commitHeadline", "commitBody", "authorEmail", "clientMutationId"},
	"DisablePullRequestAutoMergeInput":   {"pullRequestId", "clientMutationId"},
	"AddLabelsToLabelableInput":          {"labelableId", "labelIds", "clientMutationId"},
	"RemoveLabelsFromLabelableInput":     {"labelableId", "labelIds", "clientMutationId"},
	"AddAssigneesToAssignableInput":      {"assignableId", "assigneeIds", "clientMutationId"},
	"RemoveAssigneesFromAssignableInput": {"assignableId", "assigneeIds", "clientMutationId"},
}

// mutation wraps a mutation resolver: it checks the write scope, reads the
// input and fills clientMutationId.
func mutation(typ string, fn func(rq *request, in args) (payload, error)) resolver {
	return func(rq *request, src any, a args) (any, error) {
		if err := rq.write(); err != nil {
			return nil, err
		}
		in, err := a.input()
		if err != nil {
			return nil, err
		}
		p, err := fn(rq, in)
		if err != nil {
			return nil, err
		}
		if p == nil {
			p = payload{}
		}
		p["clientMutationId"] = in["clientMutationId"]
		return obj{typ, p}, nil
	}
}

func mutationFields() map[string]resolver {
	return map[string]resolver{
		"createIssue":                   mutation("CreateIssuePayload", createIssue),
		"updateIssue":                   mutation("UpdateIssuePayload", updateIssue),
		"closeIssue":                    mutation("CloseIssuePayload", closeIssue),
		"reopenIssue":                   mutation("ReopenIssuePayload", reopenIssue),
		"addComment":                    mutation("AddCommentPayload", addComment),
		"updateIssueComment":            mutation("UpdateIssueCommentPayload", updateIssueComment),
		"deleteIssueComment":            mutation("DeleteIssueCommentPayload", deleteIssueComment),
		"createPullRequest":             mutation("CreatePullRequestPayload", createPullRequest),
		"updatePullRequest":             mutation("UpdatePullRequestPayload", updatePullRequest),
		"closePullRequest":              mutation("ClosePullRequestPayload", pullState("closed")),
		"reopenPullRequest":             mutation("ReopenPullRequestPayload", pullState("open")),
		"mergePullRequest":              mutation("MergePullRequestPayload", mergePullRequest),
		"markPullRequestReadyForReview": mutation("MarkPullRequestReadyForReviewPayload", pullDraft(false)),
		"convertPullRequestToDraft":     mutation("ConvertPullRequestToDraftPayload", pullDraft(true)),
		"requestReviews":                mutation("RequestReviewsPayload", requestReviews),
		"addPullRequestReview":          mutation("AddPullRequestReviewPayload", addPullRequestReview),
		"enablePullRequestAutoMerge":    mutation("EnablePullRequestAutoMergePayload", enableAutoMerge),
		"disablePullRequestAutoMerge":   mutation("DisablePullRequestAutoMergePayload", disableAutoMerge),
		"addLabelsToLabelable":          mutation("AddLabelsToLabelablePayload", editSet("labelableId", "labelIds", "labelable", true, true)),
		"removeLabelsFromLabelable":     mutation("RemoveLabelsFromLabelablePayload", editSet("labelableId", "labelIds", "labelable", true, false)),
		"addAssigneesToAssignable":      mutation("AddAssigneesToAssignablePayload", editSet("assignableId", "assigneeIds", "assignable", false, true)),
		"removeAssigneesFromAssignable": mutation("RemoveAssigneesFromAssignablePayload", editSet("assignableId", "assigneeIds", "assignable", false, false)),
	}
}

// ---- helpers ----

// labelNames maps label global ids of r to names.
func (rq *request) labelNames(r repos.Repo, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []string
	for off := 0; off < maxScan && len(out) < len(want); off += maxPage {
		list, err := rq.s.d.Issues.ListLabels(rq.ctx, rq.who, r.Owner, r.Name, off, maxPage)
		if err != nil {
			return nil, err
		}
		for _, l := range list {
			if want[nodeID("Label", l.ID)] {
				out = append(out, l.Name)
			}
		}
		if len(list) < maxPage {
			break
		}
	}
	if len(out) < len(want) {
		for _, id := range ids {
			if _, _, err := decodeUUID(id, "Label"); err != nil {
				return nil, err
			}
		}
		return nil, errorf("NOT_FOUND", "Could not resolve to a Label in %s/%s", r.Owner, r.Name)
	}
	return out, nil
}

// userNames maps user global ids to usernames.
func (rq *request) userNames(ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		_, u, err := decodeUUID(id, "User")
		if err != nil {
			return nil, err
		}
		p, err := rq.identityUser(u)
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, errorf("NOT_FOUND", "Could not resolve to a node with the global id of '%s'", id)
		}
		out = append(out, p.Username)
	}
	return out, nil
}

// milestoneArg maps a milestoneId input to UpdateInput.MilestoneID: nil
// when absent, "" when null.
func milestoneArg(in args, key string) (*string, error) {
	v, present := in[key]
	if !present {
		return nil, nil
	}
	empty := ""
	if v == nil {
		return &empty, nil
	}
	s := in.str(key)
	if s == "" {
		return &empty, nil
	}
	_, u, err := decodeUUID(s, "Milestone")
	if err != nil {
		return nil, err
	}
	id := u.String()
	return &id, nil
}

// subject returns the issue or pull request object of number in r.
func (rq *request) subjectObj(r repos.Repo, number int64) (any, error) {
	return rq.issueByNumber(r, number, true)
}

// metadata applies labelIds, assigneeIds and milestoneId of in.
func (rq *request) metadata(r repos.Repo, in args, up *issues.UpdateInput) error {
	if in.has("labelIds") {
		names, err := rq.labelNames(r, in.list("labelIds"))
		if err != nil {
			return err
		}
		up.Labels = &names
	}
	if in.has("assigneeIds") {
		names, err := rq.userNames(in.list("assigneeIds"))
		if err != nil {
			return err
		}
		up.Assignees = &names
	}
	m, err := milestoneArg(in, "milestoneId")
	if err != nil {
		return err
	}
	up.MilestoneID = m
	return nil
}

func emptyUpdate(u issues.UpdateInput) bool {
	return u.Title == nil && u.Body == nil && u.State == nil && u.StateReason == nil &&
		u.Labels == nil && u.Assignees == nil && u.MilestoneID == nil
}

// ---- issues ----

func createIssue(rq *request, in args) (payload, error) {
	rid, err := in.required("repositoryId")
	if err != nil {
		return nil, err
	}
	_, ru, err := decodeUUID(rid, "Repository")
	if err != nil {
		return nil, err
	}
	r, err := rq.repoByID(ru)
	if err != nil {
		return nil, err
	}
	title, err := in.required("title")
	if err != nil {
		return nil, err
	}
	ci := issues.CreateInput{Title: title, Body: in.str("body")}
	if ci.Labels, err = rq.labelNames(r, in.list("labelIds")); err != nil {
		return nil, err
	}
	if ci.Assignees, err = rq.userNames(in.list("assigneeIds")); err != nil {
		return nil, err
	}
	if s := in.str("milestoneId"); s != "" {
		_, u, err := decodeUUID(s, "Milestone")
		if err != nil {
			return nil, err
		}
		ci.MilestoneID = &u
	}
	i, err := rq.s.d.Issues.Create(rq.ctx, rq.who, r.Owner, r.Name, ci)
	if err != nil {
		return nil, err
	}
	return payload{"issue": issueObj(r, i)}, nil
}

func updateIssue(rq *request, in args) (payload, error) {
	id, err := in.required("id")
	if err != nil {
		return nil, err
	}
	r, n, _, err := rq.subjectByID(id, "Issue")
	if err != nil {
		return nil, err
	}
	up := issues.UpdateInput{Title: in.strp("title"), Body: in.strp("body")}
	if st := strings.ToLower(in.str("state")); st != "" {
		up.State = &st
	}
	if err := rq.metadata(r, in, &up); err != nil {
		return nil, err
	}
	if !emptyUpdate(up) {
		if _, err := rq.s.d.Issues.Update(rq.ctx, rq.who, r.Owner, r.Name, n, up); err != nil {
			return nil, err
		}
	}
	i, err := rq.issueByNumber(r, n, false)
	if err != nil {
		return nil, err
	}
	return payload{"issue": i}, nil
}

func setIssueState(rq *request, in args, state, reason string) (payload, error) {
	id, err := in.required("issueId")
	if err != nil {
		return nil, err
	}
	r, n, _, err := rq.subjectByID(id, "Issue")
	if err != nil {
		return nil, err
	}
	up := issues.UpdateInput{State: &state}
	if reason != "" {
		up.StateReason = &reason
	}
	i, err := rq.s.d.Issues.Update(rq.ctx, rq.who, r.Owner, r.Name, n, up)
	if err != nil {
		return nil, err
	}
	return payload{"issue": issueObj(r, i)}, nil
}

func closeIssue(rq *request, in args) (payload, error) {
	reason := issues.ReasonCompleted
	switch strings.ToUpper(in.str("stateReason")) {
	case "NOT_PLANNED":
		reason = issues.ReasonNotPlanned
	case "DUPLICATE":
		reason = issues.ReasonDuplicate
	}
	return setIssueState(rq, in, "closed", reason)
}

func reopenIssue(rq *request, in args) (payload, error) {
	return setIssueState(rq, in, "open", "")
}

func addComment(rq *request, in args) (payload, error) {
	id, err := in.required("subjectId")
	if err != nil {
		return nil, err
	}
	body, err := in.required("body")
	if err != nil {
		return nil, err
	}
	r, n, kind, err := rq.subjectByID(id)
	if err != nil {
		return nil, err
	}
	c, err := rq.s.d.Issues.CreateComment(rq.ctx, rq.who, r.Owner, r.Name, n, body)
	if err != nil {
		return nil, err
	}
	node := obj{"IssueComment", &commentV{r: r, kind: kind, c: c}}
	subj, err := rq.subjectObj(r, n)
	if err != nil {
		return nil, err
	}
	return payload{
		"commentEdge": obj{"IssueCommentEdge", edge{node: node, cursor: cursor(0)}},
		"subject":     subj,
	}, nil
}

// commentByID resolves the global id of an issue comment.
func (rq *request) commentByID(id string) (repos.Repo, *commentV, error) {
	_, u, err := decodeUUID(id, "IssueComment")
	if err != nil {
		return repos.Repo{}, nil, err
	}
	v, err := rq.commentNode(u)
	if isNotFound(rq, err) {
		return repos.Repo{}, nil, errorf("NOT_FOUND", "Could not resolve to a node with the global id of '%s'", id)
	}
	if err != nil {
		return repos.Repo{}, nil, err
	}
	c := v.(obj).v.(*commentV)
	return c.r, c, nil
}

func updateIssueComment(rq *request, in args) (payload, error) {
	id, err := in.required("id")
	if err != nil {
		return nil, err
	}
	body, err := in.required("body")
	if err != nil {
		return nil, err
	}
	r, c, err := rq.commentByID(id)
	if err != nil {
		return nil, err
	}
	nc, err := rq.s.d.Issues.UpdateComment(rq.ctx, rq.who, r.Owner, r.Name, c.c.ID, body)
	if err != nil {
		return nil, err
	}
	return payload{"issueComment": obj{"IssueComment", &commentV{r: r, kind: c.kind, c: nc}}}, nil
}

func deleteIssueComment(rq *request, in args) (payload, error) {
	id, err := in.required("id")
	if err != nil {
		return nil, err
	}
	r, c, err := rq.commentByID(id)
	if err != nil {
		return nil, err
	}
	if err := rq.s.d.Issues.DeleteComment(rq.ctx, rq.who, r.Owner, r.Name, c.c.ID, rq.client()); err != nil {
		return nil, err
	}
	return payload{}, nil
}

// editSet adds or removes labels (labels true) or assignees of a labelable
// or assignable.
func editSet(idKey, listKey, out string, labels, add bool) func(rq *request, in args) (payload, error) {
	return func(rq *request, in args) (payload, error) {
		id, err := in.required(idKey)
		if err != nil {
			return nil, err
		}
		r, n, _, err := rq.subjectByID(id)
		if err != nil {
			return nil, err
		}
		var names []string
		if labels {
			names, err = rq.labelNames(r, in.list(listKey))
		} else {
			names, err = rq.userNames(in.list(listKey))
		}
		if err != nil {
			return nil, err
		}
		cur, err := rq.s.d.Issues.Get(rq.ctx, rq.who, r.Owner, r.Name, n)
		if err != nil {
			return nil, err
		}
		var set []string
		if labels {
			for _, l := range cur.Labels {
				set = append(set, l.Name)
			}
		} else {
			set = append(set, cur.Assignees...)
		}
		next := make([]string, 0, len(set)+len(names))
		for _, s := range set {
			if add || !containsFold(names, s) {
				next = append(next, s)
			}
		}
		if add {
			for _, s := range names {
				if !containsFold(next, s) {
					next = append(next, s)
				}
			}
		}
		up := issues.UpdateInput{}
		if labels {
			up.Labels = &next
		} else {
			up.Assignees = &next
		}
		if _, err := rq.s.d.Issues.Update(rq.ctx, rq.who, r.Owner, r.Name, n, up); err != nil {
			return nil, err
		}
		subj, err := rq.subjectObj(r, n)
		if err != nil {
			return nil, err
		}
		return payload{out: subj}, nil
	}
}

// ---- pull requests ----

// pullByID resolves the global id of a pull request.
func (rq *request) pullByID(in args) (repos.Repo, int64, error) {
	id, err := in.required("pullRequestId")
	if err != nil {
		return repos.Repo{}, 0, err
	}
	r, n, _, err := rq.subjectByID(id, "PullRequest")
	return r, n, err
}

func (rq *request) pullPayload(r repos.Repo, n int64) (payload, error) {
	p, err := rq.pullByNumber(r, n)
	if err != nil {
		return nil, err
	}
	return payload{"pullRequest": p}, nil
}

func createPullRequest(rq *request, in args) (payload, error) {
	rid, err := in.required("repositoryId")
	if err != nil {
		return nil, err
	}
	_, ru, err := decodeUUID(rid, "Repository")
	if err != nil {
		return nil, err
	}
	r, err := rq.repoByID(ru)
	if err != nil {
		return nil, err
	}
	title, err := in.required("title")
	if err != nil {
		return nil, err
	}
	base, err := in.required("baseRefName")
	if err != nil {
		return nil, err
	}
	head, err := in.required("headRefName")
	if err != nil {
		return nil, err
	}
	ci := pulls.CreateInput{Title: title, Body: in.str("body"), Base: base, Head: head}
	if d, ok := in.boolean("draft"); ok {
		ci.Draft = d
	}
	if hid := in.str("headRepositoryId"); hid != "" {
		_, hu, err := decodeUUID(hid, "Repository")
		if err != nil {
			return nil, err
		}
		hr, err := rq.repoByID(hu)
		if err != nil {
			return nil, err
		}
		ci.HeadRepo = hr.Owner + "/" + hr.Name
	} else if owner, branch, ok := strings.Cut(head, ":"); ok {
		ci.Head = branch
		if !strings.EqualFold(owner, r.Owner) {
			ci.HeadRepo = owner + "/" + r.Name
		}
	}
	p, err := rq.s.d.Pulls.Create(rq.ctx, rq.who, r.Owner, r.Name, ci)
	if err != nil {
		return nil, err
	}
	return payload{"pullRequest": pullObj(r, p)}, nil
}

func updatePullRequest(rq *request, in args) (payload, error) {
	r, n, err := rq.pullByID(in)
	if err != nil {
		return nil, err
	}
	pu := pulls.UpdateInput{Title: in.strp("title"), Body: in.strp("body"), Base: in.strp("baseRefName")}
	if st := strings.ToLower(in.str("state")); st != "" {
		pu.State = &st
	}
	if pu.Title != nil || pu.Body != nil || pu.Base != nil || pu.State != nil {
		if _, err := rq.s.d.Pulls.Update(rq.ctx, rq.who, r.Owner, r.Name, n, pu); err != nil {
			return nil, err
		}
	}
	var up issues.UpdateInput
	if err := rq.metadata(r, in, &up); err != nil {
		return nil, err
	}
	if !emptyUpdate(up) {
		if _, err := rq.s.d.Issues.Update(rq.ctx, rq.who, r.Owner, r.Name, n, up); err != nil {
			return nil, err
		}
	}
	return rq.pullPayload(r, n)
}

func pullState(state string) func(rq *request, in args) (payload, error) {
	return func(rq *request, in args) (payload, error) {
		r, n, err := rq.pullByID(in)
		if err != nil {
			return nil, err
		}
		p, err := rq.s.d.Pulls.Update(rq.ctx, rq.who, r.Owner, r.Name, n, pulls.UpdateInput{State: &state})
		if err != nil {
			return nil, err
		}
		return payload{"pullRequest": pullObj(r, p)}, nil
	}
}

func pullDraft(draft bool) func(rq *request, in args) (payload, error) {
	return func(rq *request, in args) (payload, error) {
		r, n, err := rq.pullByID(in)
		if err != nil {
			return nil, err
		}
		p, err := rq.s.d.Pulls.Update(rq.ctx, rq.who, r.Owner, r.Name, n, pulls.UpdateInput{Draft: &draft})
		if err != nil {
			return nil, err
		}
		return payload{"pullRequest": pullObj(r, p)}, nil
	}
}

func mergeMethod(s string) string {
	switch strings.ToUpper(s) {
	case "SQUASH":
		return pulls.MethodSquash
	case "REBASE":
		return pulls.MethodRebase
	}
	return pulls.MethodMerge
}

func mergePullRequest(rq *request, in args) (payload, error) {
	r, n, err := rq.pullByID(in)
	if err != nil {
		return nil, err
	}
	mi := pulls.MergeInput{Method: mergeMethod(in.str("mergeMethod")), Title: in.str("commitHeadline"),
		Message: in.str("commitBody"), SHA: in.str("expectedHeadOid")}
	if _, err := rq.s.d.Pulls.Merge(rq.ctx, rq.who, r.Owner, r.Name, n, mi); err != nil {
		return nil, err
	}
	return rq.pullPayload(r, n)
}

func enableAutoMerge(rq *request, in args) (payload, error) {
	r, n, err := rq.pullByID(in)
	if err != nil {
		return nil, err
	}
	ai := pulls.AutoMergeInput{Method: mergeMethod(in.str("mergeMethod")), Title: in.str("commitHeadline"),
		Message: in.str("commitBody")}
	p, err := rq.s.d.Pulls.EnableAutoMerge(rq.ctx, rq.who, r.Owner, r.Name, n, ai)
	if err != nil {
		return nil, err
	}
	return payload{"pullRequest": pullObj(r, p)}, nil
}

func disableAutoMerge(rq *request, in args) (payload, error) {
	r, n, err := rq.pullByID(in)
	if err != nil {
		return nil, err
	}
	p, err := rq.s.d.Pulls.DisableAutoMerge(rq.ctx, rq.who, r.Owner, r.Name, n)
	if err != nil {
		return nil, err
	}
	return payload{"pullRequest": pullObj(r, p)}, nil
}

func requestReviews(rq *request, in args) (payload, error) {
	r, n, err := rq.pullByID(in)
	if err != nil {
		return nil, err
	}
	if len(in.list("teamIds")) > 0 {
		return nil, errorf("UNPROCESSABLE", "team review requests are not supported")
	}
	names, err := rq.userNames(in.list("userIds"))
	if err != nil {
		return nil, err
	}
	union, set := in.boolean("union")
	if set && !union {
		// Replace the requested reviewers: drop the ones not listed.
		cur, err := rq.s.d.Pulls.Get(rq.ctx, rq.who, r.Owner, r.Name, n)
		if err != nil {
			return nil, err
		}
		var drop []string
		for _, x := range cur.RequestedReviewers {
			if !containsFold(names, x) {
				drop = append(drop, x)
			}
		}
		if len(drop) > 0 {
			if _, err := rq.s.d.Pulls.RemoveReviewers(rq.ctx, rq.who, r.Owner, r.Name, n, drop); err != nil {
				return nil, err
			}
		}
	}
	if len(names) > 0 {
		if _, err := rq.s.d.Pulls.RequestReviewers(rq.ctx, rq.who, r.Owner, r.Name, n, names); err != nil {
			return nil, err
		}
	}
	return rq.pullPayload(r, n)
}

func addPullRequestReview(rq *request, in args) (payload, error) {
	r, n, err := rq.pullByID(in)
	if err != nil {
		return nil, err
	}
	ri := pulls.ReviewInput{Body: in.str("body"), CommitSHA: in.str("commitOID")}
	switch strings.ToUpper(in.str("event")) {
	case "APPROVE":
		ri.Event = pulls.EventApprove
	case "REQUEST_CHANGES":
		ri.Event = pulls.EventRequestChanges
	case "COMMENT":
		ri.Event = pulls.EventComment
	case "":
	default:
		return nil, errorf("UNPROCESSABLE", "unsupported review event %q", in.str("event"))
	}
	rv, err := rq.s.d.Pulls.CreateReview(rq.ctx, rq.who, r.Owner, r.Name, n, ri)
	if err != nil {
		return nil, err
	}
	p, err := rq.s.d.Pulls.Get(rq.ctx, rq.who, r.Owner, r.Name, n)
	if err != nil {
		return nil, err
	}
	return payload{"pullRequestReview": obj{"PullRequestReview", &reviewV{v: &pullV{r: r, p: p}, rv: rv}}}, nil
}
