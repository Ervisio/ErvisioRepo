// Package blobsvc holds what the Actions results services (cache and
// artifacts) share: runtime-token authentication, Twirp JSON encoding,
// signed blob URLs and Azure-style staged uploads on storage.
package blobsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/actions"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Verifier checks ACTIONS_RUNTIME_TOKEN values (actions.Service).
type Verifier interface {
	VerifyRuntimeToken(token string) (actions.RuntimeClaims, error)
}

// Job is the job a runtime token was issued for.
type Job = dbgen.GetActionsResultsJobRow

// ErrUnauthorized is returned for missing or invalid runtime tokens and for
// tokens of jobs already completed.
var ErrUnauthorized = errors.New("invalid runtime token")

// Auth resolves runtime tokens to jobs.
type Auth struct {
	V   Verifier
	Q   *dbgen.Queries
	Log *slog.Logger
}

// Job authenticates r by its bearer runtime token.
func (a *Auth) Job(ctx context.Context, r *http.Request) (Job, error) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return Job{}, ErrUnauthorized
	}
	c, err := a.V.VerifyRuntimeToken(strings.TrimSpace(tok))
	if err != nil {
		return Job{}, ErrUnauthorized
	}
	j, err := a.Q.GetActionsResultsJob(ctx, dbgen.GetActionsResultsJobParams{JobID: c.JobID, RunID: c.RunID, RepoID: c.RepoID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrUnauthorized
	}
	if err != nil {
		return Job{}, fmt.Errorf("load job: %w", err)
	}
	if j.JobStatus == "completed" {
		return Job{}, ErrUnauthorized
	}
	return j, nil
}

// Twirp wraps a Twirp method: it authenticates the request and answers
// auth failures with Twirp errors.
func (a *Auth) Twirp(fn func(w http.ResponseWriter, r *http.Request, j Job)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		j, err := a.Job(r.Context(), r)
		if err != nil {
			a.twirpAuthError(w, r, err)
			return
		}
		fn(w, r, j)
	}
}

// REST wraps a REST handler: auth failures are answered as problems.
func (a *Auth) REST(fn func(w http.ResponseWriter, r *http.Request, j Job)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		j, err := a.Job(r.Context(), r)
		if errors.Is(err, ErrUnauthorized) {
			Problem(w, http.StatusUnauthorized, "invalid runtime token")
			return
		}
		if err != nil {
			a.Log.ErrorContext(r.Context(), "actions results auth", "error", err)
			Problem(w, http.StatusInternalServerError, "")
			return
		}
		fn(w, r, j)
	}
}

func (a *Auth) twirpAuthError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrUnauthorized) {
		TwirpError(w, "unauthenticated", "invalid runtime token")
		return
	}
	a.Log.ErrorContext(r.Context(), "actions results auth", "error", err)
	TwirpError(w, "internal", "internal error")
}

// Problem writes an RFC 9457 error. detail must never contain secrets.
func Problem(w http.ResponseWriter, status int, detail string) {
	p := map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status}
	if detail != "" {
		p["detail"] = detail
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}
