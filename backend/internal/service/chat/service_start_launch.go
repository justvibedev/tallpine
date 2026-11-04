package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// admitStart validates the request and consumes the awake-reconnect proof and provider-handoff reservation.
func (s *Service) admitStart(_ context.Context, l *launch) error {
	if target := l.cfg.AwakeReconnectTarget; target != nil {
		s.mu.Lock()
		candidate, exists := s.awakeInterrupted[l.owner]
		old, hasConfig := s.startConfigs[l.owner]
		valid := l.cfg.ReconnectOnly && !l.cfg.FreshIfProviderConversationMissing && l.owner.Kind == domain.ConversationOwnerSession && target.SessionID == l.cfg.SessionID && target.Owner == l.cfg.ExpectedControllerOwner && exists && candidate == *target && hasConfig && old.ExpectedControllerOwner == target.Owner && s.ownerControllers[l.owner] == nil
		if valid {
			delete(s.awakeInterrupted, l.owner)
		}
		s.mu.Unlock()
		if !valid {
			return fmt.Errorf("%w: interrupted attachment proof expired or was explicitly stopped", ports.ErrChatRecoveryInconclusive)
		}
	}
	if l.cfg.HistoryMode > ports.ChatHistoryDeferred {
		return errors.New("invalid Chat history mode")
	}
	if handoff := l.cfg.ProviderHandoff; handoff != nil {
		if handoff.BoundaryID == "" || l.cfg.ProviderConversationID == "" || l.cfg.ControllerReady == nil ||
			(l.cfg.HistoryMode == ports.ChatHistoryDeferred) || (l.cfg.ProviderScopeID != "" && l.cfg.ProviderScopeID != handoff.BoundaryID) {
			return errors.New("incomplete native Chat handoff reservation")
		}
		l.cfg.ProviderScopeID = handoff.BoundaryID
		l.cfg.HistoryMode = ports.ChatHistoryRequired
	}
	return nil
}

// loadReplayCheckpoint reads the durable native-history checkpoint when history replay is required.
func (s *Service) loadReplayCheckpoint(ctx context.Context, l *launch) error {
	if l.cfg.HistoryMode == ports.ChatHistoryRequired {
		if s.sessions == nil {
			return errors.New("native history replay requires a session reader")
		}
		rec, found, err := s.sessions.GetSession(ctx, l.cfg.SessionID)
		if err != nil {
			return fmt.Errorf("read native history checkpoint: %w", err)
		}
		if !found {
			return ports.ErrSessionNotFound
		}
		l.nativeEvidence = rec.Metadata.NativeCheckpointEvidence
		l.replayCheckpoint.latestUserPromptAt = rec.Metadata.LatestUserPromptAt
		l.replayCheckpoint.latestAssistantUpdateAt = rec.Metadata.LatestAssistantUpdateAt
		checkpointState := rec.Metadata.ConversationCheckpointState
		if checkpointState == "" {
			checkpointState = domain.ConversationCheckpointLegacy
		}
		trustedProvenance := checkpointState.Trusted() &&
			rec.Metadata.ConversationCheckpointGeneration != "" &&
			rec.Metadata.ConversationCheckpointNativeID != ""
		trusted := trustedProvenance &&
			rec.Metadata.ConversationCheckpointNativeID == l.cfg.ProviderConversationID
		if rec.Harness == domain.HarnessClaudeCode && trustedProvenance && l.nativeEvidence == "" {
			l.replayCheckpoint.hardMismatches = append(l.replayCheckpoint.hardMismatches, ports.ChatHistoryMismatchUnsettledBoundary)
		}
		if rec.Metadata.ConversationCheckpointUnsettled ||
			(checkpointState == domain.ConversationCheckpointComplete &&
				strings.TrimSpace(rec.Metadata.LatestUserPrompt) == "" &&
				strings.TrimSpace(rec.Metadata.LatestAssistantUpdate) != "") {
			// A Stop without its prompt/turn identity cannot be matched safely by
			// text: two turns may have identical answers. This hard gate is never
			// waived by provider-history recovery.
			l.replayCheckpoint.hardMismatches = append(l.replayCheckpoint.hardMismatches,
				ports.ChatHistoryMismatchUnsettledBoundary)
		}
		switch {
		case trustedProvenance && !trusted:
			// A trusted checkpoint belongs to one exact provider-native thread.
			// Explicit provider-history consent can waive ambiguous legacy text,
			// never an identity boundary such as /clear selecting a new thread.
			l.replayCheckpoint.hardMismatches = append(l.replayCheckpoint.hardMismatches,
				ports.ChatHistoryMismatchNativeIdentity)
		case trusted:
			l.replayCheckpoint.providerTurnID = rec.Metadata.ConversationCheckpointTurnID
			l.replayCheckpoint.latestUserPrompt = strings.TrimSpace(rec.Metadata.LatestUserPrompt)
			l.replayCheckpoint.userMismatch = ports.ChatHistoryMismatchTrustedUserText
			if checkpointState == domain.ConversationCheckpointComplete {
				l.replayCheckpoint.completedUserPrompt = true
				l.replayCheckpoint.latestAssistantUpdate = strings.TrimSpace(rec.Metadata.LatestAssistantUpdate)
				l.replayCheckpoint.assistantMismatch = ports.ChatHistoryMismatchTrustedAssistantText
			}
		case l.cfg.HistoryPolicy != domain.SessionInterfaceTransitionHistoryProvider:
			// Coordination does not erase preceding human text. Its old state
			// format did not preserve that text's trust; like legacy provenance,
			// bypassing it requires explicit provider-history consent.
			l.replayCheckpoint.latestUserPrompt = strings.TrimSpace(rec.Metadata.LatestUserPrompt)
			l.replayCheckpoint.latestAssistantUpdate = strings.TrimSpace(rec.Metadata.LatestAssistantUpdate)
			l.replayCheckpoint.userMismatch = ports.ChatHistoryMismatchUntrustedUserText
			l.replayCheckpoint.assistantMismatch = ports.ChatHistoryMismatchUntrustedAssistantText
		}
	}
	return nil
}

