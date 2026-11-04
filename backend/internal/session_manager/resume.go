package sessionmanager

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ResumeAgentWithMode replaces an exited agent inside its still-live session.
// Unlike RestoreWithMode, it preserves the existing worktree and terminal
// identity and never changes the durable terminated flag as an intermediate
// step.
func (m *Manager) ResumeAgentWithMode(ctx context.Context, id domain.SessionID) (result RestoreResult, err error) {
	return m.resumeAgentWithMode(ctx, id, false)
}

// resumeAgentWithMode preserves strict native-history intent for automatic
// retries, including update stops that have no stale-binary failure marker.
func (m *Manager) resumeAgentWithMode(ctx context.Context, id domain.SessionID, requireNativeHistory bool) (result RestoreResult, err error) {
	if err := m.beginAgentResumeJoining(ctx, id); err != nil {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, err)
	}
	resumeHandedOff := false
	defer func() {
		if !resumeHandedOff {
			m.endAgentResume(id)
		}
	}()
	if active, err := m.hasActiveInterfaceTransition(ctx, id); err != nil {
		return RestoreResult{}, fmt.Errorf("resume agent %s: interface transition: %w", id, err)
	} else if active {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrInterfaceTransitionInProgress)
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, err)
	}
	if !ok {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrNotFound)
	}
	if rec.HibernatedAt != nil && m.persistentHostReconcileDone != nil {
		select {
		case <-m.persistentHostReconcileDone:
		case <-ctx.Done():
			return RestoreResult{}, fmt.Errorf("resume agent %s: wait for persistent host reconciliation: %w", id, ctx.Err())
		}
	}
	m.asyncChatSpawnsMu.Lock()
	_, starting := m.asyncChatSpawns[id]
	m.asyncChatSpawnsMu.Unlock()
	if starting {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrResumeInProgress)
	}
	if rec.Harness.IsUnsupported() {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, harnessLookupError(rec.Harness))
	}
	releaseHarness, err := m.beginHarnessUse(rec.Harness)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, err)
	}
	defer func() {
		if releaseHarness != nil {
			releaseHarness()
		}
	}()
	if rec.IsTerminated {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrTerminated)
	}
	if rec.ProvisionState.IsProvisioning() {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrResumeInProgress)
	}
	if rec.ProvisionState == domain.SessionProvisionFailed {
		result, handedOff, err := m.retryFailedChatSpawn(ctx, rec, releaseHarness)
		if handedOff {
			releaseHarness = nil
			resumeHandedOff = true
		}
		return result, err
	}
	if rec.HibernatedAt == nil && m.SessionStatusReadiness(rec) == "unavailable" {
		m.beginStatusRecovery(id)
		recoveryCtx, cancel := context.WithTimeout(ctx, m.statusVerificationLimit)
		defer cancel()
		err := m.reconcileLive(recoveryCtx, rec)
		m.finishStatusRecovery(ctx, rec, err)
		if err != nil {
			return RestoreResult{}, err
		}
		current, err := m.getRecord(ctx, id)
		return RestoreResult{Session: current, Mode: RestoreModeNative}, err
	}
	mode := domain.NormalizeSessionMode(rec.Mode)
	if mode == domain.SessionModeChat && m.chat != nil && m.chat.HasLiveChatController(id) {
		// Already running (for example an auto-resume or wake won the race):
		// resume is idempotent, so report the current state instead of
		// failing a user who pressed Resume on a live Chat. A second
		// controller is never started. The operation lock taken above
		// serializes this check behind any in-flight wake or resume.
		current, err := m.getRecord(ctx, id)
		return RestoreResult{Session: current, Mode: RestoreModeNative}, err
	}
	if rec.Activity.State != domain.ActivityExited {
		// Builds before the controller-stop lifecycle fix can leave a Chat row
		// idle, active, or blocked even though no controller survived. The live
		// registry is authoritative for whether a duplicate Chat controller could
		// be created, so recover only when it confirms there is none. TUI keeps its
		// existing durable-exited precondition.
		if mode != domain.SessionModeChat || m.chat == nil {
			return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrAgentNotExited)
		}
	} else if mode == domain.SessionModeTUI && m.launchedTUIAgentStillAlive(ctx, rec) {
		// A durable exited state is a hook-derived claim. Resume tears down the
		// runtime, so refuse when the exact agent Tallpine launched for this generation
		// is provably still running: replacing it would kill a live conversation
		// and resume whatever native id the stray signal recorded (#6454).
		//
		// Reconcile the activity state from this live probe so the session no
		// longer shows as exited and the refusal is not a dead end.
		signal := ports.ActivitySignal{
			Valid:            true,
			State:            domain.ActivityIdle,
			Event:            "liveness-probe",
			LaunchID:         strings.TrimSpace(rec.Metadata.RuntimeLaunchID),
			ExpectedRevision: &rec.Revision,
			Timestamp:        m.clock(),
		}
		if err := m.lcm.ApplyActivitySignal(ctx, rec.ID, signal); err != nil {
			m.logger.Warn("resume agent: reconcile live agent activity failed", "sessionID", rec.ID, "error", err)
		}
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrAgentNotExited)
	}
	if rec.HibernatedAt != nil {
		rec, err = m.clearResumeHibernation(ctx, id, rec)
		if err != nil {
			return RestoreResult{}, err
		}
	}
	result, err = m.resumeAgentRecordWithPolicy(ctx, "resume agent", rec,
		requireNativeHistory || (rec.StaleBinary != nil && rec.StaleBinary.SwitchFailedAt != nil))
	if err == nil && m.chat != nil && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		m.setAgentExitReason(ctx, id, domain.AgentExitUnknown)
		// Messages queued behind the lost turn (or kept by a failed move to a new
		// release) wait for this controller; deliver them now, as a wake would.
		if drainErr := m.chat.DrainChatQueue(ctx, id); drainErr != nil {
			m.logger.Warn("could not deliver messages queued before resume", "sessionID", id, "error", drainErr)
		}
	}
	return result, err
}

