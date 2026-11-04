package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// reconcileLive performs explicit recovery of a non-terminated session. It
// adopts a surviving runtime or relaunches the controller in its existing
// worktree, preserving conversation identity when recovery fails. Startup uses
// checkSessionHealth instead so missing TUI agents stay stopped.
func (m *Manager) reconcileLive(ctx context.Context, rec domain.SessionRecord) error {
	if rec.HibernatedAt != nil {
		return nil
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	// An asynchronous Chat spawn is mid-flight or was interrupted: it may have no
	// workspace yet, or a worktree published ahead of its controller. Neither is
	// a session this pass can act on. They are also not phantoms — the API handed
	// the id to a client, the session is on screen, any failure explains itself,
	// and the user's queued messages live in it. FailInterruptedProvisioning
	// settles them; reaping or relaunching here would be the disappearing session
	// that state exists to prevent.
	if rec.ProvisionState.WithDefault() != domain.SessionProvisionReady {
		return nil
	}
	projectKind := projectKindForSession(project, rec.ProjectID)
	if rec.Metadata.WorkspacePath == "" || (rec.Metadata.Branch == "" && projectKind != domain.ProjectKindScratch) {
		// The previous daemon died before Spawn committed a workspace (e.g. the
		// app was closed while "Preparing the worker terminal" was still
		// creating the worktree). Nothing observable was ever built, so — same
		// as an ordinary in-request spawn failure — remove the seed row instead
		// of leaving a non-terminated phantom that can never launch sitting in
		// the sidebar forever.
		m.rollbackSpawnSeedRow(ctx, rec.ID)
		return nil
	}
	isChat := domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat

	if !isChat {
		handle := runtimeHandle(rec.Metadata)
		if handle.ID != "" {
			alive, err := m.runtime.IsAlive(ctx, handle)
			switch {
			case err == nil:
			case errors.Is(err, ports.ErrRuntimeUnavailable):
				// Normal after a machine reboot: the runtime is conclusively gone,
				// so proceed to the in-place relaunch below.
				alive = false
			default:
				// A failed probe is not proof of death: leave the session as-is.
				return fmt.Errorf("reconcile %s: probe: %w", rec.ID, err)
			}
			if alive {
				return nil // adopt: the session survived the crash.
			}
		}
	}
	// Legacy Scratch sessions were intentionally one-shot. Standalone sessions
	// also use the plain-directory workspace adapter, but unlike Scratch they
	// are durable and must be relaunched after the daemon restarts.
	legacyScratch := projectKind == domain.ProjectKindScratch && rec.ProjectID != ""
	if legacyScratch && !isChat {
		return m.lcm.MarkTerminated(ctx, rec.ID)
	}
	var ws ports.WorkspaceInfo
	var restoreErr error
	if legacyScratch {
		ws = workspaceInfo(rec)
	} else {
		// A Chat provider may still be running and be live-reconnected below,
		// so its worktree must not be refreshed here. Non-Chat sessions reach
		// this point only when the runtime is proven gone.
		ws, restoreErr = m.restoreSessionWorkspace(ctx, project, rec, !isChat)
	}
	if restoreErr == nil {
		_, relaunchErr := m.relaunchRestoredSession(ctx, rec, project, ws)
		if relaunchErr == nil {
			return nil
		}
		if errors.Is(relaunchErr, ports.ErrChatRecoveryInconclusive) {
			return fmt.Errorf("reconcile %s: preserve detached Chat provider: %w", rec.ID, relaunchErr)
		}
		restoreErr = relaunchErr
	}
	// A provider or runtime dependency can be temporarily unavailable during an
	// app restart (for example, a GUI-launched daemon may have a sparse PATH).
	// That is not user intent to terminate the Tallpine session, remove its worktree,
	// or retire an orchestrator. Preserve the durable session and native resume
	// identity, but expose the stopped controller as an exited workload so the
	// existing Resume Agent path can retry it in place.
	committed, preserveErr := m.preserveFailedReconcileRelaunch(ctx, rec)
	if preserveErr != nil {
		return fmt.Errorf("reconcile %s: relaunch failed and commit state became uncertain: %w", rec.ID, errors.Join(restoreErr, preserveErr))
	}
	if committed {
		m.logger.Warn("reconcile: relaunch reported an error after the controller commit; preserving the live session", "sessionID", rec.ID, "error", restoreErr)
		return nil
	}
	return fmt.Errorf("reconcile %s: relaunch controller: %w", rec.ID, restoreErr)
}

func (m *Manager) relaunchCommitted(before, after domain.SessionRecord) bool {
	if after.IsTerminated {
		return false
	}
	if domain.NormalizeSessionMode(before.Mode) == domain.SessionModeChat {
		generation := after.Metadata.ControllerGeneration
		// Chat Service durably claims a generation before native-history import
		// and ControllerReady. A changed generation alone therefore proves only
		// that launch began, not that a controller reached the published registry.
		// Require both durable epoch movement and live registry ownership before
		// treating an error returned after launch as a committed controller.
		return generation != "" && generation != before.Metadata.ControllerGeneration &&
			m.chat != nil && m.chat.HasLiveChatController(before.ID)
	}
	launchID := after.Metadata.RuntimeLaunchID
	if launchID != "" && launchID != before.Metadata.RuntimeLaunchID {
		return true
	}
	handleID := after.Metadata.RuntimeHandleID
	return handleID != "" && handleID != before.Metadata.RuntimeHandleID
}

// preserveFailedReconcileRelaunch transitions the current controller epoch to
// exited without using the boot snapshot as a CAS token. Launch preparation can
// legitimately persist metadata (notably the browser capability verifier)
// before runtime creation fails, so the current row must be read immediately
// before the activity signal. Since ApplyActivitySignal intentionally treats a
// stale CAS as a no-op, reread and retry rather than claiming recovery succeeded
// while the session is still active and exposed to the reaper.
func (m *Manager) preserveFailedReconcileRelaunch(ctx context.Context, before domain.SessionRecord) (bool, error) {
	const maxAttempts = 3
	for range maxAttempts {
		current, ok, err := m.store.GetSession(ctx, before.ID)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, ErrNotFound
		}
		if m.relaunchCommitted(before, current) {
			return true, nil
		}
		if current.IsTerminated || current.Activity.State == domain.ActivityExited {
			return false, nil
		}

		signal := ports.ActivitySignal{
			Valid:            true,
			State:            domain.ActivityExited,
			ExpectedRevision: &current.Revision,
		}
		if domain.NormalizeSessionMode(current.Mode) == domain.SessionModeChat {
			signal.ControllerGeneration = current.Metadata.ControllerGeneration
		} else {
			signal.LaunchID = current.Metadata.RuntimeLaunchID
		}
		if err := m.lcm.ApplyActivitySignal(ctx, before.ID, signal); err != nil {
			return false, err
		}

		after, ok, err := m.store.GetSession(ctx, before.ID)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, ErrNotFound
		}
		if m.relaunchCommitted(before, after) {
			return true, nil
		}
		if after.IsTerminated || after.Activity.State == domain.ActivityExited {
			return false, nil
		}
	}
	return false, errors.New("session changed while recording failed relaunch")
}

