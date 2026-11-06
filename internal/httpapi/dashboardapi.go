package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/dashboard"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// RepoExplorer lists public repositories and manages the pins of a user
// (satisfied by *repos.Service). The Repos dependency serves it when it
// implements it.
type RepoExplorer interface {
	Explore(ctx context.Context, who auth.Principal, query, sort string, page, perPage int) (repos.ExploreList, error)
	ListPins(ctx context.Context, who auth.Principal) ([]repos.Repo, error)
	Pin(ctx context.Context, who auth.Principal, owner, name string) error
	Unpin(ctx context.Context, who auth.Principal, owner, name string) error
}

var _ RepoExplorer = (*repos.Service)(nil)

var errDashboardUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "the dashboard is not available"}

func (p problemResponse) VisitExploreReposResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListUserActionsRunsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListUserActivityResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListUserIssuesResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListUserPinsResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitPinRepoResponse(w http.ResponseWriter) error        { return p.write(w) }
func (p problemResponse) VisitUnpinRepoResponse(w http.ResponseWriter) error      { return p.write(w) }
func (p problemResponse) VisitGetUserPreferencesResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitUpdateUserPreferencesResponse(w http.ResponseWriter) error {
	return p.write(w)
}

// dashboardProblem maps an error of the dashboard, explore or pin endpoints.
func dashboardProblem(err error) (problemResponse, bool) {
	var ve *dashboard.ValidationError
	switch {
	case errors.Is(err, dashboard.ErrUnauthenticated):
		return problemResponse{status: http.StatusUnauthorized, detail: err.Error()}, true
	case errors.As(err, &ve):
		return badRequest(err.Error()), true
	case errors.Is(err, repos.ErrPinLimit):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, true
	}
	return repoParamProblem(err)
}

func signedIn(ctx context.Context) bool { return principal(ctx).UserID != uuid.Nil }

func (s *v1Server) ListUserIssues(ctx context.Context, req v1gen.ListUserIssuesRequestObject) (v1gen.ListUserIssuesResponseObject, error) {
	if s.dashboard == nil {
		return errDashboardUnavailable, nil
	}
	f := dashboard.IssueFilter{Page: deref(req.Params.Page), PerPage: deref(req.Params.PerPage)}
	if req.Params.Filter != nil {
		f.Filter = string(*req.Params.Filter)
	}
	if req.Params.Type != nil {
		f.Type = string(*req.Params.Type)
	}
	if req.Params.State != nil {
		f.State = string(*req.Params.State)
	}
	list, err := s.dashboard.Issues(ctx, principal(ctx), f)
	if err != nil {
		return dashFail[v1gen.ListUserIssuesResponseObject](err)
	}
	out := v1gen.ListUserIssues200JSONResponse{TotalCount: list.Total, Items: make([]v1gen.UserIssue, 0, len(list.Items))}
	for _, i := range list.Items {
		labels := make([]v1gen.LabelRef, 0, len(i.Labels))
		for _, l := range i.Labels {
			labels = append(labels, v1gen.LabelRef{Name: l.Name, Color: l.Color})
		}
		out.Items = append(out.Items, v1gen.UserIssue{Owner: i.Owner, Repo: i.Repo, FullName: i.Owner + "/" + i.Repo,
			Number: i.Number, Title: i.Title, Kind: v1gen.UserIssueKind(i.Kind), State: v1gen.UserIssueState(i.State),
			Author: i.Author, Labels: labels, CommentCount: i.CommentCount, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt})
	}
	return out, nil
}

