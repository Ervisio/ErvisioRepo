package httpapi

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Profiles is the public profile part of the identity service (satisfied by
// *identity.Service).
type Profiles interface {
	ProfileDetails(ctx context.Context, userID uuid.UUID) (identity.ProfileDetails, error)
	UpdateProfileDetails(ctx context.Context, userID uuid.UUID, in identity.ProfileDetails, c identity.Client) (identity.ProfileDetails, error)
	VisibleOrgs(ctx context.Context, who auth.Principal, user uuid.UUID) ([]identity.OrgSummary, error)
}

// ProfilePinner lists the featured repositories of a profile (satisfied by *repos.Service).
type ProfilePinner interface {
	ListProfilePins(ctx context.Context, who auth.Principal, user uuid.UUID) ([]repos.Repo, error)
}

var (
	_ Profiles      = (*identity.Service)(nil)
	_ ProfilePinner = (*repos.Service)(nil)
)

// orgLanguageRepos bounds the repositories sized by ListOrgLanguages.
const orgLanguageRepos = 12

var errProfileNotFound = problemResponse{status: http.StatusNotFound, detail: "not found"}

func (p problemResponse) VisitGetUserProfileResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetProfileDetailsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitUpdateProfileDetailsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListProfilePinsResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListProfileActivityResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetUserContributionsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListReposWeeklyActivityResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListOrgLanguagesResponse(w http.ResponseWriter) error {
	return p.write(w)
}

// profileOf resolves a username to its public profile; nil problem on success.
func (s *v1Server) profileOf(ctx context.Context, username string) (identity.Profile, *problemResponse, error) {
	if s.identity == nil {
		return identity.Profile{}, &errIdentityUnavailable, nil
	}
	pr, err := s.identity.UserProfile(ctx, username)
	if errors.Is(err, identity.ErrNotFound) {
		return identity.Profile{}, &errProfileNotFound, nil
	}
	if err != nil {
		return identity.Profile{}, nil, err
	}
	return pr, nil, nil
}

// GetUserProfile serves the public profile page of a user; signed-out
// visitors may call it.
func (s *v1Server) GetUserProfile(ctx context.Context, req v1gen.GetUserProfileRequestObject) (v1gen.GetUserProfileResponseObject, error) {
	pr, fail, err := s.profileOf(ctx, req.Username)
	if fail != nil || err != nil {
		return failOr[v1gen.GetUserProfileResponseObject](fail, err)
	}
	out := v1gen.GetUserProfile200JSONResponse{Username: pr.Username, Kind: v1gen.UserProfileKind(pr.Kind),
		DisplayName: pr.DisplayName, AvatarUrl: pr.AvatarURL, CreatedAt: pr.CreatedAt}
	if pr.Description != "" {
		out.Description = &pr.Description
	}
	if pr.InstanceAdmin {
		out.InstanceAdmin = &pr.InstanceAdmin
	}
	d := pr.Details
	out.Bio, out.Company, out.Location, out.Website = nonEmpty(d.Bio), nonEmpty(d.Company), nonEmpty(d.Location), nonEmpty(d.Website)
	out.Timezone, out.PublicEmail = nonEmpty(d.Timezone), nonEmpty(d.PublicEmail)
	if len(d.SocialLinks) > 0 {
		out.SocialLinks = &d.SocialLinks
	}
	if ps, ok := s.identity.(Profiles); ok {
		orgs, err := ps.VisibleOrgs(ctx, principal(ctx), pr.ID)
		if err != nil {
			return nil, err
		}
		list := make([]v1gen.OrgSummary, 0, len(orgs))
		for _, o := range orgs {
			list = append(list, v1gen.OrgSummary{Name: o.Name, DisplayName: o.DisplayName})
		}
		out.Orgs = &list
	}
	return out, nil
}

func nonEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// failOr returns the problem as R when set, otherwise the error.
func failOr[R any](fail *problemResponse, err error) (R, error) {
	var zero R
	if fail != nil {
		if r, ok := any(*fail).(R); ok {
			return r, nil
		}
	}
	return zero, err
}

func detailsDTO(d identity.ProfileDetails) v1gen.ProfileDetails {
	return v1gen.ProfileDetails{Bio: d.Bio, Company: d.Company, Location: d.Location, Website: d.Website,
		SocialLinks: d.SocialLinks, Timezone: d.Timezone, PublicEmail: d.PublicEmail}
}

func (s *v1Server) GetProfileDetails(ctx context.Context, _ v1gen.GetProfileDetailsRequestObject) (v1gen.GetProfileDetailsResponseObject, error) {
	_, p, pr := s.account(ctx)
	if pr != nil {
		return *pr, nil
	}
	ps, ok := s.identity.(Profiles)
	if !ok {
		return errIdentityUnavailable, nil
	}
	d, err := ps.ProfileDetails(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return v1gen.GetProfileDetails200JSONResponse(detailsDTO(d)), nil
}

func (s *v1Server) UpdateProfileDetails(ctx context.Context, req v1gen.UpdateProfileDetailsRequestObject) (v1gen.UpdateProfileDetailsResponseObject, error) {
	_, p, pr := s.account(ctx)
	if pr != nil {
		return *pr, nil
	}
	ps, ok := s.identity.(Profiles)
	if !ok {
		return errIdentityUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("request body required"), nil
	}
	b := req.Body
	d, err := ps.UpdateProfileDetails(ctx, p.UserID, identity.ProfileDetails{Bio: b.Bio, Company: b.Company,
		Location: b.Location, Website: b.Website, SocialLinks: b.SocialLinks, Timezone: b.Timezone,
		PublicEmail: b.PublicEmail}, clientOf(ctx))
	if pr, ok := accountProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.UpdateProfileDetails200JSONResponse(detailsDTO(d)), nil
}

func (s *v1Server) ListProfilePins(ctx context.Context, req v1gen.ListProfilePinsRequestObject) (v1gen.ListProfilePinsResponseObject, error) {
	pinner, ok := s.repos.(ProfilePinner)
	if !ok {
		return errReposUnavailable, nil
	}
	pr, fail, err := s.profileOf(ctx, req.Username)
	if fail != nil || err != nil {
		return failOr[v1gen.ListProfilePinsResponseObject](fail, err)
	}
	list, err := pinner.ListProfilePins(ctx, principal(ctx), pr.ID)
	if err != nil {
		return dashFail[v1gen.ListProfilePinsResponseObject](err)
	}
	out, err := s.reposOut(ctx, list)
	if err != nil {
		return nil, err
	}
	return v1gen.ListProfilePins200JSONResponse(out), nil
}

func (s *v1Server) ListProfileActivity(ctx context.Context, req v1gen.ListProfileActivityRequestObject) (v1gen.ListProfileActivityResponseObject, error) {
	if s.dashboard == nil {
		return errDashboardUnavailable, nil
	}
	pr, fail, err := s.profileOf(ctx, req.Username)
	if fail != nil || err != nil {
		return failOr[v1gen.ListProfileActivityResponseObject](fail, err)
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, err := s.dashboard.UserActivity(ctx, principal(ctx), pr.ID, offset, limit)
	if err != nil {
		return dashFail[v1gen.ListProfileActivityResponseObject](err)
	}
	out := make(v1gen.ListProfileActivity200JSONResponse, 0, len(list))
	for _, i := range list {
		out = append(out, v1gen.ActivityItem{Id: i.ID, Kind: v1gen.ActivityItemKind(i.Kind), Action: i.Action,
			Actor: pr.Username, Owner: i.Owner, Repo: i.Repo, FullName: i.Owner + "/" + i.Repo, Number: i.Number,
			Title: i.Title, CreatedAt: i.CreatedAt})
	}
	return out, nil
}

func (s *v1Server) GetUserContributions(ctx context.Context, req v1gen.GetUserContributionsRequestObject) (v1gen.GetUserContributionsResponseObject, error) {
	if s.dashboard == nil {
		return errDashboardUnavailable, nil
	}
	pr, fail, err := s.profileOf(ctx, req.Username)
	if fail != nil || err != nil {
		return failOr[v1gen.GetUserContributionsResponseObject](fail, err)
	}
	cal, err := s.dashboard.Contributions(ctx, principal(ctx), pr.ID, deref(req.Params.Year), time.Now())
	if err != nil {
		return dashFail[v1gen.GetUserContributionsResponseObject](err)
	}
	out := v1gen.ContributionCalendar{From: openapi_types.Date{Time: cal.From}, To: openapi_types.Date{Time: cal.To},
		Total: cal.Total, Days: make([]v1gen.ContributionDay, 0, len(cal.Days)), Years: cal.Years}
	for _, d := range cal.Days {
		out.Days = append(out.Days, v1gen.ContributionDay{Date: openapi_types.Date{Time: d.Date}, Count: d.Count})
	}
	return v1gen.GetUserContributions200JSONResponse(out), nil
}

func (s *v1Server) ListReposWeeklyActivity(ctx context.Context, req v1gen.ListReposWeeklyActivityRequestObject) (v1gen.ListReposWeeklyActivityResponseObject, error) {
	if s.dashboard == nil {
		return errDashboardUnavailable, nil
	}
	weeks, err := s.dashboard.WeeklyActivity(ctx, principal(ctx), req.Params.Repo, time.Now())
	if err != nil {
		return dashFail[v1gen.ListReposWeeklyActivityResponseObject](err)
	}
	out := make(v1gen.ListReposWeeklyActivity200JSONResponse, 0, len(weeks))
	for _, id := range req.Params.Repo {
		if w, ok := weeks[id]; ok {
			out = append(out, v1gen.RepoWeeklyActivity{RepoId: id, Weeks: w})
			delete(weeks, id) // a repeated ID is listed once
		}
	}
	return out, nil
}

// ListOrgLanguages sums the language breakdown of the most recently updated
// hot repositories of an organization; cold ones are skipped so that the
// page never restores a repository.
func (s *v1Server) ListOrgLanguages(ctx context.Context, req v1gen.ListOrgLanguagesRequestObject) (v1gen.ListOrgLanguagesResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if s.repos == nil || s.browse == nil {
		return errReposUnavailable, nil
	}
	who := principal(ctx)
	if _, err := s.orgs.GetOrg(ctx, who, req.Org); err != nil {
		return orgsFail(err)
	}
	list, err := s.repos.ListOwner(ctx, who, req.Org, 0, 100)
	if err != nil {
		if pr, ok := repoParamProblem(err); ok {
			return pr, nil
		}
		return nil, err
	}
	list = slices.DeleteFunc(list, func(r repos.Repo) bool { return r.StorageState != "hot" || r.SizeBytes == 0 })
	slices.SortFunc(list, func(a, b repos.Repo) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	list = list[:min(len(list), orgLanguageRepos)]

	type sum struct {
		bytes int64
		color string
	}
	sums := map[string]*sum{}
	out := v1gen.OrgLanguages{Items: []v1gen.Language{}, Repos: []v1gen.RepoLanguage{}}
	for _, r := range list {
		res, err := s.browse.Languages(ctx, gitRepo(r), "")
		if err != nil {
			// One busy or broken repository should not hide the others.
			slog.DebugContext(ctx, "org languages: skip repository", "repo", r.FullName(), "err", err)
			continue
		}
		for i, l := range res.Items {
			if i == 0 && l.Name != "Other" {
				out.Repos = append(out.Repos, v1gen.RepoLanguage{Name: r.Name, Language: l.Name, Color: l.Color})
			}
			if sums[l.Name] == nil {
				sums[l.Name] = &sum{color: l.Color}
			}
			sums[l.Name].bytes += l.Bytes
			out.TotalBytes += l.Bytes
		}
	}
	for name, v := range sums {
		pct := 0.0
		if out.TotalBytes > 0 {
			pct = math.Round(float64(v.bytes)*1000/float64(out.TotalBytes)) / 10
		}
		out.Items = append(out.Items, v1gen.Language{Name: name, Bytes: v.bytes, Percent: pct, Color: v.color})
	}
	slices.SortFunc(out.Items, func(a, b v1gen.Language) int {
		if a.Name == "Other" || b.Name == "Other" { // the folded remainder stays last
			return cmp.Compare(boolInt(a.Name == "Other"), boolInt(b.Name == "Other"))
		}
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), cmp.Compare(a.Name, b.Name))
	})
	return v1gen.ListOrgLanguages200JSONResponse(out), nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
