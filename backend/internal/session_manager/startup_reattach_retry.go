package sessionmanager

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// defaultStartupChatRetryDelays spaces the reattach attempts for a Chat whose
// startup check failed transiently. Its provider host is still running, so a
// later attempt reconnects to it: no provider is launched and no history moves.
var defaultStartupChatRetryDelays = []time.Duration{
	2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second,
	30 * time.Second, time.Minute, time.Minute,
}

// defaultStartupChatRetryMaxDelay caps the backoff once the schedule is spent.
// The retries never stop on a transient failure: a reattach that gave up left
// the session without a controller until the next daemon restart (B810).
const defaultStartupChatRetryMaxDelay = 5 * time.Minute

// retryableStartupChatCheck reports failures a later reattach can succeed past:
// the check's deadline (a busy writer at startup) and an inconclusive host owner
// (a replaced daemon has not detached yet). Anything else needs the user.
func retryableStartupChatCheck(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ports.ErrChatRecoveryInconclusive)
}

// startupChatRetryDelay is the wait before the given retry: the schedule, then
// doubling up to the cap.
func (m *Manager) startupChatRetryDelay(attempt int, previous time.Duration) time.Duration {
	if attempt < len(m.startupChatRetryDelays) {
		return m.startupChatRetryDelays[attempt]
	}
	limit := m.startupChatRetryMaxDelay
	if limit <= 0 {
		limit = defaultStartupChatRetryMaxDelay
	}
	if next := previous * 2; next > 0 && next < limit {
		return next
	}
	return limit
}

// retryStartupChatChecks reattaches Chats whose startup check failed
// transiently. Without it, a live host reads "stopped" until the user clicks
// Resume, even though the next attempt would reconnect immediately. The
// sessions stay "checking" through the schedule. When it is spent the session
// reads "unavailable" so Resume is offered and its orchestrator is told, but the
// reattach keeps trying with backoff until it succeeds, the session no longer
// wants it, or the failure turns permanent.
func (m *Manager) retryStartupChatChecks(ctx context.Context, ids []domain.SessionID) {
	var delay time.Duration
	for attempt := 0; len(ids) > 0; attempt++ {
		delay = m.startupChatRetryDelay(attempt, delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		report := attempt == len(m.startupChatRetryDelays)-1
		remaining := ids[:0]
		for _, id := range ids {
			if m.retryStartupChatCheck(ctx, id, report) {
				remaining = append(remaining, id)
			}
		}
		ids = remaining
	}
}

// retryStartupChatCheck makes one reattach attempt and reports whether the
// session still needs another one. Every other outcome settles its readiness.
// report marks the attempt that ends the schedule: a failure there is surfaced
// once while the retries go on.
func (m *Manager) retryStartupChatCheck(ctx context.Context, id domain.SessionID, report bool) bool {
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		if ctx.Err() == nil {
			if report {
				m.finishStatusRecovery(ctx, domain.SessionRecord{ID: id}, err)
			}
			return true
		}
		m.finishStatusRecovery(ctx, domain.SessionRecord{ID: id}, err)
		return false
	}
	if !found || rec.IsTerminated || rec.HibernatedAt != nil ||
		domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat || m.chat == nil ||
		m.chat.HasLiveChatController(id) {
		// Gone, asleep, or already reattached (for example by Resume).
		m.finishStatusRecovery(ctx, rec, nil)
		return false
	}
	if err := m.beginAgentOperation(ctx, id, agentOperationReconcile); err != nil {
		// A user operation owns the session now; look again after it ends.
		if errors.Is(err, errAgentOperationInProgress) && ctx.Err() == nil {
			return true
		}
		m.finishStatusRecovery(ctx, rec, err)
		return false
	}
	err = func() error {
		defer m.endAgentOperation(id, agentOperationReconcile)
		recoveryCtx, cancel := context.WithTimeout(ctx, m.statusVerificationLimit)
		defer cancel()
		return m.checkSessionHealth(recoveryCtx, rec)
	}()
	if errors.Is(err, errStartupChatExited) {
		m.finishStatusRecovery(ctx, rec, nil)
		m.resumeStartupChats(ctx, []domain.SessionID{id})
		return false
	}
	if err == nil {
		m.finishStatusRecovery(ctx, rec, nil)
		m.logger.Info("reconcile: chat reattached on retry", "sessionID", id)
		return false
	}
	retry := retryableStartupChatCheck(err) && ctx.Err() == nil
	if !retry || report {
		m.finishStatusRecovery(ctx, rec, err)
		m.reportStartupReattachFailed(ctx, rec, err)
	}
	m.logger.Warn("reconcile: chat reattach retry failed", "sessionID", id, "retrying", retry, "error", err)
	return retry
}