func (s *v1Server) ListUserActionsRuns(ctx context.Context, req v1gen.ListUserActionsRunsRequestObject) (v1gen.ListUserActionsRunsResponseObject, error) {
	if s.dashboard == nil {
		return errDashboardUnavailable, nil
	}
	list, err := s.dashboard.Runs(ctx, principal(ctx), deref(req.Params.Status), deref(req.Params.Page), deref(req.Params.PerPage))
	if err != nil {
		return dashFail[v1gen.ListUserActionsRunsResponseObject](err)
	}
	out := v1gen.ListUserActionsRuns200JSONResponse{TotalCount: list.Total, Runs: make([]v1gen.UserActionsRun, 0, len(list.Items))}
	for _, r := range list.Items {
		full := r.Owner + "/" + r.Repo
		out.Runs = append(out.Runs, v1gen.UserActionsRun{Owner: r.Owner, Repo: r.Repo, FullName: full, Id: r.ID,
			RunNumber: r.RunNumber, WorkflowName: r.WorkflowName, Title: r.Title, Branch: r.Branch, Event: r.Event,
			Status: v1gen.UserActionsRunStatus(r.Status), Conclusion: r.Conclusion,
			Url: "/" + full + "/actions/runs/" + r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
	}
	return out, nil
}

func (s *v1Server) ListUserActivity(ctx context.Context, req v1gen.ListUserActivityRequestObject) (v1gen.ListUserActivityResponseObject, error) {
	if s.dashboard == nil {
		return errDashboardUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, err := s.dashboard.Activity(ctx, principal(ctx), offset, limit)
	if err != nil {
		return dashFail[v1gen.ListUserActivityResponseObject](err)
	}
	out := make(v1gen.ListUserActivity200JSONResponse, 0, len(list))
	for _, i := range list {
		out = append(out, v1gen.ActivityItem{Id: i.ID, Kind: v1gen.ActivityItemKind(i.Kind), Action: i.Action,
			Actor: i.Actor, Owner: i.Owner, Repo: i.Repo, FullName: i.Owner + "/" + i.Repo, Number: i.Number,
			Title: i.Title, CreatedAt: i.CreatedAt})
	}
	return out, nil
}

func (s *v1Server) ExploreRepos(ctx context.Context, req v1gen.ExploreReposRequestObject) (v1gen.ExploreReposResponseObject, error) {
	rx, ok := s.repos.(RepoExplorer)
	if !ok {
		return errReposUnavailable, nil
	}
	sort := ""
	if req.Params.Sort != nil {
		sort = string(*req.Params.Sort)
	}
	list, err := rx.Explore(ctx, principal(ctx), deref(req.Params.Q), sort, deref(req.Params.Page), deref(req.Params.PerPage))
	if err != nil {
		return dashFail[v1gen.ExploreReposResponseObject](err)
	}
	return v1gen.ExploreRepos200JSONResponse{TotalCount: list.Total, Items: repoList(list.Items)}, nil
}

func (s *v1Server) ListUserPins(ctx context.Context, _ v1gen.ListUserPinsRequestObject) (v1gen.ListUserPinsResponseObject, error) {
	rx, ok := s.repos.(RepoExplorer)
	if !ok {
		return errReposUnavailable, nil
	}
	who := principal(ctx)
	if !signedIn(ctx) {
		return unauthenticated, nil
	}
	list, err := rx.ListPins(ctx, who)
	if err != nil {
		return dashFail[v1gen.ListUserPinsResponseObject](err)
	}
	return v1gen.ListUserPins200JSONResponse(repoList(list)), nil
}

func (s *v1Server) PinRepo(ctx context.Context, req v1gen.PinRepoRequestObject) (v1gen.PinRepoResponseObject, error) {
	rx, ok := s.repos.(RepoExplorer)
	if !ok {
		return errReposUnavailable, nil
	}
	if !signedIn(ctx) {
		return unauthenticated, nil
	}
	if err := rx.Pin(ctx, principal(ctx), req.Owner, req.Name); err != nil {
		return dashFail[v1gen.PinRepoResponseObject](err)
	}
	return v1gen.PinRepo204Response{}, nil
}

func (s *v1Server) UnpinRepo(ctx context.Context, req v1gen.UnpinRepoRequestObject) (v1gen.UnpinRepoResponseObject, error) {
	rx, ok := s.repos.(RepoExplorer)
	if !ok {
		return errReposUnavailable, nil
	}
	if !signedIn(ctx) {
		return unauthenticated, nil
	}
	if err := rx.Unpin(ctx, principal(ctx), req.Owner, req.Name); err != nil {
		return dashFail[v1gen.UnpinRepoResponseObject](err)
	}
	return v1gen.UnpinRepo204Response{}, nil
}

func (s *v1Server) GetUserPreferences(ctx context.Context, _ v1gen.GetUserPreferencesRequestObject) (v1gen.GetUserPreferencesResponseObject, error) {
	if s.dashboard == nil {
		return errDashboardUnavailable, nil
	}
	p, err := s.dashboard.Preferences(ctx, principal(ctx))
	if err != nil {
		return dashFail[v1gen.GetUserPreferencesResponseObject](err)
	}
	return v1gen.GetUserPreferences200JSONResponse(prefsDTO(p)), nil
}

func (s *v1Server) UpdateUserPreferences(ctx context.Context, req v1gen.UpdateUserPreferencesRequestObject) (v1gen.UpdateUserPreferencesResponseObject, error) {
	if s.dashboard == nil {
		return errDashboardUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("a request body is required"), nil
	}
	var theme, layout *string
	if req.Body.Theme != nil {
		t := string(*req.Body.Theme)
		theme = &t
	}
	if req.Body.Layout != nil {
		l := string(*req.Body.Layout)
		layout = &l
	}
	p, err := s.dashboard.UpdatePreferences(ctx, principal(ctx), theme, layout)
	if err != nil {
		return dashFail[v1gen.UpdateUserPreferencesResponseObject](err)
	}
	return v1gen.UpdateUserPreferences200JSONResponse(prefsDTO(p)), nil
}

func prefsDTO(p dashboard.Preferences) v1gen.UserPreferences {
	return v1gen.UserPreferences{Theme: v1gen.UserPreferencesTheme(p.Theme), Layout: v1gen.UserPreferencesLayout(p.Layout)}
}

var unauthenticated = problemResponse{status: http.StatusUnauthorized, detail: dashboard.ErrUnauthenticated.Error()}

// dashFail maps an error to a problem response of type R, or returns the
// error when it is not a client error.
func dashFail[R any](err error) (R, error) {
	var zero R
	if p, ok := dashboardProblem(err); ok {
		r, ok := any(p).(R)
		if !ok {
			return zero, err
		}
		return r, nil
	}
	return zero, err
}
