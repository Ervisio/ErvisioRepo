package actions

import (
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1/runnerv1connect"
)

// RunnerPathPrefix is where the runner protocol is served.
const RunnerPathPrefix = "/api/actions/runner"

// maxRunnerMessage bounds one runner request (log batches, outputs).
const maxRunnerMessage = 32 << 20

// RunnerHandler serves the ConnectRPC runner service under
// RunnerPathPrefix; mount it at RunnerPathPrefix + "/". Its calls are long
// polls: do not wrap it in a request timeout.
func (s *Service) RunnerHandler() http.Handler {
	p, h := runnerv1connect.NewRunnerServiceHandler(s.Runners(), connect.WithReadMaxBytes(maxRunnerMessage))
	mux := http.NewServeMux()
	mux.Handle(p, h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.Path, RunnerPathPrefix)
		if !ok || !strings.HasPrefix(rest, "/") {
			http.NotFound(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = rest
		r2.URL.RawPath = ""
		mux.ServeHTTP(w, r2)
	})
}
