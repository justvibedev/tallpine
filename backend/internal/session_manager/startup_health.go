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
// hangs must not hold a worker slot forever. It matches a manual Resume's
// budget: on a loaded machine after a restart the handshake alone can take a
// minute and the history import's conversation read many seconds more.
var startupChatResumeTimeout = manualResumeBudget

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
				if errors.Is(err, ErrResumeModelDenied) {
					m.reportStartupResumeFailed(ctx, id, err)
				}
			} else {
				m.logger.Info("reconcile: chat resumed after restart", "sessionID", id, "attempt", attempt+1)
			}
			return
		}
		if attempt >= len(m.startupChatResumeDelays) || ctx.Err() != nil {
			m.logger.Warn("reconcile: chat resume after restart gave up", "sessionID", id, "attempts", attempt+1, "error", err)
			m.reportStartupResumeFailed(ctx, id, err)
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

// reportStartupResumeFailed tells the worker's orchestrator that its agent did
// not come back. Only workers cut off mid-turn are in the restart tracker, so
// without this an idle worker stays stopped with nothing but a log line (B627).
// A resume cut short by daemon shutdown is retried at the next boot: not news.
func (m *Manager) reportStartupResumeFailed(ctx context.Context, id domain.SessionID, cause error) {
	if m.startFailed != nil && ctx.Err() == nil {
		m.startFailed(id, fmt.Sprintf("could not resume after the app restarted: %v", cause))
	}
}

// reportStartupReattachFailed reports a Chat whose startup check ended without
// a controller: the worker is not running and only Resume brings it back. Silent
// on purpose: a session already stopped by choice or asleep has nothing to report,
// and callers do not report a store read failure (nothing is known about the
// session) or a user operation owning it (that operation settles it).
func (m *Manager) reportStartupReattachFailed(ctx context.Context, rec domain.SessionRecord, cause error) {
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat || rec.HibernatedAt != nil ||
		rec.Activity.State == domain.ActivityExited && m.agentExitReason(ctx, rec.ID) != domain.AgentExitHostLost {
		return
	}
	m.reportStartupResumeFailed(ctx, rec.ID, cause)
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
	userOwned, err := m.autoResumeJoined(attemptCtx, id, false)
	if err != nil && userOwned {
		// The owner pressed Resume during this attempt and got its result;
		// another automatic attempt now would only take the lock their next
		// press needs.
		return true, fmt.Errorf("left to the user's Resume: %w", err)
	}
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
