package ghsync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

const (
	catalogTimeout        = 8 * time.Second
	catalogMaxConnections = 8
	catalogMaxPages       = 10
)

var errCatalogLimit = errors.New("GitHub catalog limit reached")

// ListAccountRepositories uses only the account's own GitHub.com connections.
// A failed connection or page does not discard previously authorized results.
func (s *Service) ListAccountRepositories(ctx context.Context, accountID uuid.UUID) (names []string, connected bool, err error) {
	if accountID == uuid.Nil {
		return nil, false, ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	rows, err := s.q.ListGitHubConnections(ctx, accountID)
	if err != nil {
		return nil, false, fmt.Errorf("list GitHub catalog connections: %w", err)
	}
	count := 0
	var incomplete error
	for _, row := range rows {
		if row.OwnerKind != "user" || row.GithubConnection.BaseUrl != DefaultBaseURL {
			continue
		}
		connected = true
		if count == catalogMaxConnections {
			return names, true, errCatalogLimit
		}
		count++
		repositories, err := s.listConnectionRepositories(ctx, row.GithubConnection)
		names = append(names, repositories...)
		if err != nil {
			incomplete = err
			if ctx.Err() != nil {
				break
			}
		}
	}
	return names, connected, incomplete
}

type catalogRepository struct {
	FullName string `json:"full_name"`
}

func (s *Service) listConnectionRepositories(ctx context.Context, row dbgen.GithubConnection) ([]string, error) {
	cred, err := s.credential(row)
	if err != nil {
		return nil, err
	}
	if cred.kind != kindApp && cred.kind != kindToken {
		return nil, ErrForbidden
	}
	api, err := s.tokens.api(ctx, cred)
	if err != nil {
		return nil, err
	}
	var names []string
	for page := 1; page <= catalogMaxPages; page++ {
		repositories, next, err := catalogPage(ctx, api, cred.kind, page)
		if err != nil {
			return names, err
		}
		for _, repo := range repositories {
			names = append(names, repo.FullName)
		}
		if !next {
			return names, nil
		}
	}
	return names, errCatalogLimit
}

func catalogPage(ctx context.Context, api *API, kind string, page int) ([]catalogRepository, bool, error) {
	query := "?per_page=100&page=" + strconv.Itoa(page)
	var repositories []catalogRepository
	var header http.Header
	var err error
	if kind == kindApp {
		var result struct {
			Repositories []catalogRepository `json:"repositories"`
		}
		header, err = api.do(ctx, http.MethodGet, "/installation/repositories"+query, nil, &result)
		repositories = result.Repositories
	} else {
		header, err = api.do(ctx, http.MethodGet, "/user/repos"+query, nil, &repositories)
	}
	if err != nil {
		return nil, false, err
	}
	next, err := api.nextPage(header.Get("Link"))
	return repositories, next != "", err
}
