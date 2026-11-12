package chat

import (
	"context"
	"errors"
	"net"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TransientRetryPolicy is how a worker or orchestrator turn that failed on a
// transient network or API-availability error is retried without anyone typing
// "try again" (B682).
type TransientRetryPolicy struct {
	// Backoff is the wait before each attempt; its length caps the attempts. A
	// failed retry that fails transiently again counts as the next attempt.
	Backoff []time.Duration
	// OfflinePoll is how often connectivity is checked while the network is down.
	// Waiting for the network does not spend an attempt.
	OfflinePoll time.Duration
	// MaxOffline bounds the wait for the network before the turn is left failed.
	MaxOffline time.Duration
	// Online reports whether the provider's API is reachable now.
	Online func(context.Context, domain.AgentHarness) bool
}

// DefaultTransientRetryPolicy retries five times over about four minutes and
// waits up to two hours for a lost network to come back.
func DefaultTransientRetryPolicy() TransientRetryPolicy {
	return TransientRetryPolicy{
		Backoff:     []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute},
		OfflinePoll: 10 * time.Second,
		MaxOffline:  2 * time.Hour,
		Online:      providerReachable,
	}
}

// providerAPIHosts are the endpoints whose reachability stands for "online". Any
// one answering is enough: the probe asks whether DNS and routing work again,
// not whether this provider is healthy, which the retry itself finds out.
var providerAPIHosts = map[domain.AgentHarness][]string{
	domain.HarnessClaudeCode: {"api.anthropic.com:443"},
	domain.HarnessCodex:      {"chatgpt.com:443", "api.openai.com:443"},
}

var fallbackAPIHosts = []string{"api.anthropic.com:443", "api.openai.com:443"}

