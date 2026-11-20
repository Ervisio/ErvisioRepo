package ghsync

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// AuthorizeAccountRepository checks one repository against the account's current GitHub.com connections.
func (s *Service) AuthorizeAccountRepository(ctx context.Context, accountID uuid.UUID, owner, name string) error {
	if accountID == uuid.Nil {
		return ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	rows, err := s.q.ListGitHubConnections(ctx, accountID)
	if err != nil {
		return fmt.Errorf("list personal GitHub connections: %w", err)
	}
	client := *s.tokens.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, row := range rows {
		conn := row.GithubConnection
		if row.OwnerKind != "user" || conn.BaseUrl != DefaultBaseURL {
			continue
		}
		cred, err := s.credential(conn)
		if err != nil || checkRepositoryAccess(ctx, cred, &client, owner, name) != nil {
			if ctx.Err() != nil {
				break
			}
			continue
		}
		current, err := s.q.GetGitHubConnection(ctx, conn.ID)
		if noRows(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("check current GitHub connection: %w", err)
		}
		if current.GithubConnection.OwnerID == accountID && current.GithubConnection.UpdatedAt.Time.Equal(conn.UpdatedAt.Time) {
			return nil
		}
	}
	return ErrForbidden
}

func checkRepositoryAccess(ctx context.Context, cred credential, client *http.Client, owner, name string) error {
	api := &API{base: APIURL(DefaultBaseURL), http: client}
	switch cred.kind {
	case kindApp:
		token, _, err := mintInstallationToken(ctx, cred, client, nil)
		if err != nil {
			return err
		}
		api.token = token
	case kindToken:
		api.token = string(cred.secret)
	default:
		return ErrForbidden
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	var repository struct {
		ID         int64  `json:"id"`
		FullName   string `json:"full_name"`
		Visibility string `json:"visibility"`
	}
	if err := api.Do(ctx, http.MethodGet, path, nil, &repository); err != nil {
		return err
	}
	if repository.ID <= 0 || !strings.EqualFold(repository.FullName, owner+"/"+name) {
		return ErrForbidden
	}
	if cred.kind == kindApp {
		body := struct {
			RepositoryIDs []int64           `json:"repository_ids"`
			Permissions   map[string]string `json:"permissions"`
		}{[]int64{repository.ID}, map[string]string{"contents": "read"}}
		_, _, err := mintInstallationToken(ctx, cred, client, body)
		return err
	}
	switch repository.Visibility {
	case "private", "internal":
	case "public":
		// Public contents are readable without a grant, so require explicit affiliation first.
		var viewer struct {
			Login string `json:"login"`
		}
		if err := api.Do(ctx, http.MethodGet, "/user", nil, &viewer); err != nil {
			return err
		}
		if viewer.Login == "" {
			return ErrForbidden
		}
		if err := api.Do(ctx, http.MethodGet, path+"/collaborators/"+url.PathEscape(viewer.Login), nil, nil); err != nil {
			return err
		}
	default:
		return ErrForbidden
	}
	return api.Do(ctx, http.MethodGet, path+"/branches?per_page=1", nil, nil)
}
