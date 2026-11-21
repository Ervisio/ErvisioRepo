package httpapi

import (
	"context"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

// browseOperations are the code page reads that must not wait for the
// hydration of a cold repository: they answer from the cold sources or 503
// + Retry-After after a short wait (gitbrowse.Interactive), and the page
// follows the hydration progress.
var browseOperations = map[string]bool{
	"ResolveRef": true, "GetTree": true, "GetBlob": true, "GetReadme": true, "GetBlame": true,
	"ListCommits": true, "ListContributors": true, "ListLanguages": true, "GetCommit": true,
	"ListBranches": true, "ListTags": true, "CompareRefs": true, "SearchCode": true,
}

func interactiveBrowse(f v1gen.StrictHandlerFunc, operationID string) v1gen.StrictHandlerFunc {
	if !browseOperations[operationID] {
		return f
	}
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		return f(gitbrowse.Interactive(ctx), w, r, request)
	}
}
