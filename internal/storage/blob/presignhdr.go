package blob

import "context"

// ResponseHeaders are the headers a presigned GET asks the backend to send
// back instead of the stored ones, so a direct download keeps the file name
// and media type the server would have served.
type ResponseHeaders struct {
	ContentType        string
	ContentDisposition string
}

type responseHeadersKey struct{}

// WithResponseHeaders returns ctx carrying h for the next PresignGet calls.
func WithResponseHeaders(ctx context.Context, h ResponseHeaders) context.Context {
	return context.WithValue(ctx, responseHeadersKey{}, h)
}

// ResponseHeadersFrom returns the headers set by WithResponseHeaders.
func ResponseHeadersFrom(ctx context.Context) (ResponseHeaders, bool) {
	h, ok := ctx.Value(responseHeadersKey{}).(ResponseHeaders)
	return h, ok
}
