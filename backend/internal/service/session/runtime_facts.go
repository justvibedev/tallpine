package session

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// RuntimeFacts is the one source of the live, non-persisted facts every REST
// (and so mobile) session response carries; SSE frames carry raw rows and
// clients refetch over REST: a quota pause and the background tasks a session
// waits on.
type RuntimeFacts interface {
	PausedUntil(ctx context.Context, id domain.SessionID) (time.Time, bool)
	ReauthRequired(ctx context.Context, id domain.SessionID) bool
	Waiting(id domain.SessionID) []string
	Running(id domain.SessionID) []bgtasks.Info
}

// UsageLimitSource reports a session paused by its provider's usage limit.
type UsageLimitSource interface {
	UsageLimitPausedUntil(context.Context, domain.SessionID) (time.Time, bool)
}

// ReauthSource reports a session whose provider refused its login. The chat
// service implements it beside UsageLimitSource; a usage source without it
// reports no reauth.
type ReauthSource interface {
	ReauthRequired(context.Context, domain.SessionID) bool
}

// BackgroundTaskSource names the background tasks a session waits on or runs.
type BackgroundTaskSource interface {
	Waiting(domain.SessionID) []string
	Running(domain.SessionID) []bgtasks.Info
}

type runtimeFacts struct {
	usage UsageLimitSource
	tasks BackgroundTaskSource
}

// NewRuntimeFacts composes RuntimeFacts from optional sources; a nil source
// leaves its facts empty.
func NewRuntimeFacts(usage UsageLimitSource, tasks BackgroundTaskSource) RuntimeFacts {
	return runtimeFacts{usage: usage, tasks: tasks}
}

func (f runtimeFacts) PausedUntil(ctx context.Context, id domain.SessionID) (time.Time, bool) {
	if f.usage == nil {
		return time.Time{}, false
	}
	return f.usage.UsageLimitPausedUntil(ctx, id)
}

func (f runtimeFacts) ReauthRequired(ctx context.Context, id domain.SessionID) bool {
	source, ok := f.usage.(ReauthSource)
	return ok && source.ReauthRequired(ctx, id)
}

func (f runtimeFacts) Waiting(id domain.SessionID) []string {
	if f.tasks == nil {
		return nil
	}
	return f.tasks.Waiting(id)
}

func (f runtimeFacts) Running(id domain.SessionID) []bgtasks.Info {
	if f.tasks == nil {
		return nil
	}
	return f.tasks.Running(id)
}