// reconcileReap kills the leaked tmux session of a session the DB already marks
// terminated. This covers the teardown that marked the row terminated but failed
// to kill the runtime (e.g. ForceDestroy/Destroy errored after MarkTerminated).
// Destroy is idempotent, so an already-gone session is a no-op.
func (m *Manager) reconcileReap(ctx context.Context, rec domain.SessionRecord) error {
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		return nil
	}
	alive, err := m.runtime.IsAlive(ctx, handle)
	if err != nil {
		if errors.Is(err, ports.ErrRuntimeUnavailable) {
			return nil // no server means no leaked session to reap
		}
		return fmt.Errorf("reconcile reap %s: probe: %w", rec.ID, err)
	}
	if !alive {
		return nil
	}
	if err := m.runtime.Destroy(ctx, handle); err != nil {
		return fmt.Errorf("reconcile reap %s: destroy: %w", rec.ID, err)
	}
	return nil
}

// Reconcile settles interrupted operations, checks surviving controllers, and
// reaps leaked runtimes. A Chat whose provider host died with the previous
// daemon is resumed in the background (resumeStartupChats); other missing agents
// remain stopped until an explicit Resume or Restore. The daemon runs the health checks after binding its
// listener so a slow probe cannot delay access to durable session metadata.
func (m *Manager) Reconcile(ctx context.Context) error {
	if err := m.ReconcileStartupSafety(ctx); err != nil {
		return err
	}
	return m.ReconcileBackground(ctx)
}

