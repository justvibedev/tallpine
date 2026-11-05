package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	chatdriverregistry "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/registry"
	"tallpine.eu/tallpine/backend/internal/attachmentstore"
	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/service/turnnotify"
	"tallpine.eu/tallpine/backend/internal/storagejanitor"
)

// wireChat: settings, storage janitor, turn notifier, background tasks and the chat service.
func (b *boot) wireChat() error {
	b.wireChatSettings()
	b.wireChatNotifiers()
	// The session's own account (a saved one, or this computer's login) is read
	// again off the chat path; the CLI probe can take seconds.
	recheckSessionAccount := func(sessionID domain.SessionID, harness domain.AgentHarness) {
		if accounts := b.agentAccountSvc; accounts != nil {
			go accounts.RecheckSessionAccount(b.ctx, sessionID, harness)
		}
	}
	b.chatSvc = chatsvc.New(chatsvc.Options{
		Store:                      b.store,
		Sessions:                   b.store,
		Projects:                   b.store,
		Renders:                    attachmentstore.New(b.cfg.DataDir),
		DataDir:                    b.cfg.DataDir,
		ReconcileOutputType:        b.lcStack.LCM.ReconcileSessionOutputType,
		HibernationEnabled:         b.settingsSvc.ChatHibernationEnabled,
		AutoCompactDefault:         b.settingsSvc.AutoCompactDefault,
		DynamicOrchestratorContext: b.settingsSvc.DynamicOrchestratorContextEnabled,
		HibernationPolicy:          chatHibernationPolicy(b.settingsSvc, b.store),
		BackgroundProcesses:        chatBackgroundProcesses(b.cfg.DataDir, nil, b.log),
		VersionRefresh: func() (time.Duration, bool) {
			return b.cfg.VersionRefreshIdle, b.cfg.VersionRefresh && b.settingsSvc.VersionRefreshEnabled(b.ctx)
		},
		AppRunID: b.cfg.AppRunID,
		StopProviderHost: func(ctx context.Context, id domain.SessionID) error {
			return persistenthost.Shutdown(ctx, b.cfg.DataDir, string(id))
		},
		// Adapts the store's own snapshot type, so the chat service never has to
		// import the storage layer.
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
			rows, err := b.store.LoadConversationSnapshot(ctx, conversationID)
			if err != nil {
				return chatsvc.ConversationRows{}, err
			}
			return chatsvc.ConversationRows{
				Conversation:                     rows.Conversation,
				ActiveBranch:                     rows.ActiveBranch,
				EditFloorSequence:                rows.EditFloorSequence,
				NativeForkAvailableAfterSequence: rows.NativeForkAvailableAfterSequence,
				Turns:                            rows.Turns,
				Messages:                         rows.Messages,
				Activities:                       rows.Activities,
				BranchPoints:                     rows.BranchPoints,
				BranchedFromEarlierMessage:       rows.BranchedFromEarlierMessage,
			}, nil
		}),
		PageReader: chatsvc.SnapshotPageReaderFunc(func(ctx context.Context, conversationID string, beforeSequence, limit int64) (chatsvc.ConversationRows, error) {
			rows, err := b.store.LoadConversationSnapshotPage(ctx, conversationID, beforeSequence, limit)
			if err != nil {
				return chatsvc.ConversationRows{}, err
			}
			return chatsvc.ConversationRows{
				Conversation:                     rows.Conversation,
				ActiveBranch:                     rows.ActiveBranch,
				EditFloorSequence:                rows.EditFloorSequence,
				NativeForkAvailableAfterSequence: rows.NativeForkAvailableAfterSequence,
				Turns:                            rows.Turns,
				Messages:                         rows.Messages,
				Activities:                       rows.Activities,
				BranchPoints:                     rows.BranchPoints,
				BranchedFromEarlierMessage:       rows.BranchedFromEarlierMessage,
				OldestSequence:                   rows.OldestSequence,
				HasMoreBefore:                    rows.HasMoreBefore,
			}, nil
		}),
		Drivers: b.chatDrivers,
		// A chat turn is a pure lifecycle reduction, same as a hook signal from a
		// terminal session; the turn observer forwards every signal to the LCM.
		Activity:         b.turnNotifier,
		OnAsyncTask:      func(id domain.SessionID, task ports.ChatAsyncTask) { b.bgTasks.Apply(id, task) },
		OnAsyncTasksGone: b.bgTasks.Clear,
		Log:              b.log,
		NewID:            uuid.NewString,
		RecheckAccount:   recheckSessionAccount,
		OnAccountChanged: func(sessionID domain.SessionID, generation string, harness domain.AgentHarness) {
			// The session's own account (a saved one, or this computer's login) is
			// read again off the chat path; the CLI probe can take seconds.
			recheckSessionAccount(sessionID, harness)
			if harness != domain.HarnessCodex || b.agentSvc == nil || b.agentSvc.Codex().CodexAccountSwitchInProgress() {
				return
			}
			rec, ok, readErr := b.store.GetSession(b.ctx, sessionID)
			if readErr == nil && ok && rec.Harness == domain.HarnessCodex && rec.Metadata.ControllerGeneration == generation {
				b.agentSvc.Codex().InvalidateCodexAccountAuthentication()
			}
		},
		OnCodexCapacityChanged: func(sessionID domain.SessionID, generation string, observation ports.CodexCapacityObservation) {
			if b.agentSvc == nil || b.agentSvc.Codex().CodexAccountSwitchInProgress() {
				return
			}
			rec, ok, readErr := b.store.GetSession(b.ctx, sessionID)
			if readErr != nil || !ok || rec.Harness != domain.HarnessCodex || rec.Metadata.ControllerGeneration != generation {
				return
			}
			b.agentSvc.Codex().ObserveActiveCodexAccountCapacity(observation)
		},
		// Sync ChatUI's model choice, including clearing its override, before a
		// later TUI rebuild reads the session metadata.
		OnModelChanged: func(sessionID domain.SessionID, model string) {
			if b.sessMgr == nil {
				return
			}
			if err := b.sessMgr.PersistChatModel(b.ctx, sessionID, model); err != nil {
				b.log.Warn("persist ChatUI model on session failed; a TUI rebuild may resume with a different model",
					"sessionID", sessionID, "model", model, "error", err)
			}
		},
	})
	b.chatSvc.SetRenderCheck(renderViaDesktop(b.browserBroker, "__render-check"))
	b.chatSvc.SetRenderMeasure(renderViaDesktop(b.browserBroker, "__render-measure"))

	if err := b.chatSvc.InitializeSideChats(b.ctx); err != nil {
		b.log.Warn("side chat initialization failed", "error", err)
	}
	return nil
}

