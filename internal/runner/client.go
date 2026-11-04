package runner

import (
	"context"
	"crypto/tls"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1/runnerv1connect"
)

// APIPath is where the server mounts the runner service.
const APIPath = "/api/actions/runner"

// NewClient returns a RunnerService client for the server at serverURL.
// credential (erun_…) is sent as a bearer token; it is empty for Register,
// which authenticates with the registration token in the request body.
func NewClient(serverURL, credential, version string, insecureTLS bool) runnerv1connect.RunnerServiceClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit opt-in for self-signed test instances
	}
	// No client timeout: every call carries its own deadline (FetchTask
	// long-polls).
	httpClient := &http.Client{Transport: transport}
	userAgent := "ervisio-runner/" + version
	auth := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("User-Agent", userAgent)
			if credential != "" {
				req.Header().Set("Authorization", "Bearer "+credential)
			}
			return next(ctx, req)
		}
	})
	return runnerv1connect.NewRunnerServiceClient(
		httpClient,
		strings.TrimRight(serverURL, "/")+APIPath,
		connect.WithInterceptors(auth),
		connect.WithSendGzip(),
		connect.WithCompressMinBytes(1024),
	)
}

// isAuthError reports whether err means the credential was refused.
func isAuthError(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated, connect.CodePermissionDenied:
		return true
	}
	return false
}
