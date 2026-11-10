package chat

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// WaitProjection waits for the projector to finish its current event, including
// post-commit lifecycle effects. Call after observing the event's durable row;
// a flush request alone need not consume buffered provider events first.
func (c *Controller) WaitProjection(ctx context.Context) error {
	c.flushDeltas(ctx)
	return ctx.Err()
}

// SetNativeHistoryLoadAttemptLimit shortens the per-attempt provider load bound
// for tests and returns a restore function.
func SetNativeHistoryLoadAttemptLimit(limit time.Duration) (restore func()) {
	previous := nativeHistoryLoadAttemptLimit
	nativeHistoryLoadAttemptLimit = limit
	return func() { nativeHistoryLoadAttemptLimit = previous }
}

// SetRenderMeasureTimeout shortens how long publishing waits for heights, for
// tests, and returns a restore function.
func SetRenderMeasureTimeout(timeout time.Duration) (restore func()) {
	previous := renderMeasureTimeout
	renderMeasureTimeout = timeout
	return func() { renderMeasureTimeout = previous }
}

// WaitRenderMeasures waits for the background measures PublishRender started.
func (s *Service) WaitRenderMeasures() { s.renderMeasures.Wait() }

// SetDeltaFlushInterval changes how long streamed deltas may wait to be batched
// for controllers started afterwards, and returns a restore function. Zero writes
// every delta on its own, the way the projector worked before batching.
func SetDeltaFlushInterval(interval time.Duration) (restore func()) {
	previous := deltaFlushInterval
	deltaFlushInterval = interval
	return func() { deltaFlushInterval = previous }
}

// DeltaRunMaxBytes is the most delta text one batched write carries.
const DeltaRunMaxBytes = deltaRunMaxBytes

// SetChatViewWakeWait shortens how long opening a view waits for its wake.
func SetChatViewWakeWait(wait time.Duration) (restore func()) {
	previous := chatViewWakeWait
	chatViewWakeWait = wait
	return func() { chatViewWakeWait = previous }
}

// SetAfterFailedWakeRead installs a hook between reading the session and
// queueing a message after a failed wake.
func SetAfterFailedWakeRead(hook func()) (restore func()) {
	previous := afterFailedWakeRead
	afterFailedWakeRead = hook
	return func() { afterFailedWakeRead = previous }
}

// DeferWake records one failure of a wake on the retry schedule, the way each
// joiner of a failed wake does.
func (s *Service) DeferWake(id domain.SessionID) bool { return s.deferWake(id) }

// RunBackgroundWake starts a background wake the way a send (retry false) or the
// retry scan (retry true) does.
func (s *Service) RunBackgroundWake(id domain.SessionID, retry bool) { s.runBackgroundWake(id, retry) }

// WakeRetryState reports the pending retry's failure count and next due time.
func (s *Service) WakeRetryState(id domain.SessionID) (failures int, next time.Time, pending bool) {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	r, ok := s.wakeRetries[id]
	return r.failures, r.next, ok
}

// StartConfigOwnerIDs reports the provider ids the retained start config expects
// the session's controller owner to hold.
func StartConfigOwnerIDs(s *Service, id domain.SessionID) (agentSessionID, providerID string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, ok := s.startConfigs[domain.SessionConversationOwner(id)]
	return cfg.ExpectedControllerOwner.AgentSessionID, cfg.ExpectedControllerOwner.ProviderConversationID, ok
}

// HoldControllerGate takes the session's start/stop gate the way Start does and
// returns the release function.
func HoldControllerGate(s *Service, id domain.SessionID) (release func()) {
	g := s.controllerGate(domain.SessionConversationOwner(id))
	g <- struct{}{}
	return func() { <-g }
}

// SetFenceGOOSForTest swaps the OS chat launches are admitted for.
func SetFenceGOOSForTest(goos string) (restore func()) {
	prev := fenceGOOS
	fenceGOOS = goos
	return func() { fenceGOOS = prev }
}