// ReconcileStartupSafety closes interrupted operations or quarantines ambiguous
// interface targets with a restored input fence before the API accepts input.
func (m *Manager) ReconcileStartupSafety(ctx context.Context) error {
	m.startTransitionMessageDispatcher(ctx)
	_, err := m.recoverInterruptedInterfaceTransitions(ctx)
	if err != nil {
		return fmt.Errorf("reconcile: interface transitions: %w", err)
	}
	// Capture only rows that predate the listener. Git cleanup belongs in the
	// background, where a slow or unavailable repository cannot delay boot.
	m.startupTaskPreparations = []domain.SessionRecord{}
	m.startupProvisioningRetries = nil
	// No branch naming survives a restart; clients show the branch it left.
	if err := m.store.ClearSessionBranchNaming(ctx); err != nil {
		m.logger.Warn("reconcile: interrupted branch naming could not be cleared", "error", err)
	}
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		m.logger.Warn("reconcile: interrupted startup work could not be listed", "error", err)
	} else {
		for _, rec := range recs {
			if rec.IsTaskPreparation {
				m.startupTaskPreparations = append(m.startupTaskPreparations, rec)
			}
		}
	}
	// An asynchronous spawn lives in one daemon's memory. Anything still
	// "starting" after a restart has no one left to finish it, so say so rather
	// than leaving a session that spins forever.
	if err == nil {
		m.startupProvisioningRetries, err = m.failInterruptedProvisioningRecords(ctx, recs)
		if err != nil {
			m.logger.Warn("reconcile: interrupted session starts could not be settled", "error", err)
		}
	}
	return nil
}

// ReconcileBackground checks existing controllers and performs startup cleanup.
// It never launches missing agents or restores shutdown-saved workspaces.
func (m *Manager) ReconcileBackground(ctx context.Context) (resultErr error) {
	settled := false
	defer func() {
		if !settled {
			m.settleStartupReadiness(resultErr)
		}
	}()
	for _, interrupted := range m.startupTaskPreparations {
		releaseWorkspaceGate := m.acquireWorkspaceGate(interrupted.ProjectID)
		rec, ok, err := m.store.GetSession(ctx, interrupted.ID)
		if err == nil && ok && !interrupted.CreatedAt.IsZero() && rec.CreatedAt.Equal(interrupted.CreatedAt) &&
			rec.IsTaskPreparation && !rec.IsTerminated {
			err = m.cleanupTaskPreparationRecord(ctx, rec)
		}
		releaseWorkspaceGate()
		if err != nil {
			m.logger.Warn("interrupted task preparation cleanup failed", "sessionID", interrupted.ID, "error", err)
		}
	}
	m.startupTaskPreparations = nil
	for _, interrupted := range m.startupProvisioningRetries {
		id := interrupted.ID
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil {
			m.logger.Warn("reconcile: interrupted session start retry lookup failed", "sessionID", id, "error", err)
			continue
		}
		if !ok || interrupted.CreatedAt.IsZero() || !rec.CreatedAt.Equal(interrupted.CreatedAt) ||
			rec.IsTerminated || rec.IsTaskPreparation || !rec.ProvisionState.IsProvisioning() {
			continue
		}
		if _, err := m.setProvisionState(ctx, id, domain.SessionProvisionFailed,
			"Tallpine restarted before this session finished starting"); err != nil {
			m.logger.Warn("reconcile: interrupted session start retry failed", "sessionID", id, "error", err)
		}
	}
	m.startupProvisioningRetries = nil
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("reconcile: list sessions: %w", err)
	}
	m.reconcileLivePass(ctx, recs)
	// Readiness only depends on the live pass. Flip it before reaping so the UI
	// leaves "checking" without waiting for a runtime probe per terminated session.
	settled = true
	m.settleStartupReadiness(nil)
	m.reconcileReapTerminated(ctx, recs)
	if err := m.deliverAllTransitionMessages(ctx); err != nil {
		m.logger.Error("reconcile: transition-message delivery deferred for retry", "error", err)
	}
	m.wakeTransitionMessageDispatcher()
	return nil
}

