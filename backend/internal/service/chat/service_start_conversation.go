package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// openConversation opens or creates the conversation row and repairs an interrupted edit.
func (s *Service) openConversation(ctx context.Context, l *launch) error {
	var err error
	l.scope = domain.ConversationScopeSession
	if l.cfg.Kind == domain.KindOrchestrator {
		l.scope = domain.ConversationScopeProject
		// A laned orchestrator is its own chat: it must not adopt the
		// project's default orchestrator conversation.
		if ls, ok := s.store.(ports.LaneReader); ok {
			sl, laneErr := ls.SessionLane(ctx, l.cfg.SessionID)
			if laneErr != nil {
				return laneErr
			}
			if sl.LaneItemID != "" {
				l.scope = domain.ConversationScopeSession
			}
		}
	}
	now := s.now()
	var resetBoundary domain.ConversationActivity
	l.freshProjectContext = l.scope == domain.ConversationScopeProject &&
		l.cfg.ProviderConversationID == "" && l.cfg.ProviderSwitch == nil
	if l.freshProjectContext {
		detail, marshalErr := json.Marshal(map[string]string{
			"event":  "context.reset",
			"reason": "native conversation was unavailable",
		})
		if marshalErr != nil {
			return fmt.Errorf("encode fresh context boundary: %w", marshalErr)
		}
		resetBoundary = domain.ConversationActivity{
			ID:             s.newID(),
			Kind:           domain.ActivityKindSystem,
			Status:         domain.ActivityStatusCompleted,
			Summary:        "Agent context reset.",
			Detail:         detail,
			ProviderItemID: domain.ConversationContextResetProviderItemID(l.cfg.SessionID),
		}
	}
	conversationID := s.newID()
	if l.owner.Kind == domain.ConversationOwnerReview {
		if l.cfg.ProviderHandoff != nil || l.owner.ID == "" {
			return errors.New("reviewer chat does not support provider handoff")
		}
		l.conversation, err = s.store.CreateReviewConversation(ctx, conversationID, l.owner.ID, l.cfg.ProjectID, l.cfg.SessionID, now)
	} else if l.cfg.ProviderHandoff != nil {
		// Read the observed owner without rebinding it. Provider I/O can fail;
		// ownership changes only with the prepared history's lifecycle commit.
		handoff := l.cfg.ProviderHandoff
		l.conversation, err = s.store.ConversationForSession(ctx, handoff.PreviousSessionID)
		if err == nil && (l.conversation.ID != handoff.ConversationID ||
			l.conversation.ActiveBranchID != handoff.PreviousBranchID || l.conversation.LatestSequence != handoff.PreviousSequence) {
			err = errors.New("native Chat handoff conversation changed")
		}
		if err == nil && handoff.PreviousSessionID != l.cfg.SessionID {
			// A different session's provider reported this state; the commit
			// clears it, and the new controller must not be seeded with it.
			l.conversation.MCPServers, l.conversation.ThreadState = nil, nil
		}
	} else if providerSwitch := l.cfg.ProviderSwitch; providerSwitch != nil {
		l.conversation, err = s.store.ConversationForSession(ctx, l.cfg.SessionID)
		if err == nil && (l.conversation.ID != providerSwitch.ConversationID ||
			l.conversation.ActiveBranchID != providerSwitch.SourceBranchID ||
			l.conversation.LatestSequence != providerSwitch.SourceSequence) {
			err = errors.New("provider switch conversation changed")
		}
		if err == nil {
			// The source provider's reports do not describe the target; seeding
			// the controller with them would persist them again.
			l.conversation.MCPServers, l.conversation.ThreadState = nil, nil
		}
	} else if l.freshProjectContext && l.firstController {
		// What the user typed while this orchestrator provisioned waits in a
		// session-scoped row. It moves to the project conversation with the rebind
		// (#6523); a later controller must not replay it.
		l.conversation, err = s.store.CreateProjectConversationAdoptingQueue(
			ctx, conversationID, l.cfg.ProjectID, l.cfg.SessionID, resetBoundary, now)
	} else if l.freshProjectContext {
		l.conversation, err = s.store.CreateProjectConversationWithContextReset(
			ctx, conversationID, l.cfg.ProjectID, l.cfg.SessionID, resetBoundary, now)
	} else if l.scope == domain.ConversationScopeProject &&
		l.cfg.ProviderConversationID != "" && l.cfg.ProviderScopeID != "" {
		// A coordinator-reserved provider boundary is an ownership transfer, not
		// permission to steal a project narrative from a newer orchestrator.
		// Require the durable current_session_id proof the coordinator observed;
		// ControllerReady/CommitChatSpawn rechecks it after provider I/O.
		l.conversation, err = s.store.ConversationForSession(ctx, l.cfg.SessionID)
	} else if l.cfg.ProviderConversationID != "" {
		l.conversation, err = s.store.OpenNativeConversation(
			ctx, conversationID, l.scope, l.cfg.ProjectID, l.cfg.SessionID, now)
	} else {
		l.conversation, err = s.store.CreateConversation(
			ctx, conversationID, l.scope, l.cfg.ProjectID, l.cfg.SessionID, now)
	}
	if err != nil {
		return fmt.Errorf("open conversation: %w", err)
	}
	var repairedBranch domain.ConversationBranch
	var restoredProviderOwner bool
	if l.cfg.ProviderHandoff == nil && l.cfg.ProviderSwitch == nil && l.owner.Kind != domain.ConversationOwnerReview {
		repairedBranch, restoredProviderOwner, err = s.store.RepairIncompleteConversationEdit(
			ctx, l.cfg.SessionID, l.conversation.ID, s.now())
		if err != nil {
			return fmt.Errorf("repair incomplete conversation edit: %w", err)
		}
	}
	if repairedBranch.ID != "" {
		l.conversation.ActiveBranchID = repairedBranch.ID
		// An ordinary restart was handed the abandoned child's provider handle.
		// A coordinator-supplied scope belongs to a pending provider boundary (for
		// example, agent switching) and must keep its independently reserved handle.
		if restoredProviderOwner && l.cfg.ProviderScopeID == "" {
			l.cfg.ProviderConversationID = repairedBranch.ProviderConversationID
		}
	}
	return nil
}

