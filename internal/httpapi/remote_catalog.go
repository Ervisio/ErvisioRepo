package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

const catalogRepoPages = 10

func (p problemResponse) VisitListErvisioCodeRepositoryCatalogResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (s *v1Server) ListErvisioCodeRepositoryCatalog(ctx context.Context, req v1gen.ListErvisioCodeRepositoryCatalogRequestObject) (v1gen.ListErvisioCodeRepositoryCatalogResponseObject, error) {
	accountID, problem, err := s.remoteAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	account, problem, err := s.catalogPrincipal(ctx, accountID)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	local, localStatus := s.ervisioRepoCatalog(ctx, account)
	github, githubStatus := s.gitHubCatalog(ctx, accountID)
	devices, err := s.remoteDevices(ctx, accountID)
	if err != nil {
		return nil, err
	}
	projects := s.remote.listProjects(accountID, devices)
	return v1gen.ListErvisioCodeRepositoryCatalog200JSONResponse{
		Ervisiorepo: matchCatalog(local, localStatus, v1gen.ErvisioCodeRemoteRepositoryProviderErvisiorepo, projects),
		Github:      matchCatalog(github, githubStatus, v1gen.ErvisioCodeRemoteRepositoryProviderGithub, projects),
	}, nil
}

func (s *v1Server) catalogPrincipal(ctx context.Context, accountID uuid.UUID) (auth.Principal, problemResponse, error) {
	if p, ok := caller(ctx); ok && p.Method == auth.MethodSession && p.UserID == accountID {
		return p, problemResponse{}, nil
	}
	if _, hub := ctx.Value(hubSessionKey{}).(string); !hub {
		return auth.Principal{}, errAuthRequired, nil
	}
	if s.identity == nil {
		return auth.Principal{}, errIdentityUnavailable, nil
	}
	user, err := s.identity.User(ctx, accountID)
	if errors.Is(err, identity.ErrNotFound) {
		return auth.Principal{}, errAuthRequired, nil
	}
	return auth.Principal{UserID: accountID, Role: user.Role}, problemResponse{}, err
}

func (s *v1Server) ervisioRepoCatalog(ctx context.Context, account auth.Principal) ([]string, v1gen.ErvisioCodeProviderCatalogStatus) {
	if s.repos == nil {
		return nil, v1gen.CatalogUnavailable
	}
	var names []string
	for page := range catalogRepoPages {
		list, err := s.repos.ListMine(ctx, account, page*repos.MaxLimit, repos.MaxLimit)
		if err != nil {
			return names, failedCatalogStatus(names)
		}
		for _, repo := range list {
			names = append(names, repo.FullName())
		}
		if len(list) < repos.MaxLimit {
			return names, v1gen.CatalogReady
		}
	}
	return names, v1gen.CatalogPartial
}

func (s *v1Server) gitHubCatalog(ctx context.Context, accountID uuid.UUID) ([]string, v1gen.ErvisioCodeProviderCatalogStatus) {
	if s.github == nil {
		return nil, v1gen.CatalogUnavailable
	}
	names, connected, err := s.github.ListAccountRepositories(ctx, accountID)
	switch {
	case err != nil:
		return names, failedCatalogStatus(names)
	case !connected:
		return nil, v1gen.CatalogNotConnected
	default:
		return names, v1gen.CatalogReady
	}
}

func failedCatalogStatus(names []string) v1gen.ErvisioCodeProviderCatalogStatus {
	if len(names) == 0 {
		return v1gen.CatalogUnavailable
	}
	return v1gen.CatalogPartial
}

func matchCatalog(names []string, status v1gen.ErvisioCodeProviderCatalogStatus, provider v1gen.ErvisioCodeRemoteRepositoryProvider, projects []v1gen.ErvisioCodeRemoteProject) v1gen.ErvisioCodeProviderCatalog {
	byName := make(map[string][]v1gen.ErvisioCodeRemoteProject)
	for _, project := range projects {
		if project.Repository != nil && project.Repository.Provider == provider {
			key := strings.ToLower(project.Repository.Name)
			byName[key] = append(byName[key], project)
		}
	}
	slices.Sort(names)
	catalog := v1gen.ErvisioCodeProviderCatalog{Status: status, Repositories: make([]v1gen.ErvisioCodeCatalogRepository, 0, len(names))}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if !remoteRepositoryName.MatchString(name) {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		checkouts := byName[key]
		if checkouts == nil {
			checkouts = []v1gen.ErvisioCodeRemoteProject{}
		}
		catalog.Repositories = append(catalog.Repositories, v1gen.ErvisioCodeCatalogRepository{Name: name, Checkouts: checkouts})
	}
	return catalog
}
