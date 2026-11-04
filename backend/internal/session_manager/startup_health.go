package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// checkSessionHealth observes existing controllers. Startup must never create a
// provider, restore a worktree, or replay a task to discover whether it is alive.
func (m *Manager) checkSessionHealth(ctx context.Context, rec domain.SessionRecord) error {
	if rec.ProvisionState.WithDefault() != domain.SessionProvisionReady {
		return nil
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	if legacyInterruptedClientSetup(rec) {
		settled, current, err := m.rollbackLegacyClientSetup(ctx, rec)
		if err != nil || settled {
			return err
		}
		rec = current
	}
	// Preserve cleanup of interrupted spawns that never committed a workspace.
	// Their client request IDs must remain retryable after a daemon crash.
	if rec.Metadata.WorkspacePath == "" ||
		(rec.Metadata.Branch == "" && projectKindForSession(project, rec.ProjectID) != domain.ProjectKindScratch) {
		m.rollbackSpawnSeedRow(ctx, rec.ID)
		return nil
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if m.chat == nil {
			return ports.ErrChatUnsupported
		}
		if m.chat.HasLiveChatController(rec.ID) {
			return nil
		}
		if rec.Metadata.ProviderConversationID == "" {
			return fmt.Errorf("check session %s: %w", rec.ID, ErrIncompleteHandle)
		}
		_, err = m.resumeChatController(ctx, "check session", rec, project,
			workspaceInfo(rec), false, true, "", domain.SessionInterfaceTransitionHistoryStrict)
		if errors.Is(err, ports.ErrChatHostNotRunning) {
			// Only an agent that was live when the previous daemon went down
			// lost its host involuntarily. One that had already exited (the
			// user's Exit Agent, a finished task) stays stopped, unless an
			// earlier startup already recorded it as lost.
			involuntary := rec.Activity.State != domain.ActivityExited ||
				m.agentExitReason(ctx, rec.ID) == domain.AgentExitHostLost
			if err := m.recordAgentExited(ctx, rec); err != nil {
				return err
			}
			if !involuntary {
				return nil
			}
			m.setAgentExitReason(ctx, rec.ID, domain.AgentExitHostLost)
			return errStartupChatExited
		}
		return err
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		return m.recordAgentExited(ctx, rec)
	}
	alive, err := m.runtime.IsAlive(ctx, handle)
	if err != nil {
		return fmt.Errorf("check session %s: %w", rec.ID, err)
	}
	if alive {
		// The start hook may fail without stopping the TUI. Keep a live runtime
		// intact; its unverified identity will not qualify for native restore.
		return nil
	}
	return m.recordAgentExited(ctx, rec)
}

// errStartupChatExited reports a Chat whose provider host did not survive the
// restart (an app quit takes it down with the daemon). The check recorded it
// exited; the caller resumes it, since the user never stopped it.
var errStartupChatExited = errors.New("chat host did not survive restart")

// defaultStartupChatResumeWorkers bounds how many Chats resume at once after a
// restart, so an app with many sessions does not launch every provider together.
const defaultStartupChatResumeWorkers = 2

// defaultStartupChatResumeDelays spaces the retries of a startup resume that
// failed, the same bounded backoff a failed wake uses.
var defaultStartupChatResumeDelays = []time.Duration{
	5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute,
}

// startupChatResumeTimeout bounds one resume attempt; a provider handshake that
// hangs must not hold a worker slot forever.
const startupChatResumeTimeout = 2 * time.Minute

// resumeStartupChats resumes Chats whose agents died with the previous daemon,
// as the user's Resume would, so workers keep working and the orchestrator keeps
// receiving their reports. A shared semaphore bounds concurrent resumes across
// the startup pass and its retries.
// A session already scheduled is skipped: its pending attempt also delivers
// whatever was queued since.
func (m *Manager) resumeStartupChats(ctx context.Context, ids []domain.SessionID) {
	for _, id := range ids {
		m.startupChatResumeMu.Lock()
		_, pending := m.startupChatResumePending[id]
		if !pending {
			m.startupChatResumePending[id] = struct{}{}
		}
		m.startupChatResumeMu.Unlock()
		if !pending {
			go m.resumeStartupChat(ctx, id)
		}
	}
}

func (m *Manager) resumeStartupChat(ctx context.Context, id domain.SessionID) {
	defer func() {
		m.startupChatResumeMu.Lock()
		delete(m.startupChatResumePending, id)
		m.startupChatResumeMu.Unlock()
	}()
	for attempt := 0; ; attempt++ {
		select {
		case m.startupChatResumeSem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		done, err := m.startupChatResume(ctx, id)
		<-m.startupChatResumeSem
		if done {
			if err != nil {
				m.logger.Warn("reconcile: chat not resumed after restart", "sessionID", id, "error", err)
			} else {
				m.logger.Info("reconcile: chat resumed after restart", "sessionID", id, "attempt", attempt+1)
			}
			return
		}
		if attempt >= len(m.startupChatResumeDelays) || ctx.Err() != nil {
			m.logger.Warn("reconcile: chat resume after restart gave up", "sessionID", id, "attempts", attempt+1, "error", err)
			return
		}
		m.logger.Warn("reconcile: chat resume after restart failed, retrying", "sessionID", id, "attempt", attempt+1, "error", err)
		timer := time.NewTimer(m.startupChatResumeDelays[attempt])
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// resumeStartupChatOnce reports done when no further attempt is needed: the
// resume succeeded, or the session no longer wants one (the user terminated,
// resumed or put it to sleep meanwhile, or the failure is permanent).
func (m *Manager) resumeStartupChatOnce(ctx context.Context, id domain.SessionID) (bool, error) {
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return false, err
	}
	if !found || rec.IsTerminated || rec.HibernatedAt != nil || rec.Activity.State != domain.ActivityExited ||
		domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat || m.chat == nil ||
		m.chat.HasLiveChatController(id) || m.agentExitReason(ctx, id) != domain.AgentExitHostLost {
		return true, nil
	}
	attemptCtx, cancel := context.WithTimeout(ctx, startupChatResumeTimeout)
	defer cancel()
	err = m.autoResume(attemptCtx, id, false)
	if err == nil || errors.Is(err, ErrTerminated) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrAgentNotExited) ||
		errors.Is(err, ErrResumeModelDenied) {
		// A denied model stays denied: retrying only loops. The session stays stopped.
		return true, err
	}
	return false, err
}

// agentExitReasonStore persists why a session's agent last exited.
type agentExitReasonStore interface {
	SetSessionAgentExitReason(context.Context, domain.SessionID, domain.AgentExitReason) error
	SessionAgentExitReason(context.Context, domain.SessionID) (domain.AgentExitReason, error)
}

// setAgentExitReason is best effort: a lost reason reads as unknown, which only
// keeps the agent stopped.
func (m *Manager) setAgentExitReason(ctx context.Context, id domain.SessionID, reason domain.AgentExitReason) {
	store, ok := m.store.(agentExitReasonStore)
	if !ok {
		return
	}
	if err := store.SetSessionAgentExitReason(ctx, id, reason); err != nil {
		m.logger.Warn("could not record agent exit reason", "sessionID", id, "reason", reason, "error", err)
	}
}

// agentExitReason reads the recorded reason; any failure reads as unknown.
func (m *Manager) agentExitReason(ctx context.Context, id domain.SessionID) domain.AgentExitReason {
	store, ok := m.store.(agentExitReasonStore)
	if !ok {
		return domain.AgentExitUnknown
	}
	reason, err := store.SessionAgentExitReason(ctx, id)
	if err != nil {
		m.logger.Warn("could not read agent exit reason", "sessionID", id, "error", err)
		return domain.AgentExitUnknown
	}
	return reason
}

// defaultStartupChatRetryDelays spaces the reattach attempts for a Chat whose
// startup check failed transiently. Its provider host is still running, so a
// later attempt reconnects to it: no provider is launched and no history moves.
var defaultStartupChatRetryDelays = []time.Duration{
	2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second,
	30 * time.Second, time.Minute, time.Minute,
}

// retryableStartupChatCheck reports failures a later reattach can succeed past:
// the check's deadline (a busy writer at startup) and an inconclusive host owner
// (a replaced daemon has not detached yet). Anything else needs the user.
func retryableStartupChatCheck(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ports.ErrChatRecoveryInconclusive)
}

// retryStartupChatChecks reattaches Chats whose startup check failed
// transiently. Without it, a live host reads "stopped" until the user clicks
// Resume, even though the next attempt would reconnect immediately. The
// sessions stay "checking" until an attempt settles them, so clients show a
// reconnect in progress instead of offering Resume.
func (m *Manager) retryStartupChatChecks(ctx context.Context, ids []domain.SessionID) {
	for i, delay := range m.startupChatRetryDelays {
		if len(ids) == 0 {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		final := i == len(m.startupChatRetryDelays)-1
		remaining := ids[:0]
		for _, id := range ids {
			if m.retryStartupChatCheck(ctx, id, final) {
				remaining = append(remaining, id)
			}
		}
		ids = remaining
	}
}

// retryStartupChatCheck makes one reattach attempt and reports whether the
// session still needs another one. Every other outcome settles its readiness.
func (m *Manager) retryStartupChatCheck(ctx context.Context, id domain.SessionID, final bool) bool {
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		if !final && ctx.Err() == nil {
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
		if errors.Is(err, errAgentOperationInProgress) && !final {
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
	retry := retryableStartupChatCheck(err) && !final && ctx.Err() == nil
	if !retry {
		m.finishStatusRecovery(ctx, rec, err)
	}
	m.logger.Warn("reconcile: chat reattach retry failed", "sessionID", id, "retrying", retry, "error", err)
	return retry
}

// legacyInterruptedClientSetup matches a synchronous client start that a build
// from before background branch naming checkpointed setup for and then lost to
// a restart. Nothing creates this state any more; such a start was never
// acknowledged, so it is rolled back to keep its client request retryable.
// ponytail: upgrade path for older builds; delete once they are no longer in use.
func legacyInterruptedClientSetup(rec domain.SessionRecord) bool {
	return rec.ClientRequestID != "" && !rec.ClientRequestCommitted && !rec.IsTerminated &&
		rec.Metadata.RuntimeHandleID == "" && rec.Metadata.ProviderConversationID == "" &&
		slices.ContainsFunc(rec.ProvisionSteps, func(step domain.SessionProvisionStep) bool {
			return step.ID == domain.SessionProvisionStepSetup && step.Status == domain.SessionProvisionStepRunning
		})
}

// rollbackLegacyClientSetup reports settled when the session is gone or was
// rolled back; otherwise it returns the current record for the normal check.
func (m *Manager) rollbackLegacyClientSetup(ctx context.Context, rec domain.SessionRecord) (bool, domain.SessionRecord, error) {
	release := sync.OnceFunc(m.acquireWorkspaceGate(rec.ProjectID))
	defer release()
	ctx = context.WithValue(ctx, spawnWorkspaceGateKey{}, release)
	current, found, err := m.store.GetSession(ctx, rec.ID)
	if err != nil || !found {
		return true, current, err
	}
	// An initial spawn can finish while this pass waits for the project gate.
	if !current.CreatedAt.Equal(rec.CreatedAt) || !legacyInterruptedClientSetup(current) {
		return false, current, nil
	}
	rows, isProject, err := m.workspaceProjectRows(ctx, current)
	if err != nil {
		return true, current, err
	}
	var info *ports.WorkspaceProjectInfo
	if isProject {
		info = &ports.WorkspaceProjectInfo{Root: workspaceInfo(current), Worktrees: rows}
	}
	// The old build saved this unacknowledged path; clear it so the seed row
	// can be deleted once the worktree is gone.
	m.clearProvisionedWorkspace(ctx, current.ID, current.Metadata.WorkspacePath)
	m.rollbackSeedSpawnWorkspace(ctx, current, workspaceInfo(current), info, false, false)
	return true, current, nil
}
