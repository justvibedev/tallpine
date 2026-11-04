package requestscope

import "context"

type tokenlessKey struct{}

// WithTokenless marks a request the loopback listener served without its
// install token, through one of its closed exemptions (preview files and
// hosts, render documents).
func WithTokenless(ctx context.Context) context.Context {
	return context.WithValue(ctx, tokenlessKey{}, true)
}

// IsTokenless reports whether the request was served without the install
// token. Handlers serving workspace bytes use it to keep hidden files private.
func IsTokenless(ctx context.Context) bool {
	value, _ := ctx.Value(tokenlessKey{}).(bool)
	return value
}
