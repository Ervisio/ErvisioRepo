// Package graphql implements the subset of the GitHub GraphQL API (v4) used
// by the gh CLI: a hand-written parser and executor (operations, variables
// with defaults, aliases, fragments, inline fragments, @include/@skip and
// __type introspection) over a schema whose resolvers call the same
// services as the REST API in internal/ghapi, with the same authorization.
// Global ids are the node_id values the REST API emits.
package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/orgs"
	"github.com/Ervisio/ErvisioRepo/internal/pulls"
	"github.com/Ervisio/ErvisioRepo/internal/releases"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/statuses"
)

// Repos is the subset of the repository service used by the resolvers.
type Repos interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
	ListOwner(ctx context.Context, who auth.Principal, owner string, offset, limit int) ([]repos.Repo, error)
	ListMine(ctx context.Context, who auth.Principal, offset, limit int) ([]repos.Repo, error)
	ListCollaborators(ctx context.Context, who auth.Principal, owner, name string) ([]repos.Collaborator, error)
	Search(ctx context.Context, who auth.Principal, query string, offset, limit int) ([]repos.Repo, error)
}

// Orgs is the subset of the organization service used by the resolvers.
type Orgs interface {
	GetOrg(ctx context.Context, who auth.Principal, name string) (orgs.Org, error)
	ListMyOrgs(ctx context.Context, who auth.Principal) ([]orgs.Org, error)
}

// Identity is the subset of the identity service used by the resolvers.
type Identity interface {
	User(ctx context.Context, id uuid.UUID) (identity.User, error)
}

// Issues is the subset of the issue service used by the resolvers.
type Issues interface {
	List(ctx context.Context, who auth.Principal, owner, name string, in issues.ListInput) ([]issues.Issue, error)
	Get(ctx context.Context, who auth.Principal, owner, name string, number int64) (issues.Issue, error)
	Create(ctx context.Context, who auth.Principal, owner, name string, in issues.CreateInput) (issues.Issue, error)
	Update(ctx context.Context, who auth.Principal, owner, name string, number int64, in issues.UpdateInput) (issues.Issue, error)
	ListComments(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]issues.Comment, error)
	GetComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (issues.Comment, error)
	CreateComment(ctx context.Context, who auth.Principal, owner, name string, number int64, body string) (issues.Comment, error)
	UpdateComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, body string) (issues.Comment, error)
	DeleteComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, c identity.Client) error
	ListLabels(ctx context.Context, who auth.Principal, owner, name string, offset, limit int) ([]issues.Label, error)
	ListMilestones(ctx context.Context, who auth.Principal, owner, name, state string, offset, limit int) ([]issues.Milestone, error)
}

// Pulls is the subset of the pull request service used by the resolvers.
type Pulls interface {
	Get(ctx context.Context, who auth.Principal, owner, name string, number int64) (pulls.Pull, error)
	List(ctx context.Context, who auth.Principal, owner, name string, o pulls.ListOptions) ([]pulls.Pull, error)
	Create(ctx context.Context, who auth.Principal, owner, name string, in pulls.CreateInput) (pulls.Pull, error)
	Update(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.UpdateInput) (pulls.Pull, error)
	ListCommits(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]gitbrowse.Commit, bool, error)
	ListFiles(ctx context.Context, who auth.Principal, owner, name string, number int64) ([]gitbrowse.FileChange, error)
	Merge(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.MergeInput) (string, error)
	ListReviews(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]pulls.Review, error)
	CreateReview(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.ReviewInput) (pulls.Review, error)
	RequestReviewers(ctx context.Context, who auth.Principal, owner, name string, number int64, reviewers []string) (pulls.Pull, error)
	RemoveReviewers(ctx context.Context, who auth.Principal, owner, name string, number int64, reviewers []string) (pulls.Pull, error)
	EnableAutoMerge(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.AutoMergeInput) (pulls.Pull, error)
	DisableAutoMerge(ctx context.Context, who auth.Principal, owner, name string, number int64) (pulls.Pull, error)
}

// Releases is the subset of the release service used by the resolvers.
type Releases interface {
	List(ctx context.Context, who auth.Principal, owner, name, tag string, offset, limit int) ([]releases.Release, error)
	Latest(ctx context.Context, who auth.Principal, owner, name string) (releases.Release, error)
}

// Checks is the subset of the commit status service used by
// statusCheckRollup.
type Checks interface {
	CombinedStatus(ctx context.Context, who auth.Principal, owner, name, ref string) (statuses.Combined, error)
	ListCheckRuns(ctx context.Context, who auth.Principal, owner, name, ref string, f statuses.CheckRunFilter) ([]statuses.CheckRun, int, string, error)
}

// Deps are the dependencies of the endpoint.
type Deps struct {
	Log       *slog.Logger
	DB        dbgen.DBTX
	PublicURL string // base URL of the web UI, without a trailing slash
	Repos     Repos
	Orgs      Orgs
	Identity  Identity
	Issues    Issues
	Pulls     Pulls
	Releases  Releases
	// Checks feeds statusCheckRollup; nil: the rollup is empty.
	Checks Checks
	// Classify maps a service error to an HTTP status and message, as the
	// REST API answers it; status 0 or 500 is an unexpected error.
	Classify func(error) (int, string)
}

// Server serves POST /api/graphql.
type Server struct {
	d    Deps
	q    *dbgen.Queries
	sch  *schema
	host string
}

