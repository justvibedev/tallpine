package chat

import (
	"context"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

// buildController settles previous work and constructs the unpublished controller.
func (s *Service) buildController(ctx context.Context, l *launch) error {
	// Whatever the previous controller left in flight is not this controller's, and
	// it is not evidence that any work finished.
	//
	// The graceful path settles this when the event stream ends, but a daemon that
	// was killed never got there — so on a crash the timeline was left claiming a
	// turn was still running and a queued message was still waiting to be sent,
	// behind a controller that no longer existed. Nothing would ever have corrected
	// it. Settling here covers every way a controller can come up, and is a no-op
	// for a session that has none of it. Queued turns never reached the dead
	// provider, so they stay queued for this controller to deliver.
	//
	// A session whose first controller this is has no previous controller and so
	// no orphaned work — only the intake an asynchronous spawn queued ahead of
	// it. Settling that would fail the user's opening prompt the moment the agent
	// it was waiting for finally arrived.
	if !l.liveReconnect && l.cfg.ProviderHandoff == nil &&
		l.owner.Kind != domain.ConversationOwnerReview &&
		!l.queuedBeforeFirstController && !l.preserveUndispatchedQueue {
		s.settleOrphanedWork(ctx, l.cfg.SessionID, l.conversation.ID)
	}
	// A provider that is not the live one a persistent host kept is a new process
	// and cannot hold the background jobs the daemon re-seeded at boot for the old
	// host; no controller closed to clear them, so drop them here.
	if !l.liveReconnect && l.owner.Kind != domain.ConversationOwnerReview && s.onAsyncTasksGone != nil {
		s.onAsyncTasksGone(l.cfg.SessionID)
	}
	// A fresh generation per launch, so events from the controller this one
	// replaced can be told apart from the current one's.
	l.controller = newController(
		l.cfg.SessionID, l.owner, l.conversation, l.generation, l.cfg.Harness, l.conv, s.store, s.activity, s.log, s.newID, s.now, s.onAccountChanged, s.recheckAccount, s.onCodexCapacityChanged, s.reader)
	l.controller.onAsyncTask = s.onAsyncTask
	l.controller.onAsyncTasksGone = s.onAsyncTasksGone
	l.controller.reportCoordinator = func() *reportsvc.Coordinator { return s.reports.Load() }
	l.controller.onUsageLimitPaused = s.failoverPausedSession
	l.controller.compactLive = launchedCompactTokens(*l.cfg, l.liveReconnect)
	l.controller.compactBaseline = projectCompactTokens(*l.cfg)
	if contextwindow.AutoCompactEnvKey(l.cfg.Harness) != "" {
		// tokens -1: a reattached agent whose window is reconciled below; 0: the agent's default.
		s.log.Info("chat compaction window at launch", "session", l.cfg.SessionID, "kind", l.cfg.Kind,
			"tokens", l.controller.compactLive, "reattached", l.liveReconnect)
	}
	l.controller.kind = l.cfg.Kind
	l.controller.transientRetry.policy = s.transientRetryPolicy()
	s.carryResearchToController(ctx, l.controller)
	controller := l.controller
	controller.prepareWorkerQueue = func(ctx context.Context, queued domain.QueuedTurn) (domain.QueuedTurn, error) {
		return s.prepareWorkerQueue(ctx, controller, queued)
	}
	// A reservation whose receipt was never recorded is uncertain on a cold start
	// as well as a live reconnect. Settling it as failed keeps the message visible
	// and retryable; leaving it reserved would strand it, unsent and unreported.
	if err := s.store.FailReservedQueuedBatch(ctx, l.conversation.ID, s.now()); err != nil {
		_ = cleanupUnpublishedConversation(l.conv, false)
		return fmt.Errorf("settle uncertain queued batch: %w", err)
	}
	if l.liveReconnect {
		providerTurnID := l.controller.restoreLiveTurnOwnership(l.liveRows.Turns)
		if activator, ok := l.conv.(ports.ChatLiveReconnectActivator); ok {
			if err := activator.ActivateLiveReconnect(ctx, providerTurnID); err != nil {
				_ = cleanupUnpublishedConversation(l.conv, false)
				return err
			}
		}
	}
	return nil
}

// importNativeHistory imports the provider-native history before the projector starts.
func (s *Service) importNativeHistory(ctx context.Context, l *launch) error {
	var err error
	if l.cfg.ProviderConversationID != "" && l.cfg.HistoryMode != ports.ChatHistoryDeferred && !l.liveReconnect {
		// The provider's native thread is the continuity authority across TUI and
		// Chat. Import it before the live projector starts so the first notification
		// cannot appear ahead of the older prompt, tool work, and answer it follows.
		//
		// Read Tallpine's existing projection too. ACP message/turn ids are opaque, and an
		// agent may assign a different persisted user id from the id Tallpine supplied at
		// prompt time. Reconciliation must therefore happen before projection; doing
		// it in a Claude binding would leave every other ACP harness with the same
		// restart duplication race.
		var existing ConversationRows
		if s.reader != nil {
			existing, err = s.reader.LoadConversationSnapshot(ctx, l.conversation.ID)
			if err != nil {
				_ = cleanupUnpublishedConversation(l.conv, false)
				return fmt.Errorf("load conversation before native history import: %w", err)
			}
		}
		retained := existing
		if l.cfg.ProviderHandoff != nil {
			// Independent context: retain only the current Terminal hook proof.
			existing = ConversationRows{}
		} else {
			existing, err = s.nativeReplayRows(ctx, l.activeBranch, existing)
			if err != nil {
				_ = cleanupUnpublishedConversation(l.conv, false)
				return err
			}
			// Older builds carried legacy hook text across edits. Trusted native
			// checkpoints always retain their identity and content requirements.
			// The edit's timestamp proves those facts predate this continuation;
			// newer or undated hooks still gate replay, as does its Tallpine high-water mark.
			if l.activeBranch.ReplacedTurnID != "" {
				if l.replayCheckpoint.userMismatch == ports.ChatHistoryMismatchUntrustedUserText &&
					!l.replayCheckpoint.latestUserPromptAt.IsZero() && l.replayCheckpoint.latestUserPromptAt.Before(l.activeBranch.CreatedAt) {
					l.replayCheckpoint.latestUserPrompt = ""
				}
				if l.replayCheckpoint.assistantMismatch == ports.ChatHistoryMismatchUntrustedAssistantText &&
					!l.replayCheckpoint.latestAssistantUpdateAt.IsZero() && l.replayCheckpoint.latestAssistantUpdateAt.Before(l.activeBranch.CreatedAt) {
					l.replayCheckpoint.latestAssistantUpdate = ""
				}
			}
		}
		if l.cfg.HistoryMode == ports.ChatHistoryRequired {
			l.replayCheckpoint.captureTallpineHighWater(
				l.cfg.SessionID, existing.Turns, existing.Messages, existing.Activities,
			)
		}
		events, historyErr := l.controller.readNativeHistory(
			ctx, existing.Turns, existing.Messages, existing.Activities,
			(l.cfg.HistoryMode == ports.ChatHistoryRequired), l.replayCheckpoint,
		)
		if historyErr != nil {
			if cleanupErr := cleanupUnpublishedConversation(l.conv, false); cleanupErr != nil && (l.cfg.HistoryMode == ports.ChatHistoryRequired) {
				return fmt.Errorf("%w: failed history target shutdown: %w",
					ports.ErrChatRecoveryInconclusive, errors.Join(historyErr, cleanupErr))
			}
			return historyErr
		}
		events, err = s.withoutInheritedHistory(ctx, l.conv, l.activeBranch, retained, events)
		if err != nil {
			_ = cleanupUnpublishedConversation(l.conv, false)
			return err
		}
		events = reconcileNativeHistory(events, existing.Turns, existing.Messages, existing.Activities)
		if l.providerBoundary != nil {
			if l.cfg.ProviderHandoff != nil {
				// Visible, boundary-keyed provenance: earlier rows are retained but
				// are not represented as context inherited by this native provider.
				events = append([]ports.ChatEvent{{
					Kind:            ports.ChatEventActivityCompleted,
					ProviderEventID: l.providerBoundary.ID + ":context-boundary",
					ProviderItemID:  l.providerBoundary.ID + ":context-boundary",
					ActivityKind:    domain.ActivityKindSystem, ActivityStatus: domain.ActivityStatusCompleted,
					Summary: "Native conversation changed. Earlier messages are retained; continuity with this agent's context is not verified.",
					Detail:  []byte(`{"event":"context.boundary","reason":"native_terminal_handoff"}`),
				}}, events...)
			}
			// The pending branch does not exist yet. Carry its stable replay into
			// ControllerReady so SQLite can insert/activate the boundary, stage the
			// generation, project every event onto that branch, and publish the
			// live session as one transaction. Until then the terminated target is
			// not exposed to input and no event can be attributed to the old root.
			l.commitProviderHistory = func(commitCtx context.Context) error {
				if handoff := l.cfg.ProviderHandoff; handoff != nil {
					// Settle the retired owner's work in the publication transaction.
					// A failed replay must leave it untouched; a successful handoff
					// must not dispatch its queue into an independent native context.
					if err := s.store.SettleOrphanedTurns(commitCtx, handoff.PreviousSessionID, s.now()); err != nil {
						return err
					}
					if err := s.store.FailPendingApprovals(commitCtx, l.conversation.ID, s.now()); err != nil {
						return err
					}
					if err := s.store.FailPendingInputs(commitCtx, l.conversation.ID, s.now()); err != nil {
						return err
					}
				}
				return l.controller.projectNativeHistory(commitCtx, events)
			}
		} else if err := l.controller.projectNativeHistory(ctx, events); err != nil {
			_ = cleanupUnpublishedConversation(l.conv, false)
			return err
		}
	}
	return nil
}

// publishController commits ownership and publishes the controller. Takes s.mu.
func (s *Service) publishController(ctx context.Context, l *launch) error {
	var err error
	l.commit, err = notifyControllerReady(
		*l.cfg, l.controller, l.providerBoundary, l.commitProviderHistory,
	)
	if err != nil {
		_ = cleanupUnpublishedConversation(l.conv, l.cfg.ProviderConversationID == "")
		return err
	}
	if l.providerBoundary != nil {
		if l.cfg.ControllerReady == nil {
			if l.commitProviderHistory != nil {
				_ = cleanupUnpublishedConversation(l.conv, false)
				return errors.New("commit native history: atomic provider-boundary lifecycle is unavailable")
			}
			var err error
			if l.owner.Kind == domain.ConversationOwnerReview {
				err = s.store.CreateAndActivateReviewConversationBranch(ctx, l.owner.ID, *l.providerBoundary, l.generation, s.now())
			} else {
				err = s.store.CreateAndActivateConversationBranch(ctx, l.cfg.SessionID, *l.providerBoundary, l.generation, s.now())
			}
			if err != nil {
				_ = cleanupUnpublishedConversation(l.conv, false)
				return fmt.Errorf("commit fresh provider boundary: %w", err)
			}
			l.commit.Conversation = l.controller.conversation
			l.commit.Conversation.ActiveBranchID = l.providerBoundary.ID
			l.commit.Conversation.UpdatedAt = s.now()
		} else if l.commit.Conversation.ActiveBranchID != l.providerBoundary.ID {
			_ = cleanupUnpublishedConversation(l.conv, false)
			return fmt.Errorf("commit chat controller: provider boundary %s was not activated", l.providerBoundary.ID)
		}
	}
	if l.commit.Conversation.ID != "" {
		// The controller has not been published and its projector has not started,
		// so this is the one safe point where the activation-mutated cache fields
		// move together. Preserve immutable identity from the controller itself:
		// after ControllerReady commits ownership, synchronization must be an
		// infallible assignment rather than another operation that can strand it.
		l.controller.conversation.ActiveBranchID = l.commit.Conversation.ActiveBranchID
		l.controller.conversation.Settings = l.commit.Conversation.Settings
		l.controller.conversation.UpdatedAt = l.commit.Conversation.UpdatedAt
		l.controller.settings = l.commit.Conversation.Settings
	}
	if l.commit.ControllerOwner != (domain.SessionControllerOwner{}) {
		l.cfg.ExpectedControllerOwner = l.commit.ControllerOwner
	} else {
		l.cfg.ExpectedControllerOwner.Harness = l.cfg.Harness
		l.cfg.ExpectedControllerOwner.Mode = domain.SessionModeChat
		l.cfg.ExpectedControllerOwner.IsTerminated = false
		l.cfg.ExpectedControllerOwner.RuntimeLaunchID = ""
		l.cfg.ExpectedControllerOwner.ProviderConversationID = l.controller.ProviderConversationID()
		l.cfg.ExpectedControllerOwner.ControllerGeneration = l.controller.Generation()
	}
	s.mu.Lock()
	s.ownerControllers[l.owner] = l.controller
	if l.owner.Kind == domain.ConversationOwnerSession {
		s.controllers[l.cfg.SessionID] = l.controller
	}
	// A committed reservation is consumed. Internal controller restarts must
	// resume the now-current branch, not retry its old ownership snapshot.
	l.cfg.ProviderHandoff = nil
	l.cfg.ProviderSwitch = nil
	l.cfg.ProviderScopeID = ""
	l.cfg.AwakeReconnectTarget = nil
	l.cfg.HistoryMode = ports.ChatHistoryImport
	s.startConfigs[l.owner] = cloneStartConfig(*l.cfg)
	delete(s.awakeInterrupted, l.owner)
	l.controller.start()
	s.mu.Unlock()
	if l.liveReconnect {
		// The reattached provider launched with a window Tallpine cannot see.
		s.reconcileCompactionAsync(l.controller)
	}
	return nil
}

// watchControllerExit drops the registry entry when the provider stream ends.
func (s *Service) watchControllerExit(ctx context.Context, l *launch) error {
	// Drop the registry entry when the provider stream ends, so a later command
	// reports ErrNoController instead of writing into a dead controller.
	controller, owner, cfg := l.controller, l.owner, l.cfg
	go func() {
		controller.Wait()
		controller.waitForBranchHandoff()
		s.mu.Lock()
		if current, ok := s.ownerControllers[owner]; ok && current == controller {
			controller.mu.Lock()
			interrupted := controller.transportInterrupted && !controller.explicitClose
			controller.mu.Unlock()
			if interrupted && owner.Kind == domain.ConversationOwnerSession && cfg.ExpectedControllerOwner.ControllerGeneration != "" {
				s.awakeInterrupted[owner] = domain.AwakeAgentTarget{SessionID: cfg.SessionID, Owner: cfg.ExpectedControllerOwner}
			}
			delete(s.ownerControllers, owner)
			if owner.Kind == domain.ConversationOwnerSession {
				delete(s.controllers, cfg.SessionID)
			}
		}
		s.mu.Unlock()
	}()
	return nil
}