// wireChatSettings builds the chat driver registry, settings service and storage janitor.
func (b *boot) wireChatSettings() {
	// Wire the controller-facing session service over the same store + LCM, the
	// selected runtime, routed git/scratch workspaces, the per-session agent
	// resolver (TALLPINE_AGENT validated here for compatibility), and the agent
	// messenger, then mount it on the API.

	b.chatDrivers = chatdriverregistry.Build(b.log, func() {
		if b.agentSvc == nil {
			return
		}
		b.agentSvc.InvalidateAgentAuthentication(string(domain.HarnessClaudeCode))
		b.agentSvc.RecheckAgent(string(domain.HarnessClaudeCode))
	})

	// Daemon-owned preferences. The store's type is field-compatible with the
	// service's, adapted here so neither package imports the other. Offering
	// gates ride along: they are boot-time config, not stored preferences.
	b.settingsSvc = settingssvc.New(
		settingsStore{store: b.store},
		b.chatDrivers,
		settingssvc.OfferingFromConfig(b.cfg),
		func() time.Time { return time.Now().UTC() },
	)
	b.settingsSvc.SetManaged(b.managedLocks) // before the janitor below reads it
	// Turning dynamic orchestrator context off ends every running research mode
	// (B413). The chat service is read when it runs: it is built after this.
	b.settingsSvc.SetDynamicContextHook(func(ctx context.Context, enabled bool) {
		if !enabled && b.chatSvc != nil {
			b.chatSvc.ClearAllResearch(ctx)
		}
	})

	// Storage compaction (Settings, General): the store coalesces unchanged
	// activity writes, and the janitor compacts and expires the provider-event
	// archive in small batches off the projection path.
	b.store.SetCoalesceWrites(b.settingsSvc.StoragePolicy(b.ctx).Compaction)
	archiveJanitorDone := storagejanitor.New(b.store, func(ctx context.Context) (bool, time.Duration) {
		policy := b.settingsSvc.StoragePolicy(ctx)
		return policy.Compaction, policy.Retention
	}, storagejanitor.Config{Logger: b.log}).Start(b.ctx)
	// Deferred after store.Close, so it runs first: no pass outlives the store.
	b.cleanup(func() {
		b.stop()
		<-archiveJanitorDone
	})
}

// wireChatNotifiers builds the turn notifier and background-task registry.
func (b *boot) wireChatNotifiers() {
	// Chat service. The driver registry is the capability gate: a harness with no
	// registered driver cannot start in chat mode, so an unsupported request fails
	// loudly instead of silently becoming a TUI session.

	// Terminal hook Stop/SessionEnd facts and Chat turn lifecycle signals both
	// drive the synthesized turn_ended/exited reports, so the observer wraps the
	// LCM for both paths. The report service is bound once it exists below.
	// The same observer nudges a worker that stays idle and silent after its
	// turn, once any background job it started has finished.
	b.workerJobs = &turnnotify.ProcessJobs{CacheTTL: 5 * time.Second, Roots: b.workerJobRoots}
	b.turnNotifier = turnnotify.New(turnnotify.Deps{
		Next: b.lcStack.LCM, Sessions: b.store, Fallbacks: b.store, Log: b.log,
		NudgeDelay: b.cfg.WorkerIdleNudge, Nudger: b.lcStack.LCM,
		Jobs: b.workerJobs,
	})
	b.bgTasks = bgtasks.New(
		bgtasks.SendFunc(func(c context.Context, id domain.SessionID, msg string) error {
			if b.sessMgr == nil {
				return errors.New("session manager not ready")
			}
			return b.sessMgr.Send(c, id, msg, nil)
		}),
		func(c context.Context, id domain.SessionID) bool {
			rec, ok, err := b.store.GetSession(c, id)
			return err == nil && ok && !rec.IsTerminated && (rec.Activity.State == domain.ActivityIdle || rec.Activity.State == domain.ActivityWaitingInput)
		}, 0)
}

// workerJobRoots names where a session's process tree starts, for background-job counting.
func (b *boot) workerJobRoots(ctx context.Context, rec domain.SessionRecord) []int {
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if pid, ok := persistenthost.HostPID(b.cfg.DataDir, string(rec.ID)); ok {
			return []int{pid}
		}
		return nil
	}
	if rec.Metadata.RuntimeHandleID == "" {
		return nil
	}
	roots, err := b.runtimeAdapter.ProcessRootPIDs(ctx, ports.RuntimeHandle{ID: rec.Metadata.RuntimeHandleID})
	if err != nil {
		return nil
	}
	return roots
}
