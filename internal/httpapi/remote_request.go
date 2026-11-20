package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

const (
	maxRemoteActionBody = 128 << 10
	maxRemotePollBody   = 1 << 20
)

// Decode repository metadata strictly rather than accepting case aliases or duplicate keys.
type remotePollBody struct {
	v1gen.ErvisioCodeRelayPoll
	Projects []struct {
		v1gen.ErvisioCodeRemoteProjectPublication
		Repository remoteRepositoryJSON `json:"repository"`
	} `json:"projects"`
}

type remoteRepositoryJSON struct{}

var (
	remoteRepositoryName       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
	errInvalidRemoteRepository = errors.New("invalid Remote repository metadata")
)

func (*remoteRepositoryJSON) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return errInvalidRemoteRepository
	}
	fields := make(map[string]string, 3)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return errInvalidRemoteRepository
		}
		name, ok := key.(string)
		if !ok || len(fields) == 3 {
			return errInvalidRemoteRepository
		}
		var value string
		if _, duplicate := fields[name]; duplicate || decoder.Decode(&value) != nil {
			return errInvalidRemoteRepository
		}
		fields[name] = value
	}
	if !v1gen.ErvisioCodeRemoteRepositoryProvider(fields["provider"]).Valid() ||
		!v1gen.ErvisioCodeRemoteRepositoryCheckout(fields["checkout"]).Valid() ||
		!remoteRepositoryName.MatchString(fields["name"]) {
		return errInvalidRemoteRepository
	}
	return nil
}

func remoteRequestHeaders(publicURL string) func(http.Handler) http.Handler {
	https := hubOrigin(publicURL) != ""
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if sharingRoute(r.URL.Path, r.Method) || collaborationCheckRoute(r.URL.Path, r.Method) {
				w.Header().Set("Cache-Control", "no-store")
				if !https {
					writeProblem(w, http.StatusForbidden, "ErvisioCode sharing requires a public HTTPS URL")
					return
				}
			}
			switch r.URL.Path {
			case hubSessionPath:
				w.Header().Set("Cache-Control", "no-store")
			case remoteSessionPath, remoteProjectPath, remoteCatalogPath, remoteRelayPath:
				w.Header().Set("Cache-Control", "no-store")
				if !https {
					writeProblem(w, http.StatusForbidden, "ErvisioCode Remote requires a public HTTPS URL")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func remoteRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sharingRoute(r.URL.Path, r.Method) && (r.Method == http.MethodDelete || strings.HasSuffix(r.URL.Path, "/reject")) && r.ContentLength != 0 {
			writeProblem(w, http.StatusBadRequest, "this share action does not accept a request body")
			return
		}
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		var body any
		var limit int64
		switch r.URL.Path {
		case remoteSessionPath:
			body, limit = new(v1gen.ErvisioCodeRemoteAction), maxRemoteActionBody
		case remoteProjectPath:
			body, limit = new(v1gen.ErvisioCodeRemoteCreate), maxRemoteActionBody
		case remoteRelayPath:
			body, limit = new(remotePollBody), maxRemotePollBody
		default:
			limit = maxRemoteActionBody
			if collaborationCheckRoute(r.URL.Path, r.Method) {
				body = new(v1gen.ErvisioCodeCollaborationCheck)
			} else {
				body = sharingPostBody(r.URL.Path)
			}
			if body == nil {
				next.ServeHTTP(w, r)
				return
			}
		}
		if status := validateRemoteBody(w, r, body, limit); status != 0 {
			writeProblem(w, status, "expected a bounded ErvisioCode Remote JSON object")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sharingPostBody(path string) any {
	if !sharingRoute(path, http.MethodPost) {
		return nil
	}
	switch path {
	case remoteSharePath:
		return new(v1gen.ErvisioCodeShareCreate)
	case remoteSharePath + "/accept":
		return new(v1gen.ErvisioCodeShareAccept)
	}
	switch {
	case strings.HasSuffix(path, "/accept"):
		return new(struct{})
	case strings.HasSuffix(path, "/collaboration"):
		return new(v1gen.ErvisioCodeCollaborationAction)
	case strings.HasSuffix(path, "/proposals"):
		return new(v1gen.ErvisioCodeProposalCreate)
	case strings.HasSuffix(path, "/review"):
		return new(v1gen.ErvisioCodeProposalReview)
	default:
		return nil
	}
}

func validateRemoteBody(w http.ResponseWriter, r *http.Request, body any, limit int64) int {
	contentTypes := r.Header.Values("Content-Type")
	if len(contentTypes) != 1 {
		return http.StatusUnsupportedMediaType
	}
	mediaType, _, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || mediaType != "application/json" {
		return http.StatusUnsupportedMediaType
	}
	reader := http.MaxBytesReader(w, r.Body, limit)
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	if err != nil || !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) || !json.Valid(data) {
		return http.StatusBadRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		return http.StatusBadRequest
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	return 0
}
