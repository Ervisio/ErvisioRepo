package blobsvc

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// maxTwirpBody bounds one Twirp request.
const maxTwirpBody = 1 << 20

// Int64 is a protobuf int64 in JSON: it accepts a number or a string and
// is written as a string, as protobuf JSON does.
type Int64 int64

func (n *Int64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("int64 %q: %w", s, err)
	}
	*n = Int64(v)
	return nil
}

func (n Int64) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(n), 10) + `"`), nil
}

// Decode reads a Twirp JSON request into v. It writes the Twirp error and
// returns false when the request is not valid.
func Decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
		TwirpError(w, "bad_route", "only application/json is supported")
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxTwirpBody+1))
	if err != nil || len(body) > maxTwirpBody {
		TwirpError(w, "malformed", "request body unreadable or too large")
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		TwirpError(w, "malformed", "invalid JSON request")
		return false
	}
	return true
}

// Reply writes a Twirp JSON response.
func Reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

var twirpStatus = map[string]int{
	"bad_route":           http.StatusNotFound,
	"malformed":           http.StatusBadRequest,
	"invalid_argument":    http.StatusBadRequest,
	"unauthenticated":     http.StatusUnauthorized,
	"permission_denied":   http.StatusForbidden,
	"not_found":           http.StatusNotFound,
	"already_exists":      http.StatusConflict,
	"failed_precondition": http.StatusPreconditionFailed,
	"resource_exhausted":  http.StatusTooManyRequests,
	"internal":            http.StatusInternalServerError,
}

// TwirpError writes a Twirp error with code (a Twirp error code).
func TwirpError(w http.ResponseWriter, code, msg string) {
	status, ok := twirpStatus[code]
	if !ok {
		code, status = "internal", http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "msg": msg})
}

// Timestamp converts t for sqlc parameters.
func Timestamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// RFC3339 formats a stored timestamp for a Twirp response.
func RFC3339(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339Nano)
}
