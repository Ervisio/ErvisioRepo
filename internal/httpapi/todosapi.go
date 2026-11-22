package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

type todoScope struct {
	q    *dbgen.Queries
	repo uuid.NullUUID
	org  uuid.NullUUID
	user uuid.UUID
}

func boundTodoRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		orgTodo := strings.HasPrefix(path, "/api/v1/orgs/") && strings.Count(path, "/") == 5
		repoTodo := strings.HasPrefix(path, "/api/v1/repos/") && strings.Count(path, "/") == 6
		if (orgTodo || repoTodo) && strings.HasSuffix(path, "/todo") {
			w.Header().Set("Cache-Control", "no-store")
			if r.Body != nil && (r.Method == http.MethodPut || r.Method == http.MethodPatch) {
				r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
			}
		}
		next.ServeHTTP(w, r)
	})
}

type todoProblem problemResponse

func (p todoProblem) write(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "no-store")
	return problemResponse(p).write(w)
}

func (p todoProblem) VisitGetOrgTodoResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p todoProblem) VisitPutOrgTodoResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p todoProblem) VisitPatchOrgTodoResponse(w http.ResponseWriter) error { return p.write(w) }
func (p todoProblem) VisitGetRepoTodoResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p todoProblem) VisitPutRepoTodoResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p todoProblem) VisitPatchRepoTodoResponse(w http.ResponseWriter) error {
	return p.write(w)
}

var errTodoNotFound = errors.New("todo list not found")
var errTodoConflict = errors.New("the todo list changed; reload and try again")
var errTodoUnavailable = errors.New("todo lists are not available")

func todoFailure(err error) (todoProblem, error) {
	switch {
	case errors.Is(err, errTodoNotFound):
		return todoProblem{status: http.StatusNotFound, detail: errTodoNotFound.Error()}, nil
	case errors.Is(err, errTodoConflict):
		return todoProblem{status: http.StatusConflict, detail: errTodoConflict.Error()}, nil
	case errors.Is(err, errTodoUnavailable):
		return todoProblem{status: http.StatusServiceUnavailable, detail: errTodoUnavailable.Error()}, nil
	default:
		return todoProblem{}, err
	}
}

