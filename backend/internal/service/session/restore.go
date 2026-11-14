package session

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// Restore relaunches a terminated session and returns the API-facing read model.
func (s *Service) Restore(ctx context.Context, id domain.SessionID) (RestoreOutcome, error) {
	if err := s.refuseEndedLaneRestore(ctx, id); err != nil {
		return RestoreOutcome{}, err
	}
	res, err := s.manager.RestoreWithMode(ctx, id)
	if err != nil {
		apiErr := toAPIError(err)
		s.recordRestored(ctx, id, domain.SessionRecord{}, actionStatusForError(err), apiErr)
		return RestoreOutcome{}, apiErr
	}
	session, err := s.toSession(ctx, res.Session)
	if err != nil {
		return RestoreOutcome{}, err
	}
	s.recordRestored(ctx, id, res.Session, domain.ActivityStatusCompleted, nil)
	return RestoreOutcome{Session: session, Mode: restoreModeView(res.Mode)}, nil
}

// ExitAgent stops only the agent controller while preserving the Tallpine session,
// worktree, terminal identity, and provider-native conversation.
func (s *Service) ExitAgent(ctx context.Context, id domain.SessionID) (ExitAgentOutcome, error) {
	manager, ok := s.manager.(exitAgentCommander)
	if !ok {
		return ExitAgentOutcome{}, apierr.Conflict(
			"AGENT_EXIT_UNSUPPORTED", "This build cannot exit an agent independently", nil)
	}
	rec, err := manager.ExitAgent(ctx, id)
	if err != nil {
		return ExitAgentOutcome{}, toAPIError(err)
	}
	session, err := s.toSession(ctx, rec)
	if err != nil {
		return ExitAgentOutcome{}, err
	}
	return ExitAgentOutcome{Session: session}, nil
}

// ResumeAgent relaunches an exited agent without restoring a terminated
// session or recreating its workspace.
func (s *Service) ResumeAgent(ctx context.Context, id domain.SessionID) (ResumeAgentOutcome, error) {
	res, err := s.manager.ResumeAgentWithMode(ctx, id)
	if err != nil {
		return ResumeAgentOutcome{}, toAPIError(err)
	}
	session, err := s.toSession(ctx, res.Session)
	if err != nil {
		return ResumeAgentOutcome{}, err
	}
	return ResumeAgentOutcome{Session: session, Mode: restoreModeView(res.Mode)}, nil
}

// StartAccountTransition moves the session's agent to another provider account
// through the durable controller handoff. stopTurn cancels a turn in progress;
// otherwise the move waits for it to finish.
func (s *Service) StartAccountTransition(ctx context.Context, id domain.SessionID, accountID string, stopTurn bool) (domain.SessionInterfaceTransition, error) {
	manager, ok := s.manager.(agentAccountCommander)
	if !ok {
		return domain.SessionInterfaceTransition{}, apierr.Conflict(
			"AGENT_ACCOUNT_UNSUPPORTED", "This build cannot move a session to another account", nil)
	}
	policy := domain.SessionInterfaceTransitionDrain
	if stopTurn {
		policy = domain.SessionInterfaceTransitionInterrupt
	}
	transition, err := manager.StartAccountTransition(ctx, id, accountID, policy)
	return transition, toAPIError(err)
}

// InterfaceTransitionStatus returns capability and progress without launching
// a provider process or mutating the session.
func (s *Service) InterfaceTransitionStatus(ctx context.Context, id domain.SessionID) (InterfaceTransitionStatus, error) {
	manager, ok := s.manager.(interfaceTransitionCommander)
	if !ok {
		return InterfaceTransitionStatus{}, apierr.Conflict(
			"INTERFACE_HANDOFF_UNSUPPORTED", "This build cannot switch session interfaces", nil)
	}
	status, err := manager.InterfaceTransitionStatus(ctx, id)
	if err != nil {
		return InterfaceTransitionStatus{}, toAPIError(err)
	}
	return InterfaceTransitionStatus{
		Supported: status.Supported, TargetMode: status.TargetMode,
		ReasonCode: status.ReasonCode, Reason: status.Reason,
		Transition: status.Transition,
	}, nil
}

// StartInterfaceTransition begins a durable, asynchronous controller handoff.
func (s *Service) StartInterfaceTransition(
	ctx context.Context,
	id domain.SessionID,
	target domain.SessionMode,
	policy domain.SessionInterfaceTransitionPolicy,
	historyPolicy domain.SessionInterfaceTransitionHistoryPolicy,
) (domain.SessionInterfaceTransition, error) {
	if !target.Valid() {
		return domain.SessionInterfaceTransition{}, apierr.Invalid(
			"INVALID_SESSION_MODE", "Target mode must be chat or tui", nil)
	}
	if !policy.Valid() {
		return domain.SessionInterfaceTransition{}, apierr.Invalid(
			"INVALID_TRANSITION_POLICY", "Policy must be drain or interrupt", nil)
	}
	if !historyPolicy.Valid() {
		return domain.SessionInterfaceTransition{}, apierr.Invalid(
			"INVALID_TRANSITION_HISTORY_POLICY", "History policy must be strict or provider_history", nil)
	}
	manager, ok := s.manager.(interfaceTransitionCommander)
	if !ok {
		return domain.SessionInterfaceTransition{}, apierr.Conflict(
			"INTERFACE_HANDOFF_UNSUPPORTED", "This build cannot switch session interfaces", nil)
	}
	transition, err := manager.StartInterfaceTransition(ctx, id, target, policy, historyPolicy)
	return transition, toAPIError(err)
}