// launchedTUIAgentStillAlive reports whether the exact Tallpine supervisor of the
// session's current launch still owns a live agent child. Only a definitive
// alive reading counts: probe errors, missing handles, and runtimes without the
// strict capability keep the existing resume behavior.
func (m *Manager) launchedTUIAgentStillAlive(ctx context.Context, rec domain.SessionRecord) bool {
	handleID := strings.TrimSpace(rec.Metadata.RuntimeHandleID)
	launchID := strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
	if handleID == "" || launchID == "" {
		return false
	}
	inspector, ok := m.runtime.(ports.ExactSupervisedProcessInspector)
	if !ok {
		return false
	}
	alive, err := inspector.IsExactSupervisedProcessAlive(ctx, ports.RuntimeHandle{ID: handleID}, ports.SupervisedProcessRef{
		SessionID: rec.ID,
		LaunchID:  launchID,
	})
	if err != nil {
		m.logger.Warn("resume agent: launched agent liveness probe failed", "sessionID", rec.ID, "launch", launchID, "error", err)
		return false
	}
	return alive
}

func (m *Manager) resumeAgentRecordWithPolicy(
	ctx context.Context,
	operation string,
	rec domain.SessionRecord,
	requireNativeHistory bool,
) (RestoreResult, error) {
	return m.resumeAgentRecordWithReservedGeneration(ctx, operation, rec, false, requireNativeHistory, "")
}

func (m *Manager) resumeAgentRecordWithReservedGeneration(
	ctx context.Context,
	operation string,
	rec domain.SessionRecord,
	forceFresh bool,
	requireNativeHistory bool,
	reservedGeneration string,
) (RestoreResult, error) {
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	meta := rec.Metadata
	if meta.WorkspacePath == "" ||
		(meta.Branch == "" && projectKindForSession(project, rec.ProjectID) != domain.ProjectKindScratch) {
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, ErrIncompleteHandle)
	}
	ws := ports.WorkspaceInfo{
		Path:      meta.WorkspacePath,
		Branch:    meta.Branch,
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat || meta.RuntimeHandleID == "" {
		return m.relaunchSessionWithPolicyAndGeneration(ctx, operation, rec, project, ws, nil, forceFresh, requireNativeHistory, reservedGeneration, domain.SessionInterfaceTransitionHistoryStrict, nil)
	}
	handle := ports.RuntimeHandle{ID: meta.RuntimeHandleID}
	return m.relaunchSessionWithPolicyAndGeneration(ctx, operation, rec, project, ws, &handle, forceFresh, requireNativeHistory, reservedGeneration, domain.SessionInterfaceTransitionHistoryStrict, nil)
}

// clearResumeHibernation clears a hibernated session's sleep marker before it
// resumes, re-reading the row until the marker is gone.
func (m *Manager) clearResumeHibernation(ctx context.Context, id domain.SessionID, rec domain.SessionRecord) (domain.SessionRecord, error) {
	var err error
	store, ok := m.store.(interface {
		SetSessionHibernated(context.Context, domain.SessionID, int64, *time.Time) (bool, error)
	})
	if !ok {
		return rec, fmt.Errorf("resume agent %s: hibernation store unavailable", id)
	}
	// A failed launch stays stopped. Keep its native identity for the existing
	// Resume action instead of hiding the failure behind a sleep marker.
	for range 3 {
		if rec.HibernatedAt == nil {
			break
		}
		_, clearErr := store.SetSessionHibernated(ctx, id, rec.Revision, nil)
		if clearErr != nil {
			return rec, fmt.Errorf("resume agent %s: clear hibernation: %w", id, clearErr)
		}
		rec, err = m.getRecord(ctx, id)
		if err != nil {
			return rec, err
		}
		if rec.IsTerminated {
			return rec, fmt.Errorf("resume agent %s: %w", id, ErrTerminated)
		}
	}
	if rec.HibernatedAt != nil {
		return rec, fmt.Errorf("resume agent %s: hibernation state changed: %w", id, ErrResumeInProgress)
	}
	return rec, nil
}
