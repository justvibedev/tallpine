package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// RestoreWithMode relaunches a torn-down session and reports whether Tallpine used
// native resume, a saved-prompt fallback, or a fresh launch. The fallible I/O
// runs before any durable session write, so a failure never resurrects the row
// or destroys the worktree (it may hold the agent's prior work).
func (m *Manager) RestoreWithMode(ctx context.Context, id domain.SessionID) (RestoreResult, error) {
	if err := m.beginAgentOperation(ctx, id, agentOperationRestore); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			err = ErrOperationInProgress
		}
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, err)
	}
	defer m.endAgentOperation(id, agentOperationRestore)

	if active, err := m.hasActiveInterfaceTransition(ctx, id); err != nil {
		return RestoreResult{}, fmt.Errorf("restore %s: interface transition: %w", id, err)
	} else if active {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, ErrInterfaceTransitionInProgress)
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, err)
	}
	if !ok {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, ErrNotFound)
	}
	if rec.Harness.IsUnsupported() {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, harnessLookupError(rec.Harness))
	}
	releaseHarness, err := m.beginHarnessUse(rec.Harness)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, err)
	}
	defer releaseHarness()
	if !rec.IsTerminated {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, ErrNotRestorable)
	}
	// The retention policy purged its raw provider events or transcript: a
	// native-history replay would duplicate text without the dedupe rows.
	if purged, ok := m.store.(retentionPurgeReader); ok {
		if gone, err := purged.SessionRetentionPurged(ctx, id); err != nil {
			return RestoreResult{}, fmt.Errorf("restore %s: %w", id, err)
		} else if gone {
			return RestoreResult{}, fmt.Errorf("restore %s: %w: its history was purged by the retention policy", id, ErrNotRestorable)
		}
	}
	meta := rec.Metadata
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, err)
	}
	// Mirror Kill's incomplete-handle guard: a session whose spawn failed before
	// the workspace landed has neither WorkspacePath nor Branch, and there is
	// nothing meaningful to restore from. Surface this as a typed 409 instead of
	// letting workspace.Restore fail with an opaque wrapped error.
	if meta.WorkspacePath == "" || (meta.Branch == "" && projectKindForSession(project, rec.ProjectID) != domain.ProjectKindScratch) {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, ErrIncompleteHandle)
	}
	// Resumability is decided inside restoreArgv, not here. A promptless session
	// can still be fully resumable when the harness pins a deterministic session id
	// (Claude Code). restoreArgv returns ErrNotResumable only for a promptless,
	// unresumable non-orchestrator (a worker with no task and no native id to resume).
	// Orchestrators always relaunch fresh with the system prompt only.

	releaseWorkspaceGate := m.acquireWorkspaceGate(rec.ProjectID)
	defer releaseWorkspaceGate()

	// A restore relaunches a stopped session, so no agent holds the worktree.
	ws, err := m.restoreSessionWorkspace(ctx, project, rec, true)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore %s: workspace: %w", id, err)
	}
	return m.relaunchRestoredSession(ctx, rec, project, ws)
}

func (m *Manager) relaunchRestoredSession(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, ws ports.WorkspaceInfo) (RestoreResult, error) {
	result, err := m.relaunchSession(ctx, "restore", rec, project, ws, nil)
	if err != nil {
		return RestoreResult{}, err
	}
	if rec.Kind == domain.KindWorker {
		if err := m.restoreReviewer(ctx, rec.ID); err != nil {
			m.logger.Warn("restore: reviewer terminal restore failed; worker remains restored", "sessionID", rec.ID, "error", err)
		}
	}
	return result, nil
}

// ExitAgent stops only the current agent controller. The Tallpine session, worktree,
// terminal identity, and provider-native conversation remain available for an
// exact ResumeAgentWithMode call.
func (m *Manager) ExitAgent(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	if err := m.beginAgentOperation(ctx, id, agentOperationExit); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			err = ErrAgentExitInProgress
		}
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: %w", id, err)
	}
	defer m.endAgentOperation(id, agentOperationExit)

	if active, err := m.hasActiveInterfaceTransition(ctx, id); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: interface transition: %w", id, err)
	} else if active {
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: %w", id, ErrInterfaceTransitionInProgress)
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: %w", id, ErrNotFound)
	}
	if rec.IsTerminated {
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: %w", id, ErrTerminated)
	}
	if rec.Activity.State == domain.ActivityExited {
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: %w", id, ErrAgentExited)
	}
	if err := m.releaseTerminalRuntime(ctx, rec); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: %w", id, err)
	}
	if err := m.stopAgentController(ctx, rec); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: %w", id, err)
	}
	if err := m.recordAgentExited(ctx, rec); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: record exit: %w", id, err)
	}
	m.setAgentExitReason(ctx, id, domain.AgentExitUser)
	exited, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: confirm exit: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: %w", id, ErrNotFound)
	}
	if exited.Activity.State != domain.ActivityExited {
		return domain.SessionRecord{}, fmt.Errorf("exit agent %s: %w", id, ErrSwitchSourceStopUnconfirmed)
	}
	return exited, nil
}

// releaseTerminalRuntime records, before a terminal agent's runtime is
// destroyed, that its launch ends on purpose. Without it the reaper reads the
// dead runtime as a crash and ends the session ExitAgent keeps.
func (m *Manager) releaseTerminalRuntime(ctx context.Context, rec domain.SessionRecord) error {
	launchID := strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat || launchID == "" {
		return nil
	}
	store, ok := m.store.(interface {
		SetSessionRuntimeReleased(context.Context, domain.SessionID, string) (bool, error)
	})
	if !ok {
		return nil
	}
	released, err := store.SetSessionRuntimeReleased(ctx, rec.ID, launchID)
	if err != nil {
		return fmt.Errorf("record runtime release: %w", err)
	}
	if !released {
		return fmt.Errorf("record runtime release: launch %s is no longer the session's", launchID)
	}
	return nil
}