// resolveDriver resolves the harness driver and the resume/reconnect entry point.
func (s *Service) resolveDriver(ctx context.Context, l *launch) error {
	var err error
	l.driver, err = s.drivers.Driver(l.cfg.Harness)
	if err != nil {
		return fmt.Errorf("chat driver for %s: %w", l.cfg.Harness, err)
	}
	// Reattaching to a host that is already running starts nothing: running
	// sessions keep the decision they launched with until their next launch.
	if !l.cfg.ReconnectOnly {
		if err := admitChatFence(l.driver, l.cfg.Harness, l.cfg.Policy, l.cfg.Permissions); err != nil {
			return err
		}
	}
	l.resume = l.driver.Resume
	if l.cfg.ReconnectOnly {
		reconnector, ok := l.driver.(ports.ChatDriverReconnector)
		if !ok || l.cfg.ProviderConversationID == "" {
			return ports.ErrChatHostNotRunning
		}
		l.resume = reconnector.Reconnect
	}
	return nil
}

// verifyNativeEvidence proves retained native checkpoint evidence against the provider.
func (s *Service) verifyNativeEvidence(ctx context.Context, l *launch) error {
	if l.nativeEvidence != "" {
		for _, mismatch := range l.replayCheckpoint.hardMismatches {
			if mismatch == ports.ChatHistoryMismatchNativeIdentity {
				return &ports.ChatHistoryUnsettledError{Dimensions: l.replayCheckpoint.hardMismatches}
			}
		}
		verifier, ok := l.driver.(ports.NativeCheckpointVerifier)
		if !ok {
			return &ports.ChatHistoryUnsettledError{Dimensions: []ports.ChatHistoryMismatchDimension{ports.ChatHistoryMismatchUnsettledBoundary}}
		}
		boundary, verifyErr := verifyNativeCheckpoint(ctx, verifier, ports.NativeCheckpointRequest{
			ProviderConversationID: l.cfg.ProviderConversationID, Env: l.cfg.Env, Evidence: l.nativeEvidence,
		})
		if verifyErr != nil {
			return verifyErr
		}
		// The provider proved every retained observation against exact native
		// ancestry. Replace the hook-inferred text pair, not Tallpine's high-water gate
		// (which is populated later from the durable conversation).
		verified := nativeHistoryCheckpoint{nativeBoundary: &boundary}
		// Native Stop evidence does not waive a legacy/coordination prompt that
		// strict policy still requires. Only explicit provider-history consent
		// may drop those untrusted text dimensions.
		if l.replayCheckpoint.userMismatch == ports.ChatHistoryMismatchUntrustedUserText {
			verified.latestUserPrompt = l.replayCheckpoint.latestUserPrompt
			verified.latestAssistantUpdate = l.replayCheckpoint.latestAssistantUpdate
			verified.userMismatch = l.replayCheckpoint.userMismatch
			verified.assistantMismatch = l.replayCheckpoint.assistantMismatch
		}
		l.replayCheckpoint = verified
	}
	return nil
}

// checkInitialCapabilities admits a fresh start against the driver capabilities.
func (s *Service) checkInitialCapabilities(ctx context.Context, l *launch) error {
	var err error
	if l.cfg.ProviderConversationID == "" {
		l.caps, err = s.driverCapabilities(ctx, l.cfg.Harness, l.driver)
		if err != nil {
			return err
		}
		if err := capabilityAdmissionError(l.cfg.Harness, l.caps, l.cfg.Permissions, l.cfg.Policy); err != nil {
			return err
		}
	}
	return nil
}

// readSessionForStart reads the session once for every first-controller decision.
func (s *Service) readSessionForStart(ctx context.Context, l *launch) error {
	// One read of the session serves every first-controller decision below. A
	// provisioning retry is not necessarily the first controller: only durable
	// controller ownership or provider history proves work was dispatched.
	l.sessionFound = false
	if s.sessions != nil {
		var readErr error
		l.sessionRecord, l.sessionFound, readErr = s.sessions.GetSession(ctx, l.cfg.SessionID)
		if readErr != nil {
			return fmt.Errorf("read chat session before start: %w", readErr)
		}
	}
	l.firstController = l.sessionFound && l.sessionRecord.ProvisionState.IsProvisioning() &&
		l.sessionRecord.Metadata.ControllerGeneration == "" && l.sessionRecord.Metadata.ProviderConversationID == ""
	return nil
}