// resolveProviderScope loads the active branch and decides the provider scope and boundary.
func (s *Service) resolveProviderScope(ctx context.Context, l *launch) error {
	var err error
	l.activeBranch, err = s.store.ConversationBranch(ctx, l.conversation.ID, l.conversation.ActiveBranchID)
	if err != nil {
		return fmt.Errorf("load active conversation branch: %w", err)
	}
	l.providerScopeID = l.activeBranch.ProviderScopeID
	l.providerHandleOwnedByActiveBranch = true
	if l.cfg.ProviderScopeID != "" {
		l.providerScopeID = l.cfg.ProviderScopeID
		l.providerHandleOwnedByActiveBranch = l.providerScopeID == l.activeBranch.ProviderScopeID
	}
	// A running provider turn also proves dispatch; queued-only intake must
	// survive a failed first drain.
	l.queuedBeforeFirstController = false
	l.preserveUndispatchedQueue = false
	waking := s.isWaking(l.cfg.SessionID)
	// A session whose move to a new harness release failed kept its queued
	// messages for this start, the way a wake does (see versionSwitchFailed).
	if l.sessionFound && l.sessionRecord.StaleBinary != nil && l.sessionRecord.StaleBinary.SwitchFailedAt != nil {
		waking = true
	}
	hadProviderHistory := false
	if l.sessionFound && (l.sessionRecord.ProvisionState.IsProvisioning() || waking) {
		hadProviderHistory = l.sessionRecord.Metadata.ProviderConversationID != ""
		running, listErr := s.store.ListVisibleRunningTurnProviderIDs(ctx, l.conversation.ID)
		if listErr != nil {
			return fmt.Errorf("read running chat turns before start: %w", listErr)
		}
		l.queuedBeforeFirstController = l.firstController && len(running) == 0
		l.preserveUndispatchedQueue = len(running) == 0 && (l.sessionRecord.ProvisionState.IsProvisioning() || waking)
	}
	l.providerBoundaryID = ""
	if l.cfg.ProviderSwitch != nil {
		l.providerBoundaryID = s.newID()
		l.providerScopeID = l.providerBoundaryID
		l.providerHandleOwnedByActiveBranch = false
	} else if !l.providerHandleOwnedByActiveBranch {
		l.providerBoundaryID = l.providerScopeID
	} else if l.cfg.ProviderConversationID == "" && l.cfg.ProviderScopeID == "" &&
		// An inherited branch belongs to the previous orchestrator even if it
		// never recorded a provider thread. This session's queued intake is new.
		(l.activeBranch.SessionID != l.cfg.SessionID || l.activeBranch.ProviderConversationID != "" || hadProviderHistory ||
			(!l.preserveUndispatchedQueue && l.conversation.LatestSequence > 0)) {
		// This conversation already owns provider history, but the caller proved it
		// cannot resume that provider thread. Reserve the next provider boundary
		// before connect so every opaque id emitted by the fresh process is born in
		// the same namespace that ControllerReady will durably publish.
		l.providerBoundaryID = s.newID()
		l.providerScopeID = l.providerBoundaryID
		l.providerHandleOwnedByActiveBranch = false
	}
	if l.cfg.ProviderConversationID != "" && l.providerHandleOwnedByActiveBranch {
		if l.activeBranch.ProviderConversationID != "" &&
			l.activeBranch.ProviderConversationID != l.cfg.ProviderConversationID {
			return fmt.Errorf("active conversation branch provider handle %q does not match session handle %q",
				l.activeBranch.ProviderConversationID, l.cfg.ProviderConversationID)
		}
	}
	return nil
}