// reconcileReapTerminated probes terminated sessions that still name a runtime
// handle, in parallel on the same bounded pool as the live pass. Records without
// a handle have nothing to leak and are skipped without a runtime call.
func (m *Manager) reconcileReapTerminated(ctx context.Context, recs []domain.SessionRecord) {
	var pending []domain.SessionRecord
	for _, rec := range recs {
		if rec.IsTerminated && runtimeHandle(rec.Metadata).ID != "" {
			pending = append(pending, rec)
		}
	}
	if len(pending) == 0 {
		return
	}
	workers := m.reconcileWorkers
	if workers < 1 {
		workers = 1
	}
	if workers > len(pending) {
		workers = len(pending)
	}
	jobs := make(chan domain.SessionRecord)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for rec := range jobs {
				if err := m.reconcileReap(ctx, rec); err != nil {
					m.logger.Error("reconcile: reap pass failed, skipping", "sessionID", rec.ID, "error", err)
				}
			}
		}()
	}
	for _, rec := range pending {
		jobs <- rec
	}
	close(jobs)
	wg.Wait()
}

func (m *Manager) reconcileLivePass(ctx context.Context, recs []domain.SessionRecord) {
	candidates := make([]domain.SessionRecord, 0, len(recs))
	ids := make([]domain.SessionID, 0, len(recs))
	for _, rec := range recs {
		if rec.IsTerminated || rec.IsTaskPreparation || rec.HibernatedAt != nil || rec.ProvisionState.WithDefault() != domain.SessionProvisionReady {
			continue
		}
		candidates = append(candidates, rec)
		ids = append(ids, rec.ID)
	}
	if len(candidates) == 0 {
		return
	}
	// Reserve every candidate before starting the bounded worker pool. Otherwise
	// sessions queued behind slow probes remain open to input and the periodic
	// reaper until a worker dequeues them.
	acquired, err := m.beginAgentOperations(ctx, ids, agentOperationReconcile)
	if err != nil {
		for _, rec := range candidates {
			m.finishStatusRecovery(ctx, rec, err)
		}
		m.logger.Warn("reconcile: could not fence live sessions", "error", err)
		return
	}
	acquiredSet := make(map[domain.SessionID]struct{}, len(acquired))
	for _, id := range acquired {
		acquiredSet[id] = struct{}{}
	}
	live := make([]domain.SessionRecord, 0, len(acquired))
	for _, rec := range candidates {
		if _, ok := acquiredSet[rec.ID]; !ok {
			m.finishStatusRecovery(ctx, rec, ErrResumeInProgress)
			m.logger.Warn("reconcile: session remains input-gated pending unambiguous recovery", "sessionID", rec.ID)
			continue
		}
		live = append(live, rec)
	}
	if len(live) == 0 {
		return
	}
	m.markReconcileQueued(live)
	workers := m.reconcileWorkers
	if workers > len(live) {
		workers = len(live)
	}
	jobs := make(chan domain.SessionRecord)
	var wg sync.WaitGroup
	var retryMu sync.Mutex
	var retryChats, resumeChats []domain.SessionID
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for rec := range jobs {
				if !m.startQueuedReconcile(rec.ID) {
					// A Kill took the queued slot; nothing left to verify.
					m.finishStatusRecovery(ctx, rec, nil)
					continue
				}
				m.beginStatusRecovery(rec.ID)
				err := func() error {
					defer m.endAgentOperation(rec.ID, agentOperationReconcile)
					recoveryCtx, cancel := context.WithTimeout(ctx, m.statusVerificationLimit)
					defer cancel()
					return m.checkSessionHealth(recoveryCtx, rec)
				}()
				exited := errors.Is(err, errStartupChatExited)
				if exited {
					err = nil
				}
				retry := err != nil && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat &&
					retryableStartupChatCheck(err) && len(m.startupChatRetryDelays) > 0
				if !retry {
					// A scheduled reattach keeps the session checking; its last attempt settles it.
					m.finishStatusRecovery(ctx, rec, err)
				}
				if err != nil {
					m.logger.Error("reconcile: live pass failed, skipping", "sessionID", rec.ID, "retrying", retry, "error", err)
				}
				if retry || exited {
					retryMu.Lock()
					if retry {
						retryChats = append(retryChats, rec.ID)
					} else {
						resumeChats = append(resumeChats, rec.ID)
					}
					retryMu.Unlock()
				}
			}
		}()
	}
	for _, rec := range live {
		jobs <- rec
	}
	close(jobs)
	wg.Wait()
	if len(retryChats) > 0 && ctx.Err() == nil {
		go m.retryStartupChatChecks(ctx, retryChats)
	}
	if len(resumeChats) > 0 && ctx.Err() == nil {
		m.resumeStartupChats(ctx, resumeChats)
	}
}
