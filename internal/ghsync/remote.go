package ghsync

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Remote is the GitHub counterpart of a local repository, with an API
// client authenticated as its connection.
type Remote struct {
	API         *API
	Owner, Name string
	// BaseURL is the web URL of the GitHub server.
	BaseURL string
}

// WebURL is the GitHub page of the repository.
func (r Remote) WebURL() string { return r.BaseURL + "/" + r.Owner + "/" + r.Name }

// Repo is the API path of the repository ("/repos/o/n").
func (r Remote) Repo() string {
	return ImportContext{RemoteOwner: r.Owner, RemoteName: r.Name}.Repo()
}

// source finds the GitHub repository of a local one and its connection:
// the sync link first, then the import. ok is false when there is none.
func (s *Service) source(ctx context.Context, repoID uuid.UUID) (owner, name string, conn uuid.UUID, ok bool, err error) {
	l, err := s.q.GetGitHubSyncLinkByRepo(ctx, repoID)
	if err == nil {
		return l.RemoteOwner, l.RemoteName, l.ConnectionID, true, nil
	}
	if !noRows(err) {
		return "", "", uuid.Nil, false, fmt.Errorf("get sync link: %w", err)
	}
	imp, err := s.q.GetGitHubImportByRepo(ctx, repoID)
	switch {
	case noRows(err):
		return "", "", uuid.Nil, false, nil
	case err != nil:
		return "", "", uuid.Nil, false, fmt.Errorf("get import: %w", err)
	case !imp.ConnectionID.Valid:
		return "", "", uuid.Nil, false, nil
	}
	return imp.RemoteOwner, imp.RemoteName, imp.ConnectionID.UUID, true, nil
}

// RemoteOf returns the GitHub repository of a local one: the one of its
// sync link, or else the one it was imported from. ErrNotFound when it
// has neither, or when the connection of the import was deleted.
func (s *Service) RemoteOf(ctx context.Context, repoID uuid.UUID) (Remote, error) {
	owner, name, conn, ok, err := s.source(ctx, repoID)
	if err != nil {
		return Remote{}, err
	}
	if !ok {
		return Remote{}, ErrNotFound
	}
	r, err := s.open(ctx, conn, owner, name)
	if err != nil {
		return Remote{}, err
	}
	return Remote{API: r.api, Owner: owner, Name: name, BaseURL: r.base}, nil
}

// Describe returns the GitHub repository of a local one without opening
// its connection: "owner/name" and its web URL; ok is false when there
// is none.
func (s *Service) Describe(ctx context.Context, repoID uuid.UUID) (full, web string, ok bool, err error) {
	owner, name, conn, ok, err := s.source(ctx, repoID)
	if err != nil || !ok {
		return "", "", false, err
	}
	c, err := s.q.GetGitHubConnection(ctx, conn)
	if noRows(err) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("get GitHub connection: %w", err)
	}
	return owner + "/" + name, c.GithubConnection.BaseUrl + "/" + owner + "/" + name, true, nil
}