// CancelInterfaceTransition cancels a handoff while its source controller is
// still safe to reopen.
func (s *Service) CancelInterfaceTransition(ctx context.Context, id domain.SessionID) error {
	manager, ok := s.manager.(interfaceTransitionCommander)
	if !ok {
		return apierr.Conflict(
			"INTERFACE_HANDOFF_UNSUPPORTED", "This build cannot switch session interfaces", nil)
	}
	return toAPIError(manager.CancelInterfaceTransition(ctx, id))
}

// AcknowledgeInterfaceTransitionNotice durably dismisses one terminal failure
// or recovery notice while retaining the transition record for diagnostics.
func (s *Service) AcknowledgeInterfaceTransitionNotice(
	ctx context.Context,
	id domain.SessionID,
	transitionID string,
) (domain.SessionInterfaceTransition, error) {
	manager, ok := s.manager.(interfaceTransitionCommander)
	if !ok {
		return domain.SessionInterfaceTransition{}, apierr.Conflict(
			"INTERFACE_HANDOFF_UNSUPPORTED", "This build cannot switch session interfaces", nil)
	}
	transition, err := manager.AcknowledgeInterfaceTransitionNotice(ctx, id, transitionID)
	return transition, toAPIError(err)
}

func restoreModeView(mode sessionmanager.RestoreMode) RestoreModeView {
	switch mode {
	case sessionmanager.RestoreModeNative:
		return RestoreModeViewNative
	case sessionmanager.RestoreModeSavedPrompt:
		return RestoreModeViewSavedPrompt
	case sessionmanager.RestoreModeFresh:
		return RestoreModeViewFresh
	default:
		return RestoreModeView(mode)
	}
}

// Kill delegates terminal intent and teardown to the internal manager.
func (s *Service) Kill(ctx context.Context, id domain.SessionID) (bool, error) {
	s.cancelTitleRefinement(id)
	freed, err := s.manager.Kill(ctx, id)
	if err != nil {
		apiErr := toAPIError(err)
		s.recordTerminated(ctx, id, actionStatusForError(err), apiErr)
		return freed, apiErr
	}
	s.recordTerminated(ctx, id, domain.ActivityStatusCompleted, nil)
	return freed, nil
}

// RequestKill acknowledges terminal intent without waiting for cleanup scripts.
func (s *Service) RequestKill(ctx context.Context, id domain.SessionID) (sessionmanager.KillResult, error) {
	s.cancelTitleRefinement(id)
	result, err := s.manager.RequestKill(ctx, id)
	if err != nil {
		apiErr := toAPIError(err)
		s.recordTerminated(ctx, id, actionStatusForError(err), apiErr)
		return result, apiErr
	}
	// RequestKill can acknowledge terminal intent while cleanup still runs.
	// A later cleanup failure must not leave a completed action in chat.
	if !result.CleanupPending {
		s.recordTerminated(ctx, id, domain.ActivityStatusCompleted, nil)
	}
	return result, nil
}

// artifactDeleter deletes a session's artifact files and version rows. The
// production implementation is *artifactversions.Capturer.
type artifactDeleter interface {
	DeleteSession(ctx context.Context, id domain.SessionID) error
}

// DeleteArtifacts deletes a terminated session's artifact files, version
// history included, and its version rows, then reconciles its OutputType. The
// directory itself is kept, so a restored session starts with an empty one.
// Only the user's archive choice calls this; kill, cleanup and the reaper
// never delete artifacts.
func (s *Service) DeleteArtifacts(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return toAPIError(err)
	}
	if !ok {
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if !rec.IsTerminated {
		return apierr.Conflict("SESSION_NOT_TERMINATED", "Archive the session before deleting its artifacts.", nil)
	}
	if s.artifactDeleter == nil {
		return fmt.Errorf("delete artifacts %s: no artifact deleter", id)
	}
	// The deleter only touches the session's managed directory (canonical or
	// legacy flat). A row whose files live elsewhere (written under another data
	// dir) is refused rather than reported deleted while its files stay where
	// the UI reads them.
	if stored := rec.Metadata.ArtifactDir; !sessionartifacts.IsManaged(s.dataDir, id, stored) {
		return fmt.Errorf("delete artifacts %s: artifact dir %q is outside the data dir", id, stored)
	}
	err = s.artifactDeleter.DeleteSession(ctx, id)
	// Reconcile even after a failure: the files may already be gone while the
	// version rows were not dropped, and OutputType must follow what is on disk.
	if s.outputTypeReconciler != nil {
		if reconcileErr := s.outputTypeReconciler.ReconcileSessionOutputType(ctx, id); reconcileErr != nil && s.logger != nil {
			s.logger.Warn("reconcile output type after deleting artifacts", "session", id, "err", reconcileErr)
		}
	}
	return err
}

// RollbackSpawn deletes a seed-state session row, or falls back to a Kill if
// the session has spawn output. Used by the CLI to undo a `spawn --claim-pr`
// when the claim step fails, avoiding the orphan terminated row that a plain
// Kill would leave behind.
func (s *Service) RollbackSpawn(ctx context.Context, id domain.SessionID) (RollbackOutcome, error) {
	s.cancelTitleRefinement(id)
	deleted, killed, err := s.manager.RollbackSpawn(ctx, id)
	if err != nil {
		return RollbackOutcome{}, toAPIError(err)
	}
	return RollbackOutcome{Deleted: deleted, Killed: killed}, nil
}
