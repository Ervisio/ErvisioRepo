package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

// writeProblem writes an RFC 9457 error. detail must never contain secrets.
func writeProblem(w http.ResponseWriter, status int, detail string) {
	p := v1gen.Problem{Type: "about:blank", Title: http.StatusText(status), Status: status}
	if detail != "" {
		p.Detail = &detail
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

func problemHandler(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { writeProblem(w, status, "") }
}
