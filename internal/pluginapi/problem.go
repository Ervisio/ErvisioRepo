package pluginapi

import (
	"encoding/json"
	"net/http"
)

// problem is an RFC 9457 body, the same shape as the REST API's.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Errors lists the invalid settings of a 422.
	Errors []fieldError `json:"errors,omitempty"`
}

// writeProblem writes an application/problem+json error. detail must never
// contain secrets.
func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// writeValidation writes a 422 with the per-key errors.
func writeValidation(w http.ResponseWriter, detail string, errs []fieldError) {
	status := http.StatusUnprocessableEntity
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{Type: "about:blank", Title: http.StatusText(status), Status: status,
		Detail: detail, Errors: errs})
}