// providerReachable dials the harness's API host (no request is sent).
func providerReachable(ctx context.Context, harness domain.AgentHarness) bool {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	for _, host := range append(append([]string(nil), providerAPIHosts[harness]...), fallbackAPIHosts...) {
		if conn, err := dialer.DialContext(ctx, "tcp", host); err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

// transientRetryState is the one pending automatic retry of a controller.
// Guarded by Controller.mu.
type transientRetryState struct {
	policy       *TransientRetryPolicy
	timer        *time.Timer
	turnID       string
	attempt      int
	nextAt       time.Time
	offline      bool
	offlineSince time.Time
}

// TransientRetry reports the automatic retry this controller has scheduled for a
// failed turn, for the snapshot to overlay on that turn.
func (c *Controller) TransientRetry() (domain.ConversationTurnAutoRetry, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.transientRetry
	if r.turnID == "" || r.policy == nil {
		return domain.ConversationTurnAutoRetry{}, "", false
	}
	return domain.ConversationTurnAutoRetry{
		Attempt: r.attempt, MaxAttempts: len(r.policy.Backoff), NextAt: r.nextAt, Offline: r.offline,
	}, r.turnID, true
}

// withTransientRetry overlays a scheduled automatic retry on its failed turn.
func withTransientRetry(turns []domain.ConversationTurn, controller *Controller) []domain.ConversationTurn {
	retry, turnID, ok := controller.TransientRetry()
	if !ok {
		return turns
	}
	for i := range turns {
		if turns[i].ID == turnID && turns[i].State == domain.TurnStateFailed {
			out := append([]domain.ConversationTurn(nil), turns...)
			out[i].AutoRetry = &retry
			return out
		}
	}
	return turns
}

// armTransientRetry schedules a retry of a turn that just failed on a transient
// error, and reports whether it did. Only sessions with nobody at the keyboard
// retry on their own, mirroring usage-limit auto-resume; an interactive chat
// keeps its Retry button.
func (c *Controller) armTransientRetry(ctx context.Context, event ports.ChatEvent) bool {
	c.mu.Lock()
	policy, closed := c.transientRetry.policy, c.explicitClose
	c.mu.Unlock()
	if policy == nil || closed || len(policy.Backoff) == 0 || !unattendedSessionKind(c.kind) || c.reviewID != "" {
		return false
	}
	if event.Err == nil || event.ProviderTurnID == "" || errors.Is(event.Err, ports.ErrChatAuthRequired) ||
		!domain.IsTransientTurnError(event.Err.Error()) {
		return false
	}
	turn, err := c.store.TurnByProviderID(ctx, c.conversation.ID, event.ProviderTurnID)
	if err != nil || turn.State != domain.TurnStateFailed {
		return false
	}
	attempt := c.retryChainDepth(ctx, turn, len(policy.Backoff)) + 1
	if attempt > len(policy.Backoff) {
		c.log.Warn("chat turn failed on a transient error; automatic retries exhausted",
			"session", c.sessionID, "turn", turn.ID, "attempts", len(policy.Backoff), "error", event.Err)
		return false
	}
	delay := policy.Backoff[attempt-1]
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.explicitClose {
		return false
	}
	c.stopTransientRetryLocked()
	c.transientRetry.turnID, c.transientRetry.attempt = turn.ID, attempt
	c.scheduleTransientRetryLocked(delay)
	c.log.Info("chat turn failed on a transient error; retrying automatically",
		"session", c.sessionID, "turn", turn.ID, "attempt", attempt, "in", delay, "error", event.Err)
	return true
}

// retryChainDepth counts how many automatic or manual retries led to turn,
// stopping at limit. It is read from durable rows, so the cap survives a
// replaced controller.
func (c *Controller) retryChainDepth(ctx context.Context, turn domain.ConversationTurn, limit int) int {
	depth := 0
	for turn.RetryOfTurnID != "" && depth < limit {
		source, err := c.store.TurnByID(ctx, turn.RetryOfTurnID)
		if err != nil {
			break
		}
		depth++
		turn = source
	}
	return depth
}

func (c *Controller) scheduleTransientRetryLocked(delay time.Duration) {
	turnID := c.transientRetry.turnID
	c.transientRetry.nextAt = c.now().Add(delay)
	c.transientRetry.timer = time.AfterFunc(delay, func() { c.fireTransientRetry(turnID) })
}

// stopTransientRetryLocked cancels the pending retry; Close and Terminate call
// it. A newer turn needs no hook: the retry refuses once it is not the latest.
func (c *Controller) stopTransientRetryLocked() {
	if c.transientRetry.timer != nil {
		c.transientRetry.timer.Stop()
	}
	policy := c.transientRetry.policy
	c.transientRetry = transientRetryState{policy: policy}
}

// cancelTransientRetry drops the pending retry for turnID, if it is still the one.
func (c *Controller) cancelTransientRetry(turnID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.transientRetry.turnID == turnID {
		c.stopTransientRetryLocked()
	}
}

func (c *Controller) fireTransientRetry(turnID string) {
	ctx := context.Background()
	c.mu.Lock()
	r := c.transientRetry
	closed := c.explicitClose
	c.mu.Unlock()
	if closed || r.turnID != turnID || r.policy == nil {
		return
	}
	if r.policy.Online != nil && !r.policy.Online(ctx, c.harness) {
		c.waitForNetwork(turnID)
		return
	}
	_, err := c.retryTransientTurn(ctx, turnID)
	switch {
	case err == nil:
		c.cancelTransientRetry(turnID)
	case errors.Is(err, ErrTurnRunning) && !c.busy():
		// Another send or a queue drain held the lock for a moment; try again soon.
		c.rescheduleTransientRetry(turnID, r.policy.OfflinePoll)
	default:
		// Superseded (a newer turn ran) or no longer retryable: leave the turn as
		// it failed so its error and Retry button show.
		c.log.Info("automatic retry of a transient failure dropped", "session", c.sessionID, "turn", turnID, "reason", err)
		c.cancelTransientRetry(turnID)
		if !c.busy() {
			// The session reported active while the retry was pending.
			c.reportActivity(ctx, domain.ActivityIdle, "chat.turn.retry_dropped", c.now())
		}
	}
}

// waitForNetwork polls connectivity without spending an attempt, up to MaxOffline.
func (c *Controller) waitForNetwork(turnID string) {
	c.mu.Lock()
	if c.transientRetry.turnID != turnID || c.explicitClose {
		c.mu.Unlock()
		return
	}
	now := c.now()
	if !c.transientRetry.offline {
		c.transientRetry.offline, c.transientRetry.offlineSince = true, now
	}
	if now.Sub(c.transientRetry.offlineSince) >= c.transientRetry.policy.MaxOffline {
		c.stopTransientRetryLocked()
		c.mu.Unlock()
		c.log.Warn("network did not come back; automatic retry abandoned", "session", c.sessionID, "turn", turnID)
		c.reportActivity(context.Background(), domain.ActivityIdle, "chat.turn.retry_offline", now)
		return
	}
	c.scheduleTransientRetryLocked(c.transientRetry.policy.OfflinePoll)
	c.mu.Unlock()
}

func (c *Controller) rescheduleTransientRetry(turnID string, delay time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.transientRetry.turnID == turnID && !c.explicitClose {
		c.scheduleTransientRetryLocked(delay)
	}
}

// retryTransientTurn re-sends the failed turn's prompt through RetryTurn's
// durable, idempotent path, replaying automation-origin prompts like the
// usage-limit auto-resume does. It refuses when a newer turn has run since.
func (c *Controller) retryTransientTurn(ctx context.Context, turnID string) (domain.ConversationTurn, error) {
	if !c.sendMu.TryLock() {
		return domain.ConversationTurn{}, ErrTurnRunning
	}
	defer c.sendMu.Unlock()
	if store, ok := c.store.(usageLimitStore); ok {
		latest, err := store.LatestAutoResumeTurn(ctx, c.conversation.ID)
		if err != nil {
			return domain.ConversationTurn{}, err
		}
		if latest.ID != turnID {
			return domain.ConversationTurn{}, ErrTurnNotRetryable
		}
	}
	return c.retryTurnLocked(ctx, turnID, false, true)
}

// SetTransientRetryPolicy turns on automatic retries of turns that failed on a
// transient error; nil leaves them failed until someone retries.
func (s *Service) SetTransientRetryPolicy(policy *TransientRetryPolicy) {
	s.transientRetry.Store(policy)
}

func (s *Service) transientRetryPolicy() *TransientRetryPolicy {
	return s.transientRetry.Load()
}
