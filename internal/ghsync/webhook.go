package ghsync

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// maxWebhookBody is the largest payload GitHub sends (25 MB).
const maxWebhookBody = 25 << 20

// verifySignature checks X-Hub-Signature-256 ("sha256=<hex HMAC>") in
// constant time.
func verifySignature(secret, body []byte, header string) bool {
	hexSig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// pushEvent is the part of a GitHub push event the sync needs.
type pushEvent struct {
	Repository struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
			Name  string `json:"name"`
		} `json:"owner"`
	} `json:"repository"`
}

// WebhookError is a refused delivery; Status is the HTTP status and the
// message is safe to answer.
type WebhookError struct {
	Status int
	Msg    string
}

func (e *WebhookError) Error() string { return e.Msg }

// Webhook handles one delivery to the webhook URL of a connection. A valid
// push schedules a run of the mirror and sync links following the pushed
// repository; ping and other events are acknowledged. It returns the
// status to answer, or a *WebhookError.
func (s *Service) Webhook(r *http.Request, connID uuid.UUID) (int, error) {
	ctx := r.Context()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody+1))
	if err != nil {
		return 0, &WebhookError{http.StatusBadRequest, "cannot read the payload"}
	}
	if len(body) > maxWebhookBody {
		return 0, &WebhookError{http.StatusRequestEntityTooLarge, "payload too large"}
	}
	unknown := &WebhookError{http.StatusNotFound, "unknown webhook"}
	row, err := s.q.GetGitHubConnection(ctx, connID)
	if noRows(err) {
		return 0, unknown
	}
	if err != nil {
		return 0, fmt.Errorf("get GitHub connection: %w", err)
	}
	if !s.seal.ok() {
		return 0, ErrNoMasterKey
	}
	secret, err := s.seal.open(row.GithubConnection.WebhookSecret, connID, purposeWebhook)
	if err != nil {
		return 0, fmt.Errorf("open webhook secret: %w", err)
	}
	if !verifySignature(secret, body, r.Header.Get("X-Hub-Signature-256")) {
		s.o.Log.WarnContext(ctx, "github webhook: bad signature", "connection", connID)
		return 0, &WebhookError{http.StatusUnauthorized, "invalid signature"}
	}
	switch r.Header.Get("X-GitHub-Event") {
	case "push":
	default: // ping and everything else
		return http.StatusNoContent, nil
	}
	var ev pushEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return 0, &WebhookError{http.StatusBadRequest, "invalid push payload"}
	}
	owner := ev.Repository.Owner.Login
	if owner == "" {
		owner = ev.Repository.Owner.Name
	}
	if checkRemote(owner, ev.Repository.Name) != nil {
		return http.StatusNoContent, nil
	}
	links, err := s.q.ListGitHubSyncLinksByRemote(ctx, dbgen.ListGitHubSyncLinksByRemoteParams{ConnectionID: connID, RemoteOwner: owner, RemoteName: ev.Repository.Name})
	if err != nil {
		return 0, fmt.Errorf("list sync links: %w", err)
	}
	n := 0
	for _, l := range links {
		if l.Mode != ModeMirror && l.Mode != ModeSync {
			continue
		}
		if err := s.enqueue(ctx, RunArgs{RepoID: l.RepoID}); err != nil {
			return 0, fmt.Errorf("enqueue github sync: %w", err)
		}
		n++
	}
	if n == 0 {
		return http.StatusNoContent, nil
	}
	return http.StatusAccepted, nil
}

// IsWebhookError returns the refusal of a delivery.
func IsWebhookError(err error) (*WebhookError, bool) {
	var we *WebhookError
	ok := errors.As(err, &we)
	return we, ok
}
