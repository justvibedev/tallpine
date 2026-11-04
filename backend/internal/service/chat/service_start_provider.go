package chat

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// openProvider resumes or starts the provider conversation.
func (s *Service) openProvider(ctx context.Context, l *launch) error {
	var err error
	hostID := providerHostID(*l.cfg)
	l.freshIfMissing = l.cfg.FreshIfProviderConversationMissing
	if l.cfg.ProviderConversationID != "" {
		// A target that has not answered yet still needs its standing context, though
		// the source's turns come before it.
		unanswered := s.switchBranchUnanswered(ctx, l.conversation.ID, l.activeBranch)
		l.freshIfMissing = l.freshIfMissing || unanswered
		l.conv, err = l.resume(ctx, ports.ChatResumeConfig{
			Policy:                 l.cfg.Policy,
			SessionID:              hostID,
			ProviderConversationID: l.cfg.ProviderConversationID,
			FreshIfMissing:         l.freshIfMissing,
			HasPriorTurns:          l.conversation.LatestSequence > 0 && !unanswered,
			DataDir:                l.cfg.DataDir,
			WorkspacePath:          l.cfg.WorkspacePath,
			Env:                    l.cfg.Env,
			PrepareEnv:             l.prepareEnv,
			Model:                  l.cfg.Model,
			Effort:                 l.cfg.Effort,
			Permissions:            l.cfg.Permissions,
			ReadOnly:               l.cfg.ReadOnly,
			SystemPrompt:           l.providerSystemPrompt,
			ProviderScopeID:        l.providerScopeID,
			ProviderIDsScoped:      l.providerBoundaryID != "" || l.activeBranch.ProviderIDsScoped,
			AdditionalDirectories:  l.cfg.AdditionalDirectories,
			MCPServers:             l.cfg.MCPServers,
		})
	} else {
		l.conv, err = l.driver.Start(ctx, ports.ChatStartConfig{
			Policy:                l.cfg.Policy,
			ProviderIDsScoped:     l.providerBoundaryID != "" || l.activeBranch.ProviderIDsScoped,
			SessionID:             hostID,
			DataDir:               l.cfg.DataDir,
			WorkspacePath:         l.cfg.WorkspacePath,
			Env:                   l.cfg.Env,
			PrepareEnv:            l.prepareEnv,
			Model:                 l.cfg.Model,
			Effort:                l.cfg.Effort,
			Permissions:           l.cfg.Permissions,
			ReadOnly:              l.cfg.ReadOnly,
			SystemPrompt:          l.providerSystemPrompt,
			ProviderScopeID:       l.providerScopeID,
			AdditionalDirectories: l.cfg.AdditionalDirectories,
			MCPServers:            l.cfg.MCPServers,
		})
	}
	if err != nil {
		return err
	}
	return nil
}

// admitOpenedProvider validates the opened provider against the request. Failure paths roll the provider back.
func (s *Service) admitOpenedProvider(ctx context.Context, l *launch) error {
	var err error
	l.liveReconnect = false
	if reconnected, ok := l.conv.(ports.ChatLiveReconnector); ok {
		l.liveReconnect = reconnected.ReconnectedLive()
	}
	freshReplacement := false
	if l.cfg.ProviderConversationID != "" &&
		l.conv.ProviderConversationID() != l.cfg.ProviderConversationID {
		returned := l.conv.ProviderConversationID()
		if !l.freshIfMissing || l.liveReconnect || returned == "" {
			_ = cleanupUnpublishedConversation(l.conv, false)
			return fmt.Errorf(
				"resumed provider conversation handle %q does not match requested handle %q",
				returned, l.cfg.ProviderConversationID,
			)
		}
		// The provider never persisted the requested conversation and started a
		// fresh one in its place. ControllerReady rebinds the session to it.
		freshReplacement = true
		if !l.cfg.FreshIfProviderConversationMissing {
			// A switch branch that never had a turn: its branch row names the old
			// handle too, so it moves with the session once the start commits.
			l.cfg.ControllerReady = s.rebindBranchOnReady(ctx, l.cfg.ControllerReady, l.conversation.ID, l.activeBranch.ID, l.cfg.ProviderConversationID)
		}
	}
	if (l.cfg.HistoryMode == ports.ChatHistoryRequired) && l.liveReconnect {
		// A TUI handoff needs a fresh, verified native-history admission. A host
		// left alive by an unpublished target is not an established Chat owner,
		// and adopting it must not take the ordinary live-reconnect fast path.
		// Detach here; the transition coordinator owns conclusive host shutdown.
		cleanupErr := cleanupUnpublishedConversation(l.conv, false)
		return errors.Join(fmt.Errorf("%w: native-history handoff cannot adopt an existing live provider; a fresh observation is required",
			ports.ErrChatRecoveryInconclusive), cleanupErr)
	}
	if l.cfg.ProviderConversationID != "" {
		// Resume owns live-host adoption before installation/auth discovery. A
		// desktop update can move a binary while its old process is still running.
		// Admit using what this connection actually negotiated, not a fresh probe.
		l.caps = maps.Clone(l.conv.Capabilities())
		if l.liveReconnect {
			// Live continuation does not require native load/resume support; that
			// capability is relevant only after the provider process itself dies.
			if l.caps == nil {
				l.caps = make(ports.ChatCapabilities)
			}
			l.caps[ports.ChatCapabilityResume] = true
		}
		if err := capabilityAdmissionError(l.cfg.Harness, l.caps, l.cfg.Permissions, l.cfg.Policy); err != nil {
			_ = cleanupUnpublishedConversation(l.conv, false)
			return err
		}
	}
	if freshReplacement {
		// No native history exists to import; continue as a fresh start.
		l.cfg.ProviderConversationID = ""
	}
	if l.liveReconnect {
		if s.reader == nil {
			_ = cleanupUnpublishedConversation(l.conv, false)
			return fmt.Errorf("%w: durable conversation snapshot is unavailable", ports.ErrChatRecoveryInconclusive)
		}
		l.liveRows, err = s.reader.LoadConversationSnapshot(ctx, l.conversation.ID)
		if err != nil {
			_ = cleanupUnpublishedConversation(l.conv, false)
			return fmt.Errorf("%w: load live conversation before reconnect: %w", ports.ErrChatRecoveryInconclusive, err)
		}
	}
	if l.owner.Kind == domain.ConversationOwnerReview && !l.liveReconnect {
		if reviewStore, ok := s.store.(reviewerConversationStore); ok {
			review, found, readErr := reviewStore.GetReviewByID(ctx, l.owner.ID)
			if readErr != nil {
				_ = cleanupUnpublishedConversation(l.conv, l.cfg.ProviderConversationID == "")
				return fmt.Errorf("read reviewer Chat owner before start: %w", readErr)
			}
			if found {
				if _, cleanupErr := s.store.CleanupOwnedReviewControllerWork(ctx, l.owner.ID, l.conversation.ID, review.ControllerGeneration, s.now()); cleanupErr != nil {
					_ = cleanupUnpublishedConversation(l.conv, l.cfg.ProviderConversationID == "")
					return fmt.Errorf("settle previous reviewer Chat work: %w", cleanupErr)
				}
			}
		}
	}

	return nil
}

