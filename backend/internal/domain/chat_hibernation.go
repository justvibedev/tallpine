package domain

import "time"

// Idle Chat hibernation defaults. A worker that has been quiet for 20 minutes
// is very likely waiting on review or on its orchestrator; an orchestrator
// coordinates many workers and pays more for a cold prompt cache, so it waits
// an hour.
const (
	DefaultChatHibernationWorkerIdle       = 20 * time.Minute
	DefaultChatHibernationOrchestratorIdle = 60 * time.Minute
	// MinChatHibernationIdle is the shortest threshold a user can choose.
	MinChatHibernationIdle = time.Minute
	// MaxChatHibernationIdle bounds the threshold to something a person means.
	MaxChatHibernationIdle = 7 * 24 * time.Hour
)

// ChatHibernationPolicy is the user's idle Chat hibernation preference:
// whether idle Chat sessions release their agent processes at all, and how
// long each kind of session must be quiet first.
type ChatHibernationPolicy struct {
	Enabled          bool
	WorkerIdle       time.Duration
	OrchestratorIdle time.Duration
}

// DefaultChatHibernationPolicy is the policy of a fresh install.
func DefaultChatHibernationPolicy() ChatHibernationPolicy {
	return ChatHibernationPolicy{
		Enabled:          true,
		WorkerIdle:       DefaultChatHibernationWorkerIdle,
		OrchestratorIdle: DefaultChatHibernationOrchestratorIdle,
	}
}

// IdleFor is how long a session of kind must be quiet before it may sleep.
func (p ChatHibernationPolicy) IdleFor(kind SessionKind) time.Duration {
	idle := p.WorkerIdle
	if kind == KindOrchestrator {
		idle = p.OrchestratorIdle
	}
	return max(idle, MinChatHibernationIdle)
}

// ValidChatHibernationIdle reports whether d is an acceptable threshold.
func ValidChatHibernationIdle(d time.Duration) bool {
	return d >= MinChatHibernationIdle && d <= MaxChatHibernationIdle && d%time.Second == 0
}