// stopAgentController is the single controller-stop primitive used by the
// public exit-agent operation and coordinated Codex account switching. It
// never terminates the Tallpine session or releases its worktree.
func (m *Manager) stopAgentController(ctx context.Context, rec domain.SessionRecord) error {
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if m.chat == nil {
			return ErrIncompleteHandle
		}
		return m.chat.StopChat(ctx, rec.ID)
	}
	handle := ports.RuntimeHandle{ID: strings.TrimSpace(rec.Metadata.RuntimeHandleID)}
	if handle.ID == "" || strings.TrimSpace(rec.Metadata.RuntimeLaunchID) == "" {
		return ErrIncompleteHandle
	}
	return m.stopSourceRuntime(ctx, ports.FencedRuntimeRef{
		Handle:         handle,
		SessionID:      rec.ID,
		Generation:     strings.TrimSpace(rec.Metadata.RuntimeLaunchID),
		NativeIdentity: strings.TrimSpace(rec.Metadata.AgentSessionID),
	})
}

// stopSourceRuntime stops one fenced TUI runtime. A failed destroy is checked
// against the runtime's own liveness and retried once before it is reported.
func (m *Manager) stopSourceRuntime(ctx context.Context, ref ports.FencedRuntimeRef) error {
	if strings.TrimSpace(ref.Handle.ID) == "" {
		return ErrIncompleteHandle
	}
	var errs []error
	var probe ports.FencedProbeResult
	for range 2 {
		err := m.runtime.Destroy(ctx, ref.Handle)
		if err == nil {
			return nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			return errors.Join(append(errs, ctx.Err())...)
		}
		if probe = m.runtime.ProbeFencedRuntime(ctx, ref); probe.Liveness == ports.FencedDead {
			return nil
		}
	}
	return errors.Join(append(errs, fmt.Errorf("runtime ownership remains unresolved: %s", probe.Reason))...)
}

func (m *Manager) recordAgentExited(ctx context.Context, rec domain.SessionRecord) error {
	signal := ports.ActivitySignal{
		Valid:     true,
		State:     domain.ActivityExited,
		Timestamp: m.clock(),
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		signal.Event = "chat.controller.stopped"
		signal.ControllerGeneration = strings.TrimSpace(rec.Metadata.ControllerGeneration)
	} else {
		signal.Event = "process-exited"
		signal.LaunchID = strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
	}
	return m.lcm.ApplyActivitySignal(ctx, rec.ID, signal)
}

// HibernateIdleChats releases eligible Chat provider processes. The chat
// service rechecks turn completion and controller quiescence under its send lock.
func (m *Manager) HibernateIdleChats(ctx context.Context) error {
	ids, err := m.store.ListChatHibernationCandidates(ctx)
	if err != nil {
		return fmt.Errorf("list chats for hibernation: %w", err)
	}
	var errs []error
	for _, id := range ids {
		err := m.hibernateEligibleChat(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("hibernate chat %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) hibernateEligibleChat(ctx context.Context, id domain.SessionID) error {
	hibernator, ok := m.chat.(interface {
		HibernateChat(context.Context, domain.SessionID) (bool, error)
	})
	if !ok {
		return nil
	}
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := m.beginAgentOperation(operationCtx, id, agentOperationHibernate); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			return nil
		}
		return err
	}
	defer m.endAgentOperation(id, agentOperationHibernate)
	active, err := m.hasActiveInterfaceTransition(operationCtx, id)
	if err != nil || active {
		return err
	}
	if before := m.beforeHibernate; before != nil {
		// An idle orchestrator compacts before it sleeps (B204); the
		// compaction is a turn, so it stays awake this pass.
		// A failed check must not keep the session awake: log it and
		// hibernate; HibernateChat still refuses a busy controller.
		compacted, err := before(operationCtx, id)
		if err != nil {
			m.logger.Warn("idle compaction before hibernation failed", "session", id, "err", err)
		}
		if compacted {
			return nil
		}
	}
	_, err = hibernator.HibernateChat(operationCtx, id)
	return err
}

// WakeHibernatedChat waits for an in-flight hibernation or wake, then resumes
// the native provider conversation when there is no live Chat controller. This
// also retries a failed wake after its durable marker was cleared.
func (m *Manager) WakeHibernatedChat(ctx context.Context, id domain.SessionID) error {
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.agentOpMu.Lock()
		operation := m.agentOperations[id]
		m.agentOpMu.Unlock()
		if operation == agentOperationHibernate || operation == agentOperationResume {
			select {
			case <-waitCtx.Done():
				return waitCtx.Err()
			case <-ticker.C:
				continue
			}
		}
		rec, found, err := m.store.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if rec.HibernatedAt == nil && m.chat != nil && m.chat.HasLiveChatController(id) {
			return nil
		}
		err = m.autoResume(ctx, id, false)
		if errors.Is(err, ErrResumeInProgress) {
			m.agentOpMu.Lock()
			operation = m.agentOperations[id]
			m.agentOpMu.Unlock()
			if operation == agentOperationHibernate || operation == agentOperationResume {
				continue
			}
		}
		return err
	}
}

// retentionPurgeReader is the optional store surface that reports a session
// purged by the session retention policy.
type retentionPurgeReader interface {
	SessionRetentionPurged(ctx context.Context, id domain.SessionID) (bool, error)
}