func (s *v1Server) todoScope(ctx context.Context, owner, name string, repository bool) (todoScope, error) {
	who := principal(ctx)
	if who.UserID == uuid.Nil || who.Automated() || who.UserApp != nil || who.App != nil {
		return todoScope{}, errTodoNotFound
	}
	if s.db == nil {
		return todoScope{}, errTodoUnavailable
	}
	q := dbgen.New(s.db)
	scope := todoScope{q: q, user: who.UserID}
	if !repository {
		org, err := q.GetOrgByName(ctx, name)
		if errors.Is(err, pgx.ErrNoRows) {
			return todoScope{}, errTodoNotFound
		}
		if err != nil {
			return todoScope{}, fmt.Errorf("get todo organization: %w", err)
		}
		role, err := q.GetOrgMemberRole(ctx, dbgen.GetOrgMemberRoleParams{OrgID: org.ID, UserID: who.UserID})
		if errors.Is(err, pgx.ErrNoRows) {
			return todoScope{}, errTodoNotFound
		}
		if err != nil {
			return todoScope{}, fmt.Errorf("get todo organization membership: %w", err)
		}
		if !authz.OrgRole(role).Member() {
			return todoScope{}, errTodoNotFound
		}
		scope.org = uuid.NullUUID{UUID: org.ID, Valid: true}
		return scope, nil
	}

	row, err := q.GetRepoByPath(ctx, dbgen.GetRepoByPathParams{Owner: owner, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return todoScope{}, errTodoNotFound
	}
	if err != nil {
		return todoScope{}, fmt.Errorf("get todo repository: %w", err)
	}
	facts, err := repos.Facts(ctx, q, who, row.Repo, row.OwnerKind)
	if err != nil {
		return todoScope{}, fmt.Errorf("get todo repository membership: %w", err)
	}
	// Public visibility and instance administration do not make someone a collaborator.
	facts.Visibility = authz.Private
	facts.InstanceAdmin = false
	if authz.RepoPermission(facts) < authz.PermRead {
		return todoScope{}, errTodoNotFound
	}
	scope.repo = uuid.NullUUID{UUID: row.Repo.ID, Valid: true}
	return scope, nil
}

func (s todoScope) get(ctx context.Context) (v1gen.TodoViewJSONResponse, error) {
	board := v1gen.TodoBoard{Categories: []v1gen.TodoCategory{}, Items: []v1gen.TodoItem{}}
	row, err := s.q.GetSharedTodo(ctx, dbgen.GetSharedTodoParams{RepoID: s.repo, OrgID: s.org})
	switch {
	case err == nil:
		board.Revision = row.Revision
		if err := json.Unmarshal(row.Categories, &board.Categories); err != nil {
			return v1gen.TodoViewJSONResponse{}, fmt.Errorf("decode todo categories: %w", err)
		}
		if err := json.Unmarshal(row.Items, &board.Items); err != nil {
			return v1gen.TodoViewJSONResponse{}, fmt.Errorf("decode todo items: %w", err)
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return v1gen.TodoViewJSONResponse{}, fmt.Errorf("get shared todo: %w", err)
	}
	collapsed, err := s.q.GetSharedTodoPreference(ctx, dbgen.GetSharedTodoPreferenceParams{
		RepoID: s.repo, OrgID: s.org, UserID: s.user,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return v1gen.TodoViewJSONResponse{}, fmt.Errorf("get todo preference: %w", err)
	}
	cacheControl := "no-store"
	return v1gen.TodoViewJSONResponse{
		Body:    v1gen.TodoView{Board: board, Collapsed: collapsed},
		Headers: v1gen.TodoViewResponseHeaders{CacheControl: &cacheControl},
	}, nil
}

func todoTextValid(text string, maxLength int) bool {
	return text != "" && utf8.RuneCountInString(text) <= maxLength && strings.IndexFunc(text, unicode.IsControl) < 0
}

func validateTodoBoard(board *v1gen.TodoBoard) string {
	if board == nil || board.Revision < 0 || board.Categories == nil || board.Items == nil {
		return "revision, categories and items are required"
	}
	if len(board.Categories) > 64 || len(board.Items) > 500 {
		return "at most 64 categories and 500 items are allowed"
	}
	categories := make(map[uuid.UUID]struct{}, len(board.Categories))
	for i := range board.Categories {
		category := &board.Categories[i]
		category.Name = strings.TrimSpace(category.Name)
		if category.Id == uuid.Nil || !todoTextValid(category.Name, 80) {
			return "every category needs a valid ID and a name of 1 to 80 characters"
		}
		if _, duplicate := categories[category.Id]; duplicate {
			return "category IDs must be unique"
		}
		categories[category.Id] = struct{}{}
	}
	items := make(map[uuid.UUID]struct{}, len(board.Items))
	for i := range board.Items {
		item := &board.Items[i]
		item.Title = strings.TrimSpace(item.Title)
		if item.Id == uuid.Nil || !todoTextValid(item.Title, 240) {
			return "every item needs a valid ID and a title of 1 to 240 characters"
		}
		if _, duplicate := items[item.Id]; duplicate {
			return "item IDs must be unique"
		}
		items[item.Id] = struct{}{}
		if item.CategoryId != nil {
			if _, ok := categories[*item.CategoryId]; !ok {
				return "item category does not exist"
			}
		}
	}
	return ""
}

func (s todoScope) put(ctx context.Context, board v1gen.TodoBoard) (v1gen.TodoBoardJSONResponse, error) {
	categories, err := json.Marshal(board.Categories)
	if err != nil {
		return v1gen.TodoBoardJSONResponse{}, fmt.Errorf("encode todo categories: %w", err)
	}
	items, err := json.Marshal(board.Items)
	if err != nil {
		return v1gen.TodoBoardJSONResponse{}, fmt.Errorf("encode todo items: %w", err)
	}
	revision, err := s.q.SaveSharedTodo(ctx, dbgen.SaveSharedTodoParams{
		RepoID: s.repo, OrgID: s.org, ExpectedRevision: board.Revision, Categories: categories, Items: items,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return v1gen.TodoBoardJSONResponse{}, errTodoConflict
	}
	if err != nil {
		return v1gen.TodoBoardJSONResponse{}, fmt.Errorf("save shared todo: %w", err)
	}
	board.Revision = revision
	cacheControl := "no-store"
	return v1gen.TodoBoardJSONResponse{
		Body:    board,
		Headers: v1gen.TodoBoardResponseHeaders{CacheControl: &cacheControl},
	}, nil
}

func (s todoScope) patch(ctx context.Context, collapsed bool) error {
	if err := s.q.SaveSharedTodoPreference(ctx, dbgen.SaveSharedTodoPreferenceParams{
		RepoID: s.repo, OrgID: s.org, UserID: s.user, Collapsed: collapsed,
	}); err != nil {
		return fmt.Errorf("save todo preference: %w", err)
	}
	return nil
}

func (s *v1Server) GetOrgTodo(ctx context.Context, req v1gen.GetOrgTodoRequestObject) (v1gen.GetOrgTodoResponseObject, error) {
	scope, err := s.todoScope(ctx, "", req.Org, false)
	if err != nil {
		return todoFailure(err)
	}
	out, err := scope.get(ctx)
	if err != nil {
		return todoFailure(err)
	}
	return v1gen.GetOrgTodo200JSONResponse{TodoViewJSONResponse: out}, nil
}

func (s *v1Server) GetRepoTodo(ctx context.Context, req v1gen.GetRepoTodoRequestObject) (v1gen.GetRepoTodoResponseObject, error) {
	scope, err := s.todoScope(ctx, req.Owner, req.Name, true)
	if err != nil {
		return todoFailure(err)
	}
	out, err := scope.get(ctx)
	if err != nil {
		return todoFailure(err)
	}
	return v1gen.GetRepoTodo200JSONResponse{TodoViewJSONResponse: out}, nil
}

func (s *v1Server) PutOrgTodo(ctx context.Context, req v1gen.PutOrgTodoRequestObject) (v1gen.PutOrgTodoResponseObject, error) {
	scope, err := s.todoScope(ctx, "", req.Org, false)
	if err != nil {
		return todoFailure(err)
	}
	if detail := validateTodoBoard(req.Body); detail != "" {
		return todoProblem(badRequest(detail)), nil
	}
	out, err := scope.put(ctx, *req.Body)
	if err != nil {
		return todoFailure(err)
	}
	return v1gen.PutOrgTodo200JSONResponse{TodoBoardJSONResponse: out}, nil
}

func (s *v1Server) PutRepoTodo(ctx context.Context, req v1gen.PutRepoTodoRequestObject) (v1gen.PutRepoTodoResponseObject, error) {
	scope, err := s.todoScope(ctx, req.Owner, req.Name, true)
	if err != nil {
		return todoFailure(err)
	}
	if detail := validateTodoBoard(req.Body); detail != "" {
		return todoProblem(badRequest(detail)), nil
	}
	out, err := scope.put(ctx, *req.Body)
	if err != nil {
		return todoFailure(err)
	}
	return v1gen.PutRepoTodo200JSONResponse{TodoBoardJSONResponse: out}, nil
}

func (s *v1Server) PatchOrgTodo(ctx context.Context, req v1gen.PatchOrgTodoRequestObject) (v1gen.PatchOrgTodoResponseObject, error) {
	scope, err := s.todoScope(ctx, "", req.Org, false)
	if err != nil {
		return todoFailure(err)
	}
	if req.Body == nil {
		return todoProblem(badRequest("missing body")), nil
	}
	if err := scope.patch(ctx, req.Body.Collapsed); err != nil {
		return todoFailure(err)
	}
	return v1gen.PatchOrgTodo204Response{}, nil
}

func (s *v1Server) PatchRepoTodo(ctx context.Context, req v1gen.PatchRepoTodoRequestObject) (v1gen.PatchRepoTodoResponseObject, error) {
	scope, err := s.todoScope(ctx, req.Owner, req.Name, true)
	if err != nil {
		return todoFailure(err)
	}
	if req.Body == nil {
		return todoProblem(badRequest("missing body")), nil
	}
	if err := scope.patch(ctx, req.Body.Collapsed); err != nil {
		return todoFailure(err)
	}
	return v1gen.PatchRepoTodo204Response{}, nil
}
