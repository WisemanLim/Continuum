package agentproto

import "context"

type progressKey struct{}

// WithProgress attaches a progress callback (bytes done) to a context: on the Controller side the hub
// registers it for the command it sends; on the agent side the session installs one that streams Progress.
func WithProgress(ctx context.Context, fn func(bytes int64)) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

// ProgressFrom returns the callback, or nil.
func ProgressFrom(ctx context.Context) func(bytes int64) {
	fn, _ := ctx.Value(progressKey{}).(func(int64))
	return fn
}
