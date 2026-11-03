package graphql

import (
	"sort"
)

// Interfaces and unions the object types belong to, for type conditions
// of fragments (`... on Node`, `... on PullRequest`).
var (
	nodeIs       = []string{"Node"}
	actorIs      = []string{"Node", "Actor", "RepositoryOwner", "UniformResourceLocatable", "ProfileOwner"}
	userIs       = append(append([]string{}, actorIs...), "Assignee", "RequestedReviewer", "Reactor", "ReactingUser", "SearchResultItem")
	orgIs        = append(append([]string{}, actorIs...), "SearchResultItem")
	issueLikeIs  = []string{"Node", "IssueOrPullRequest", "SearchResultItem", "Comment", "Closable", "Labelable", "Assignable", "Reactable", "Updatable", "UpdatableComment", "UniformResourceLocatable", "Subscribable", "Lockable", "Deletable", "ProjectV2Owner"}
	repositoryIs = []string{"Node", "RepositoryInfo", "UniformResourceLocatable", "Starrable", "PinnableItem", "SearchResultItem", "Subscribable", "ProjectV2Recent"}
	commentIs    = []string{"Node", "Comment", "Reactable", "Updatable", "UpdatableComment", "Deletable", "Minimizable", "IssueTimelineItems", "PullRequestTimelineItems"}
	contextIs    = []string{"Node", "StatusCheckRollupContext"}
)

// enums are the enum types advertised by introspection.
var enums = map[string][]string{
	"IssueState":                {"OPEN", "CLOSED"},
	"IssueStateReason":          {"COMPLETED", "NOT_PLANNED", "REOPENED", "DUPLICATE"},
	"IssueClosedStateReason":    {"COMPLETED", "NOT_PLANNED", "DUPLICATE"},
	"PullRequestState":          {"OPEN", "CLOSED", "MERGED"},
	"PullRequestUpdateState":    {"OPEN", "CLOSED"},
	"PullRequestMergeMethod":    {"MERGE", "SQUASH", "REBASE"},
	"MergeableState":            {"MERGEABLE", "CONFLICTING", "UNKNOWN"},
	"PullRequestReviewEvent":    {"APPROVE", "REQUEST_CHANGES", "COMMENT", "DISMISS"},
	"PullRequestReviewState":    {"PENDING", "COMMENTED", "APPROVED", "CHANGES_REQUESTED", "DISMISSED"},
	"PullRequestReviewDecision": {"APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED"},
	"StatusState":               {"EXPECTED", "ERROR", "FAILURE", "PENDING", "SUCCESS"},
	"CheckStatusState":          {"QUEUED", "IN_PROGRESS", "COMPLETED", "WAITING", "PENDING", "REQUESTED"},
	"CheckConclusionState":      {"ACTION_REQUIRED", "TIMED_OUT", "CANCELLED", "FAILURE", "SUCCESS", "NEUTRAL", "SKIPPED", "STARTUP_FAILURE", "STALE"},
	"RepositoryPermission":      {"ADMIN", "MAINTAIN", "WRITE", "TRIAGE", "READ"},
	"RepositoryVisibility":      {"PRIVATE", "PUBLIC", "INTERNAL"},
	"RepositoryPrivacy":         {"PUBLIC", "PRIVATE"},
	"CommentAuthorAssociation":  {"MEMBER", "OWNER", "MANNEQUIN", "COLLABORATOR", "CONTRIBUTOR", "FIRST_TIME_CONTRIBUTOR", "FIRST_TIMER", "NONE"},
	"SearchType":                {"ISSUE", "ISSUE_ADVANCED", "REPOSITORY", "USER", "DISCUSSION"},
	"IssueOrderField":           {"CREATED_AT", "UPDATED_AT", "COMMENTS"},
	"OrderDirection":            {"ASC", "DESC"},
	"ReactionContent":           {"THUMBS_UP", "THUMBS_DOWN", "LAUGH", "HOORAY", "CONFUSED", "HEART", "ROCKET", "EYES"},
	"MilestoneState":            {"OPEN", "CLOSED"},
	"PatchStatus":               {"ADDED", "DELETED", "RENAMED", "COPIED", "MODIFIED", "CHANGED"},
}

