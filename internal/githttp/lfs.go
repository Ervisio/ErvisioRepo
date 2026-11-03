package githttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// LFS serves the Git LFS endpoints under /<owner>/<repo>.git/info/lfs/
// (implemented by internal/lfs). The handler authenticates the caller and
// resolves the repository; the LFS server checks the operation.
type LFS interface {
	// LookupToken resolves a short-lived LFS token sent as
	// "Authorization: RemoteAuth <token>". It returns ErrNoLFSToken for an
	// unknown or expired token.
	LookupToken(ctx context.Context, token string) (LFSToken, error)
	// ServeLFS serves one request for an authorized repository.
	ServeLFS(w http.ResponseWriter, r *http.Request, req LFSRequest)
}

// ErrNoLFSToken is returned by LFS.LookupToken for an invalid token.
var ErrNoLFSToken = errors.New("githttp: invalid or expired LFS token")

// LFSToken is a resolved short-lived LFS token: it acts for a user on one
// repository, for downloads only or for uploads too.
type LFSToken struct {
	Principal auth.Principal
	RepoID    uuid.UUID
	Upload    bool
}

// LFSRequest is an authenticated LFS request.
type LFSRequest struct {
	Repo     repos.Repo
	Caller   auth.Principal
	RemoteIP string
	// Path is the endpoint below info/lfs, without a leading slash
	// ("objects/batch", "locks/verify", ...).
	Path string
	// Token is set when the caller authenticated with an LFS token; the
	// token's repository is Repo.
	Token *LFSToken
}

// LFSAuthScheme is the Authorization scheme of short-lived LFS tokens.
const LFSAuthScheme = "RemoteAuth"

const lfsMarker = "/info/lfs/"

// lfsMediaType is the media type of LFS JSON documents.
const lfsMediaType = "application/vnd.git-lfs+json"

// parseLFSPath splits /<owner>/<repo>[.git]/info/lfs/<rest>.
func parseLFSPath(p string) (owner, name, rest string, ok bool) {
	i := strings.Index(p, lfsMarker)
	if i < 0 {
		return "", "", "", false
	}
	rest = p[i+len(lfsMarker):]
	owner, name, found := strings.Cut(strings.TrimPrefix(p[:i], "/"), "/")
	if !found || rest == "" {
		return "", "", "", false
	}
	name = strings.TrimSuffix(name, ".git")
	if identity.ValidateName(owner) != nil || git.ValidateRepoName(name) != nil {
		return "", "", "", false
	}
	return owner, name, rest, true
}

func (h *Handler) serveLFS(w http.ResponseWriter, r *http.Request, owner, name, rest string) {
	ctx := r.Context()
	ip := auth.ClientIP(r, h.o.TrustedProxies)
	var tok *LFSToken
	var who auth.Principal
	if scheme, cred, found := strings.Cut(r.Header.Get("Authorization"), " "); found && strings.EqualFold(scheme, LFSAuthScheme) {
		if h.o.Failures != nil {
			if wait := h.o.Failures.LoginWait(auth.IPKey(ip)); wait > 0 {
				LFSError(w, http.StatusTooManyRequests, "too many failed authentications; retry in "+wait.Round(time.Second).String())
				return
			}
		}
		t, err := h.o.LFS.LookupToken(ctx, strings.TrimSpace(cred))
		switch {
		case errors.Is(err, ErrNoLFSToken):
			h.fail(ip)
			LFSUnauthorized(w, "invalid or expired LFS token; run the command again")
			return
		case err != nil:
			h.o.Log.ErrorContext(ctx, "git lfs: look up token", "error", err)
			LFSError(w, http.StatusServiceUnavailable, "authentication unavailable")
			return
		}
		tok, who = &t, t.Principal
	} else {
		p, status, msg := h.authenticate(ctx, r, ip)
		if status != 0 {
			if status == http.StatusUnauthorized {
				LFSUnauthorized(w, msg)
				return
			}
			LFSError(w, status, msg)
			return
		}
		who = p
	}
	if ok, wait := h.allowRequest(who, ip); !ok {
		retryAfter(w, wait)
		LFSError(w, http.StatusTooManyRequests, "too many git requests; try again later")
		return
	}

	repo, err := h.o.Repos.Get(ctx, who, owner, name)
	var moved *repos.MovedError
	switch {
	case errors.As(err, &moved):
		n := moved.Name
		if strings.Contains(r.URL.Path, ".git"+lfsMarker) {
			n += ".git"
		}
		u := url.URL{Path: "/" + moved.Owner + "/" + n + lfsMarker + rest, RawQuery: r.URL.RawQuery}
		status := http.StatusMovedPermanently
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			status = http.StatusPermanentRedirect
		}
		http.Redirect(w, r, u.String(), status)
		return
	case errors.Is(err, repos.ErrNotFound):
		if who.UserID == uuid.Nil {
			LFSUnauthorized(w, "authentication required")
			return
		}
		LFSError(w, http.StatusNotFound, "repository not found")
		return
	case err != nil:
		h.o.Log.ErrorContext(ctx, "git lfs: resolve repository", "error", err)
		LFSError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tok != nil && tok.RepoID != repo.ID {
		LFSError(w, http.StatusForbidden, "the LFS token is not valid for this repository")
		return
	}
	h.o.LFS.ServeLFS(w, r, LFSRequest{Repo: repo, Caller: who, RemoteIP: ip, Path: rest, Token: tok})
}

// LFSUnauthorized asks the client for credentials.
func LFSUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("LFS-Authenticate", realm)
	w.Header().Set("WWW-Authenticate", realm)
	LFSError(w, http.StatusUnauthorized, msg)
}

// LFSError writes an LFS JSON error document, which git-lfs prints.
func LFSError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", lfsMediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Message string `json:"message"`
	}{msg})
}