// applyConversationSettings restores or records the durable conversation settings.
func (s *Service) applyConversationSettings(ctx context.Context, l *launch) error {
	s.applyLaunchCompaction(ctx, l.cfg, &l.conversation)
	// Conversation settings are durable user choices. Restore the selected
	// model when rebuilding a controller after a daemon restart; the caller's
	// session default must not silently replace it.
	if l.cfg.ProviderConversationID != "" && l.conversation.Settings.Model != "" {
		l.cfg.Model = l.conversation.Settings.Model
	}
	if l.cfg.ProviderConversationID != "" {
		l.cfg.Effort = l.conversation.Settings.ReasoningEffort
	}
	if l.cfg.ProviderConversationID != "" && l.conversation.Settings.ApprovalMode != "" {
		l.cfg.Permissions = l.conversation.Settings.ApprovalMode
		// The fence was admitted for the caller's mode; the stored mode is
		// the one the provider resumes in.
		if !l.cfg.ReconnectOnly {
			if err := admitChatFence(l.driver, l.cfg.Harness, l.cfg.Policy, l.cfg.Permissions); err != nil {
				return err
			}
		}
	}
	if l.cfg.ProviderConversationID == "" {
		if err := capabilityAdmissionError(l.cfg.Harness, l.caps, l.cfg.Permissions, l.cfg.Policy); err != nil {
			return err
		}
		l.conversation.Settings.Model = l.cfg.Model
		l.conversation.Settings.ReasoningEffort = l.cfg.Effort
		l.conversation.Settings.ApprovalMode = l.cfg.Permissions
		// A switch target's settings are saved by the switch's commit.
		if l.cfg.ProviderSwitch == nil {
			if err := s.store.SetConversationSettings(ctx, l.conversation.ID, l.conversation.Settings, s.now()); err != nil {
				return fmt.Errorf("record initial conversation settings: %w", err)
			}
		}
	}
	return nil
}

// prepareLaunchEnv builds the lazy controller-environment hook and provider system prompt.
func (s *Service) prepareLaunchEnv(ctx context.Context, l *launch) error {
	if l.cfg.PrepareControllerEnv != nil {
		cfg := l.cfg
		l.prepareEnv = func(prepareCtx context.Context) (map[string]string, error) {
			env, prepareErr := cfg.PrepareControllerEnv(prepareCtx, cfg.ExpectedControllerOwner, cfg.Harness)
			if prepareErr != nil {
				return nil, fmt.Errorf("prepare chat controller environment: %w", prepareErr)
			}
			return env, nil
		}
	}

	// A provider that took over through a switch keeps its handoff note for the
	// life of its branch: the switch supplies it, a later restart reads it back.
	handoffNote := l.activeBranch.HandoffNote
	if l.cfg.ProviderSwitch != nil {
		handoffNote = l.cfg.ProviderSwitch.Bootstrap
	}
	l.providerSystemPrompt = withHandoffNote(l.cfg.SystemPrompt, handoffNote)
	return nil
}
