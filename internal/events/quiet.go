package events

import "context"

type quietKey struct{}

// Quiet returns a context under which Emit and Bus.Publish record nothing.
// Bulk writes that must not fan out to webhooks, notifications and the
// other consumers (the import of a GitHub repository) run under it.
func Quiet(ctx context.Context) context.Context { return context.WithValue(ctx, quietKey{}, true) }

// IsQuiet reports whether ctx was returned by Quiet.
func IsQuiet(ctx context.Context) bool {
	q, _ := ctx.Value(quietKey{}).(bool)
	return q
}
