package ports

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type mergeRequesterKey struct{}

// WithMergeRequester records which session made an explicit merge request (the
// "pr land" API). Lifecycle reactions it triggers, such as end-on-merge
// auto-close, use it to skip notices addressed to that same session (B560).
// An empty id (a human or unscoped caller) leaves ctx unchanged.
func WithMergeRequester(ctx context.Context, id domain.SessionID) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, mergeRequesterKey{}, id)
}

// MergeRequester is the session that made the merge request ctx belongs to, or
// "" when there is none.
func MergeRequester(ctx context.Context) domain.SessionID {
	id, _ := ctx.Value(mergeRequesterKey{}).(domain.SessionID)
	return id
}