// claimControllerFence claims the durable controller fence before events are consumed.
func (s *Service) claimControllerFence(ctx context.Context, l *launch) error {
	// Reconcile legacy demands before replacing the owning generation, while
	// durable successful turns can still prove recovery for that epoch.
	if _, err := s.store.ReconcileConversationAuthentication(ctx, l.conversation.ID, "", "", s.now()); err != nil {
		_ = cleanupUnpublishedConversation(l.conv, false)
		return fmt.Errorf("reconcile conversation authentication: %w", err)
	}

	// Claim the durable fence before the controller starts consuming events. A
	// pending provider boundary claims it in ControllerReady's atomic ownership
	// commit instead, so a failed provider connect or callback cannot split the
	// session owner from the conversation head.
	l.generation = strings.TrimSpace(l.cfg.ControllerGeneration)
	if l.generation == "" {
		l.generation = s.newID()
	}
	reclaimedTurnID := ""
	if l.liveReconnect {
		if turn := liveTurnToRestore(l.liveRows.Turns); turn != nil {
			reclaimedTurnID = turn.ID
		}
	}
	if l.providerBoundaryID == "" {
		if l.owner.Kind == domain.ConversationOwnerReview {
			claimed, claimErr := s.store.ClaimReviewChatController(ctx, l.owner.ID, l.conv.ProviderConversationID(), l.generation, s.now())
			if claimErr != nil || !claimed {
				_ = cleanupUnpublishedConversation(l.conv, l.cfg.ProviderConversationID == "")
				if claimErr != nil {
					return fmt.Errorf("claim reviewer chat controller: %w", claimErr)
				}
				return errors.New("reviewer chat controller ownership changed")
			}
		} else if err := s.claimChatController(ctx, l.cfg.SessionID, l.generation, l.liveReconnect, reclaimedTurnID); err != nil {
			_ = cleanupUnpublishedConversation(l.conv, l.cfg.ProviderConversationID == "")
			return fmt.Errorf("claim chat controller: %w", err)
		}
	}
	return nil
}

// settleLiveAndPlanBoundary settles unbound live turns and plans the pending provider boundary.
func (s *Service) settleLiveAndPlanBoundary(ctx context.Context, l *launch) error {
	if l.liveReconnect {
		// A provider may have accepted a prompt before its ID could be bound.
		// Its replayed events will be adopted separately; leave bound live work
		// and queued intake intact while closing the unbound visible spinner.
		for _, turn := range l.liveRows.Turns {
			if turn.State != domain.TurnStateRunning || turn.ProviderTurnID != "" || turn.RolledBackAt != nil {
				continue
			}
			if err := s.store.SettleUnboundRunningTurn(ctx, l.conversation.ID, l.cfg.SessionID, turn.ID, s.now()); err != nil {
				s.log.Error("chat start: settle unbound running turn", "session", l.cfg.SessionID, "turn", turn.ID, "error", err)
			}
		}
	}
	l.providerBoundary = (*domain.ConversationBranch)(nil)
	if l.providerBoundaryID != "" {
		l.providerBoundary = &domain.ConversationBranch{
			ID: l.providerBoundaryID, ConversationID: l.conversation.ID, SessionID: l.cfg.SessionID,
			ProviderConversationID: l.conv.ProviderConversationID(), ParentBranchID: l.activeBranch.ID,
			ForkAfterSequence: l.conversation.LatestSequence, ProviderScopeID: l.providerScopeID,
			ProviderIDsScoped: true,
			CreatedAt:         s.now(),
		}
		if l.cfg.ProviderSwitch != nil {
			l.providerBoundary.Strategy = domain.ConversationBranchStrategyApproximateContext
			l.providerBoundary.ReplayCutoffSequence = l.cfg.ProviderSwitch.SourceSequence
			l.providerBoundary.ReplayTruncated = true
			// Stored with the branch, so a later restart of its provider reads it back.
			l.providerBoundary.HandoffNote = l.cfg.ProviderSwitch.Bootstrap
		}
	}
	return nil
}