// connectionNodes are the node types that have a Connection and an Edge
// type.
var connectionNodes = []string{
	"Repository", "Organization", "User", "Label", "Milestone", "Release", "ReleaseAsset",
	"Issue", "PullRequest", "IssueComment", "PullRequestReview", "ReviewRequest",
	"PullRequestCommit", "PullRequestChangedFile", "StatusCheckRollupContext", "ReactingUser",
	"Reactor", "Language", "RepositoryTopic", "Project", "ProjectV2", "ProjectCard",
	"ProjectV2Item", "UserContentEdit", "IssueTimelineItems", "PullRequestTimelineItems",
	"PullRequestReviewComment", "GitActor",
}

// buildSchema registers the types of the schema.
func buildSchema() *schema {
	s := &schema{types: map[string]*objType{}}
	add := func(name string, fields map[string]resolver, is ...string) {
		t := &objType{name: name, kind: "OBJECT", fields: fields, is: map[string]bool{}}
		for k := range fields {
			t.order = append(t.order, k)
		}
		sort.Strings(t.order)
		for _, i := range is {
			t.is[i] = true
		}
		s.types[name] = t
	}

	add("Query", queryFields())
	add("Mutation", mutationFields())

	add("User", userFields(), userIs...)
	add("Organization", orgFields(), orgIs...)
	add("Repository", repositoryFields(), repositoryIs...)
	add("Ref", refFields(), nodeIs...)
	add("Label", labelFields(), nodeIs...)
	add("Milestone", milestoneFields(), nodeIs...)
	add("Release", releaseFields(), nodeIs...)
	add("ReleaseAsset", assetFields(), nodeIs...)
	add("ReactionGroup", reactionGroupFields())

	add("Issue", issueOnlyFields(), issueLikeIs...)
	add("PullRequest", pullFields(), issueLikeIs...)
	add("IssueComment", commentFields(), commentIs...)
	add("PullRequestReview", reviewFields(), append([]string{"PullRequestTimelineItems"}, commentIs...)...)
	add("ReviewRequest", reviewRequestFields(), nodeIs...)
	add("AutoMergeRequest", autoMergeFields())
	add("PullRequestChangedFile", changedFileFields())
	add("Commit", commitFields(), "Node", "GitObject", "UniformResourceLocatable")
	add("PullRequestCommit", prCommitFields(), "Node", "UniformResourceLocatable")
	add("GitActor", gitActorFields())

	add("StatusCheckRollup", rollupFields(), nodeIs...)
	add("StatusContext", statusContextFields(), contextIs...)
	add("CheckRun", checkRunFields(), contextIs...)
	add("CheckSuite", checkSuiteFields(), nodeIs...)
	add("App", appFields(), nodeIs...)

	add("RateLimit", rateLimitFields())
	add("PageInfo", pageInfoFields())

	for _, n := range connectionNodes {
		add(n+"Connection", connFields())
		add(n+"Edge", edgeFields())
	}
	add("SearchResultItemConnection", searchConnFields())
	add("SearchResultItemEdge", edgeFields())

	for name, fields := range payloadTypes {
		add(name, payloadFields(fields))
	}

	add("__Schema", schemaFields())
	add("__Type", typeFields())
	add("__Field", fieldFields())
	add("__InputValue", inputValueFields())
	add("__EnumValue", enumValueFields())

	for name, fields := range inputTypes {
		f := append([]string{}, fields...)
		s.types[name] = &objType{name: name, kind: "INPUT_OBJECT", order: f, is: map[string]bool{}}
	}
	for name, vals := range enums {
		s.types[name] = &objType{name: name, kind: "ENUM", enum: vals, is: map[string]bool{}}
	}
	for _, n := range []string{"String", "Int", "Float", "Boolean", "ID", "DateTime", "URI", "GitObjectID", "HTML", "Base64String"} {
		s.types[n] = &objType{name: n, kind: "SCALAR", is: map[string]bool{}}
	}
	return s
}

// lookup returns the type named name, or nil.
func (s *schema) lookup(name string) *objType {
	return s.types[name]
}

// payloadFields are the fields of a mutation payload, read from its map.
func payloadFields(names []string) map[string]resolver {
	get := func(k string) resolver {
		return func(rq *request, src any, a args) (any, error) { return src.(payload)[k], nil }
	}
	out := map[string]resolver{"clientMutationId": get("clientMutationId")}
	for _, n := range names {
		out[n] = get(n)
	}
	return out
}