// New builds the endpoint.
func New(d Deps) (*Server, error) {
	if d.DB == nil || d.Repos == nil || d.Orgs == nil || d.Identity == nil || d.Issues == nil ||
		d.Pulls == nil || d.Releases == nil || d.Classify == nil {
		return nil, errors.New("graphql: missing dependencies")
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	d.PublicURL = strings.TrimRight(d.PublicURL, "/")
	host := d.PublicURL
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexAny(host, "/"); i >= 0 {
		host = host[:i]
	}
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	return &Server{d: d, q: dbgen.New(d.DB), sch: buildSchema(), host: host}, nil
}

// maxBody bounds a request body.
const maxBody = 1 << 20

type gqlRequest struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables"`
	OperationName string         `json:"operationName"`
}

type gqlResponse struct {
	Data   any        `json:"data,omitempty"`
	Errors []gqlError `json:"errors,omitempty"`
}

// ServeHTTP runs one GraphQL request. The caller has authenticated it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	who, _ := auth.PrincipalFrom(r.Context())
	if who.UserID == uuid.Nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"message":           "This endpoint requires you to be authenticated.",
			"documentation_url": "https://docs.github.com/graphql/guides/forming-calls-with-graphql#authenticating-with-graphql",
		})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"message": "Use POST to run a GraphQL query"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"message": "Request body is too large"})
		return
	}
	var in gqlRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Problems parsing JSON"})
		return
	}
	writeJSON(w, http.StatusOK, s.execute(r.Context(), who, in))
}

func (s *Server) execute(ctx context.Context, who auth.Principal, in gqlRequest) gqlResponse {
	doc, err := parse(in.Query)
	if err != nil {
		e := gqlError{Message: err.Error()}
		var se *syntaxError
		if errors.As(err, &se) {
			e.Locations = []location{{Line: se.line, Column: se.col}}
		}
		return gqlResponse{Errors: []gqlError{e}}
	}
	op, err := pickOperation(doc, in.OperationName)
	if err != nil {
		return gqlResponse{Errors: []gqlError{{Message: err.Error()}}}
	}
	rq := newRequest(ctx, s, who)
	if op.kind == "mutation" {
		rq.mutation = true
	}
	ex := &executor{rq: rq, sch: s.sch, doc: doc, vars: coerceVariables(op, in.Variables)}
	data, err := ex.run(op)
	if err != nil {
		ex.errs = append(ex.errs, gqlError{Type: "INTERNAL", Message: err.Error()})
		if !errors.Is(err, errTooComplex) {
			s.d.Log.ErrorContext(ctx, "graphql execution failed", "err", err)
		}
		return gqlResponse{Errors: ex.errs}
	}
	return gqlResponse{Data: data, Errors: ex.errs}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		b = []byte(`{"errors":[{"type":"INTERNAL","message":"Something went wrong while executing your query."}]}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// classify maps a service error to a GraphQL error.
func (rq *request) classify(err error) *Error {
	status, msg := rq.s.d.Classify(err)
	switch {
	case status == http.StatusNotFound:
		return &Error{Type: "NOT_FOUND", Message: msg}
	case status == http.StatusForbidden || status == http.StatusUnauthorized:
		return &Error{Type: "FORBIDDEN", Message: msg}
	case status == http.StatusMovedPermanently:
		return &Error{Type: "NOT_FOUND", Message: "the repository has moved: " + msg}
	case status == http.StatusServiceUnavailable:
		return &Error{Type: "SERVICE_UNAVAILABLE", Message: msg}
	case status >= 400 && status < 500:
		return &Error{Type: "UNPROCESSABLE", Message: msg}
	}
	rq.s.d.Log.ErrorContext(rq.ctx, "graphql resolver failed", "err", err)
	return &Error{Type: "INTERNAL", Message: "Something went wrong while executing your query."}
}

// ---- arguments ----

// args are the coerced arguments of a field.
type args map[string]any

func (a args) has(k string) bool { return a[k] != nil }

func (a args) str(k string) string {
	switch v := a[k].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case int64:
		return strconv.FormatInt(v, 10)
	}
	return ""
}

// strp returns the string argument k, nil when absent or null.
func (a args) strp(k string) *string {
	if !a.has(k) {
		return nil
	}
	s := a.str(k)
	return &s
}

func toInt(v any) (int64, bool) {
	switch v := v.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, true
		}
		if f, err := v.Float64(); err == nil {
			return int64(f), true
		}
	case string:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func (a args) int(k string, def int) int {
	if n, ok := toInt(a[k]); ok {
		return int(n)
	}
	return def
}

func (a args) int64(k string) (int64, bool) { return toInt(a[k]) }

func (a args) boolean(k string) (bool, bool) {
	b, ok := a[k].(bool)
	return b, ok
}

// list returns a list argument of strings; a single value is a list of one.
func (a args) list(k string) []string {
	switch v := a[k].(type) {
	case nil:
		return nil
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s := (args{"v": x}).str("v"); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		if s := a.str(k); s != "" {
			return []string{s}
		}
	}
	return nil
}

func (a args) obj(k string) args {
	if m, ok := a[k].(map[string]any); ok {
		return args(m)
	}
	return args{}
}

// input returns the input argument of a mutation.
func (a args) input() (args, error) {
	m, ok := a["input"].(map[string]any)
	if !ok {
		return nil, errorf("UNPROCESSABLE", "the input argument is required")
	}
	return args(m), nil
}

func (a args) required(k string) (string, error) {
	s := a.str(k)
	if s == "" {
		return "", errorf("UNPROCESSABLE", "Argument '%s' on input is required", k)
	}
	return s, nil
}
