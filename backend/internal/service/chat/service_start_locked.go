package chat

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// launch carries the state one startLocked call threads between its steps. It
// replaces what used to be function locals; every field keeps the name and
// meaning of the local it came from. A launch is single-goroutine and never
// outlives the call. Closures that outlive it (prepareEnv, prepareWorkerQueue,
// the exit watcher) capture only the controller, owner and the cfg pointer, never
// the launch, so the live-reconnect snapshot and imported history are released.
type launch struct {
	cfg   *StartConfig
	owner domain.ConversationOwner

	driver ports.ChatDriver
	resume func(context.Context, ports.ChatResumeConfig) (ports.ChatConversation, error)

	replayCheckpoint nativeHistoryCheckpoint
	nativeEvidence   string
	caps             ports.ChatCapabilities

	sessionRecord       domain.SessionRecord
	sessionFound        bool
	firstController     bool
	scope               domain.ConversationScope
	freshProjectContext bool

	conversation domain.ConversationRecord
	activeBranch domain.ConversationBranch

	providerScopeID                   string
	providerHandleOwnedByActiveBranch bool
	queuedBeforeFirstController       bool
	preserveUndispatchedQueue         bool
	providerBoundaryID                string

	prepareEnv           func(context.Context) (map[string]string, error)
	providerSystemPrompt string

	conv           ports.ChatConversation
	freshIfMissing bool
	liveReconnect  bool
	liveRows       ConversationRows

	generation            string
	providerBoundary      *domain.ConversationBranch
	controller            *Controller
	commitProviderHistory func(context.Context) error
	commit                ControllerCommit
}

// startLocked is the launch implementation shared by ordinary starts and the
// provider-switch saga. Callers must hold the typed controller gate. It takes
// s.mu itself only inside reuseExistingController, admitStart (awake proof)
// and publishController/watchControllerExit; no other step may be entered with
// s.mu held. Every step that opens the provider rolls it back with
// cleanupUnpublishedConversation on its own failure paths (there are no
// deferred cleanups), in the same order as before the split.
func (s *Service) startLocked(ctx context.Context, cfg StartConfig) (*Controller, error) {
	l := &launch{cfg: &cfg, owner: conversationOwner(cfg)}
	if l.owner.Kind == domain.ConversationOwnerReview {
		l.cfg.ReadOnly = true
	}
	if err := s.admitStart(ctx, l); err != nil {
		return nil, err
	}
	if err := s.loadReplayCheckpoint(ctx, l); err != nil {
		return nil, err
	}
	if existing, err := s.reuseExistingController(ctx, l); existing != nil || err != nil {
		return existing, err
	}
	steps := []func(context.Context, *launch) error{
		s.resolveDriver,
		s.verifyNativeEvidence,
		s.checkInitialCapabilities,
		s.readSessionForStart,
		s.openConversation,
		s.resolveProviderScope,
		s.applyConversationSettings,
		s.prepareLaunchEnv,
		s.openProvider,
		s.admitOpenedProvider,
		s.claimControllerFence,
		s.settleLiveAndPlanBoundary,
		s.buildController,
		s.importNativeHistory,
		s.publishController,
		s.watchControllerExit,
	}
	for _, step := range steps {
		if err := step(ctx, l); err != nil {
			return nil, err
		}
	}
	return l.controller, nil
}

// reuseExistingController returns a live controller for the owner, or waits out
// and removes a stopped one. (nil, nil) means continue launching.
func (s *Service) reuseExistingController(ctx context.Context, l *launch) (*Controller, error) {
	owner, cfg := l.owner, *l.cfg
	s.mu.RLock()
	existing := s.ownerControllers[owner]
	s.mu.RUnlock()
	if existing != nil && cfg.ProviderSwitch == nil {
		if existing.State() != ports.ChatControllerStopped {
			return existing, nil
		}
		// A stopped event can reach the UI before the projector finishes its final
		// durable cleanup and the registry goroutine releases the entry. Never hand
		// that dead controller back as a successful resume. Wait for its stream to
		// finish, then remove only that generation before opening the replacement.
		select {
		case <-existing.stopped:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		s.mu.Lock()
		if current := s.ownerControllers[owner]; current == existing {
			delete(s.ownerControllers, owner)
			if owner.Kind == domain.ConversationOwnerSession {
				delete(s.controllers, cfg.SessionID)
			}
		}
		s.mu.Unlock()
	}
	return nil, nil
}

// cleanupUnpublishedConversation rolls back a provider opened before its Tallpine
// controller was published. A fresh host must be destroyed even when it opened a
// stored provider conversation; only a proven attachment to the same live host
// is detached so transient Tallpine persistence cannot interrupt work in flight.
func cleanupUnpublishedConversation(conv ports.ChatConversation, fresh bool) error {
	terminator, canTerminate := conv.(ports.ChatProviderTerminator)
	liveReconnect := false
	if reconnected, ok := conv.(ports.ChatLiveReconnector); ok {
		liveReconnect = reconnected.ReconnectedLive()
	}
	// A newly launched persistent process must not be orphaned merely because it
	// was opening an existing provider conversation. Only attachment to the same
	// already-live process is preserve-on-failure ownership.
	if canTerminate && (fresh || !liveReconnect) {
		return terminator.Terminate()
	}
	return conv.Close()
}