// ---- introspection ----

// fieldInfo is a field of a type, for introspection.
type fieldInfo struct {
	name string
}

// scalarType is the type advertised for every field: introspection only
// reports field names, which is what clients use to detect features.
var scalarType = &objType{name: "String", kind: "SCALAR", is: map[string]bool{}}

func schemaFields() map[string]resolver {
	return map[string]resolver{
		"queryType": func(rq *request, src any, a args) (any, error) {
			return obj{"__Type", src.(*schema).types["Query"]}, nil
		},
		"mutationType": func(rq *request, src any, a args) (any, error) {
			return obj{"__Type", src.(*schema).types["Mutation"]}, nil
		},
		"subscriptionType": constant(nil),
		"types": func(rq *request, src any, a args) (any, error) {
			sch := src.(*schema)
			names := make([]string, 0, len(sch.types))
			for n := range sch.types {
				names = append(names, n)
			}
			sort.Strings(names)
			out := make([]any, len(names))
			for i, n := range names {
				out[i] = obj{"__Type", sch.types[n]}
			}
			return out, nil
		},
		"directives": func(rq *request, src any, a args) (any, error) { return []any{}, nil },
	}
}

func typeFields() map[string]resolver {
	t := func(src any) *objType { return src.(*objType) }
	return map[string]resolver{
		"name":        func(rq *request, src any, a args) (any, error) { return t(src).name, nil },
		"kind":        func(rq *request, src any, a args) (any, error) { return t(src).kind, nil },
		"description": constant(nil),
		"fields": func(rq *request, src any, a args) (any, error) {
			if k := t(src).kind; k != "OBJECT" && k != "INTERFACE" {
				return nil, nil
			}
			out := make([]any, 0, len(t(src).order))
			for _, n := range t(src).order {
				out = append(out, obj{"__Field", fieldInfo{name: n}})
			}
			return out, nil
		},
		"inputFields": func(rq *request, src any, a args) (any, error) {
			if t(src).kind != "INPUT_OBJECT" {
				return nil, nil
			}
			out := make([]any, 0, len(t(src).order))
			for _, n := range t(src).order {
				out = append(out, obj{"__InputValue", fieldInfo{name: n}})
			}
			return out, nil
		},
		"enumValues": func(rq *request, src any, a args) (any, error) {
			if t(src).kind != "ENUM" {
				return nil, nil
			}
			out := make([]any, 0, len(t(src).enum))
			for _, n := range t(src).enum {
				out = append(out, obj{"__EnumValue", n})
			}
			return out, nil
		},
		"interfaces": func(rq *request, src any, a args) (any, error) {
			if t(src).kind != "OBJECT" {
				return nil, nil
			}
			return []any{}, nil
		},
		"possibleTypes":  constant(nil),
		"ofType":         constant(nil),
		"specifiedByURL": constant(nil),
	}
}

func fieldFields() map[string]resolver {
	return map[string]resolver{
		"name":              func(rq *request, src any, a args) (any, error) { return src.(fieldInfo).name, nil },
		"description":       constant(nil),
		"args":              func(rq *request, src any, a args) (any, error) { return []any{}, nil },
		"type":              func(rq *request, src any, a args) (any, error) { return obj{"__Type", scalarType}, nil },
		"isDeprecated":      constant(false),
		"deprecationReason": constant(nil),
	}
}

func inputValueFields() map[string]resolver {
	return map[string]resolver{
		"name":              func(rq *request, src any, a args) (any, error) { return src.(fieldInfo).name, nil },
		"description":       constant(nil),
		"type":              func(rq *request, src any, a args) (any, error) { return obj{"__Type", scalarType}, nil },
		"defaultValue":      constant(nil),
		"isDeprecated":      constant(false),
		"deprecationReason": constant(nil),
	}
}

func enumValueFields() map[string]resolver {
	return map[string]resolver{
		"name":              func(rq *request, src any, a args) (any, error) { return src.(string), nil },
		"description":       constant(nil),
		"isDeprecated":      constant(false),
		"deprecationReason": constant(nil),
	}
}
