package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

func (m *Manager) emitNotification(ctx context.Context, intent *ports.NotificationIntent) {
	if intent == nil || m.notifications == nil {
		return
	}
	if err := m.notifications.Notify(ctx, *intent); err != nil {
		slog.Default().Warn("lifecycle: notification failed", "session", intent.SessionID, "type", intent.Type, "err", err)
	}
}

// resolveNotifications closes notifications the just-written facts made stale.
// Best-effort like emitNotification: a failed resolve must never fail the
// lifecycle write that produced it.
func (m *Manager) resolveNotifications(ctx context.Context, resolutions ...ports.NotificationResolution) {
	if m.notifications == nil {
		return
	}
	for _, res := range resolutions {
		if err := m.notifications.Resolve(ctx, res); err != nil {
			slog.Default().Warn(
				"lifecycle: notification resolve failed",
				"session", res.SessionID, "pr", res.PRURL, "type", res.Type, "err", err,
			)
		}
	}
}

// MarkChatReconnected adopts the same live provider after daemon replacement.
// Generation has already been claimed by Chat Service. Reconnection is not
// activity: preserve the activity, signal receipt, and user-visible update time.
func (m *Manager) MarkChatReconnected(ctx context.Context, id domain.SessionID, metadata domain.SessionMetadata) error {
	defer m.sessionLocks.lock(id)()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok || rec.IsTerminated || rec.Mode != domain.SessionModeChat ||
		metadata.ProviderConversationID == "" || metadata.ProviderConversationID != rec.Metadata.ProviderConversationID ||
		metadata.ControllerGeneration == "" || metadata.ControllerGeneration != rec.Metadata.ControllerGeneration {
		return fmt.Errorf("lifecycle: live Chat reconnect for %q no longer owns the session", id)
	}
	// There are no new lifecycle facts to persist. In particular, avoid a full
	// record write that could overwrite activity arriving during reconnection.
	return nil
}

// MarkSpawned marks a newly spawned or restored session live and stores runtime/workspace handles.
func (m *Manager) MarkSpawned(ctx context.Context, id domain.SessionID, metadata domain.SessionMetadata) error {
	return m.markSpawned(ctx, id, metadata, nil, nil, nil)
}

// MarkChatSpawned atomically marks a Chat controller live and publishes the
// fresh provider boundary reserved before the provider process connected.
func (m *Manager) MarkChatSpawned(
	ctx context.Context,
	id domain.SessionID,
	metadata domain.SessionMetadata,
	boundary domain.ConversationBranch,
) error {
	if boundary.ID == "" || boundary.ConversationID == "" || boundary.SessionID != id ||
		boundary.ProviderConversationID == "" || boundary.ProviderScopeID != boundary.ID ||
		metadata.ProviderConversationID != boundary.ProviderConversationID ||
		strings.TrimSpace(metadata.ControllerGeneration) == "" {
		return fmt.Errorf("lifecycle: Chat provider boundary for %q has incomplete or mismatched ownership", id)
	}
	return m.markSpawned(ctx, id, metadata, &boundary, nil, nil)
}

// MarkChatSpawnedPrepared publishes native history together with its reserved
// provider boundary and lifecycle owner. The preparation callback is executed
// only by the storage transaction; no controller is registered and the session
// remains externally unavailable until the complete unit commits.
func (m *Manager) MarkChatSpawnedPrepared(
	ctx context.Context,
	id domain.SessionID,
	metadata domain.SessionMetadata,
	boundary domain.ConversationBranch,
	handoff *domain.ChatProviderHandoff,
	prepare func(context.Context) error,
) error {
	if prepare == nil {
		return errors.New("lifecycle: Chat provider-history preparation is missing")
	}
	if boundary.ID == "" || boundary.ConversationID == "" || boundary.SessionID != id ||
		boundary.ProviderConversationID == "" || boundary.ProviderScopeID != boundary.ID ||
		metadata.ProviderConversationID != boundary.ProviderConversationID ||
		strings.TrimSpace(metadata.ControllerGeneration) == "" {
		return fmt.Errorf("lifecycle: Chat provider boundary for %q has incomplete or mismatched ownership", id)
	}
	return m.markSpawned(ctx, id, metadata, &boundary, handoff, prepare)
}

func (m *Manager) markSpawned(
	ctx context.Context,
	id domain.SessionID,
	metadata domain.SessionMetadata,
	boundary *domain.ConversationBranch,
	handoff *domain.ChatProviderHandoff,
	prepare func(context.Context) error,
) error {
	launchID := strings.TrimSpace(metadata.RuntimeLaunchID)
	reactivator, err := func() (sessionUsageReactivator, error) {
		defer m.sessionLocks.lock(id)()
		defer m.finishLaunch(id, launchID)
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("lifecycle: MarkSpawned for unknown session %q", id)
		}
		now := m.clock()
		if rec.IsTerminated {
			rec.CleanupGeneration++
		}
		rec.IsTerminated = false
		rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
		// Each spawn/restore must re-prove its hook pipeline: clear the receipt so
		// a relaunch with broken hooks degrades to no_signal instead of inheriting
		// a stale "signals worked once" fact.
		rec.FirstSignalAt = time.Time{}
		rec.Metadata = mergeMetadata(rec.Metadata, metadata)
		if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat &&
			strings.TrimSpace(metadata.ControllerGeneration) != "" {
			// A committed Chat controller is the sole session owner. Clear any
			// terminal identity retained by an older handoff/recovery build so it
			// cannot confuse runtime observation or a later daemon restart.
			rec.Metadata.RuntimeHandleID = ""
			rec.Metadata.RuntimeLaunchID = ""
		}
		rec.UpdatedAt = now
		if boundary == nil {
			if err := m.store.UpdateSession(ctx, rec); err != nil {
				return nil, err
			}
		} else if prepare == nil {
			writer, ok := m.store.(chatSpawnStore)
			if !ok {
				return nil, errors.New("lifecycle: atomic Chat spawn persistence is unavailable")
			}
			if err := writer.CommitChatSpawn(ctx, rec, *boundary); err != nil {
				return nil, err
			}
		} else {
			writer, ok := m.store.(preparedChatSpawnStore)
			if !ok {
				return nil, errors.New("lifecycle: atomic Chat provider-history persistence is unavailable")
			}
			if err := writer.CommitChatSpawnPrepared(ctx, rec, *boundary, handoff, prepare); err != nil {
				return nil, err
			}
		}
		return m.currentUsageReactivator(), nil
	}()
	if err != nil {
		return err
	}
	reactivateSessionUsage(ctx, id, launchID, reactivator)
	return nil
}

// CommitControllerEpoch atomically changes which controller owns a live
// session. Session Manager coordinates the external-process saga, but only
// Lifecycle Manager is allowed to write the durable controller/activity facts.
// A false result means the expected source controller no longer owns the row.
// startFresh is accepted only with an empty native id; Session Manager sets it
// after an adapter proved the reserved id has no persisted conversation.
func (m *Manager) CommitControllerEpoch(
	ctx context.Context,
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	startFresh bool,
) (bool, error) {
	return m.changeControllerEpoch(
		ctx, id, source, target, nativeConversationID, startFresh, false,
	)
}

// RestoreControllerEpoch rolls an incomplete handoff back to its source owner.
// It deliberately preserves replay checkpoint provenance: the target never
// became live, so a later strict retry must still prove the same checkpoint.
func (m *Manager) RestoreControllerEpoch(
	ctx context.Context,
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	startFresh bool,
) (bool, error) {
	return m.changeControllerEpoch(
		ctx, id, source, target, nativeConversationID, startFresh, true,
	)
}

func (m *Manager) changeControllerEpoch(
	ctx context.Context,
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	startFresh, restore bool,
) (bool, error) {
	if !source.Valid() || !target.Valid() || source == target {
		return false, fmt.Errorf("lifecycle: invalid controller epoch %q -> %q", source, target)
	}
	nativeConversationID = strings.TrimSpace(nativeConversationID)
	if nativeConversationID == "" && !startFresh {
		return false, fmt.Errorf("lifecycle: controller epoch for %q has no native conversation id", id)
	}
	if nativeConversationID != "" && startFresh {
		return false, fmt.Errorf("lifecycle: fresh controller epoch for %q also supplied a native conversation id", id)
	}
	writer, ok := m.store.(controllerEpochStore)
	if !ok {
		return false, fmt.Errorf("lifecycle: controller epoch persistence is unavailable")
	}

	unlockSession := m.sessionLocks.lock(id)
	previous, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		unlockSession()
		return false, err
	}
	if !found {
		unlockSession()
		return false, fmt.Errorf("%w: %s", ports.ErrSessionNotFound, id)
	}
	if previous.IsTerminated || domain.NormalizeSessionMode(previous.Mode) != source {
		unlockSession()
		return false, nil
	}
	now := m.clock()
	var changed bool
	if restore {
		changed, err = writer.RestoreSessionControllerEpoch(
			ctx, id, source, target, nativeConversationID, now,
		)
	} else {
		changed, err = writer.CommitSessionControllerEpoch(
			ctx, id, source, target, nativeConversationID, now,
		)
	}
	if err != nil || !changed {
		unlockSession()
		return changed, err
	}

	// Mirror the atomic store write for lifecycle side effects. MarkSpawned will
	// clear FirstSignalAt and attach the target's process generation once the new
	// controller is actually live.
	next := previous
	next.Mode = target
	next.Metadata.RuntimeHandleID = ""
	next.Metadata.RuntimeLaunchID = ""
	next.Metadata.AgentSessionID = nativeConversationID
	next.Metadata.AgentSessionIDLaunchID = ""
	next.Metadata.ProviderConversationID = nativeConversationID
	next.Metadata.ControllerGeneration = ""
	if !restore && target == domain.SessionModeTUI {
		// The checkpoint admitted the source Terminal transcript into Chat. Once
		// Chat hands ownership back, newer Chat turns are represented by Tallpine's
		// durable high-water facts; retaining the old Terminal text would compare
		// it against the latest provider turn on the next round trip and fail a
		// valid replay closed. A new TUI main-turn hook establishes fresh text.
		next.Metadata.LatestUserPrompt = ""
		next.Metadata.LatestAssistantUpdate = ""
		next.Metadata.ConversationCheckpointState = domain.ConversationCheckpointEmpty
		next.Metadata.ConversationCheckpointGeneration = ""
		next.Metadata.ConversationCheckpointNativeID = ""
		next.Metadata.ConversationCheckpointTurnID = ""
		next.Metadata.ConversationCheckpointUnsettled = false
	}
	next.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
	next.UpdatedAt = now
	// The old runtime's open step never gets its post-hook; left in place it
	// would show as the new controller's current tool.
	m.dropSessionState(id)
	resolutions := needsInputResolutions(previous, next, now)
	unlockSession()

	m.resolveNotifications(ctx, resolutions...)
	return true, nil
}

// MarkTerminated marks a session terminated. Runtime/workspace teardown is the
// caller's responsibility (see session_manager.Manager.Kill); this also reaps the
// session's Docker containers via the optional ContainerReaper (#2652) as its one
// built-in external side effect.
func (m *Manager) MarkTerminated(ctx context.Context, id domain.SessionID) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil || !ok {
			return err
		}
		if rec.IsTerminated {
			m.reapSessionContainers(ctx, id)
			m.reapSessionProcesses(id)
			m.discardSessionScratch(id)
			return nil
		}

		launchID := rec.Metadata.RuntimeLaunchID
		sessionRevision := rec.Revision
		m.mu.Lock()
		finalizer := m.usageFinalizer
		m.mu.Unlock()
		finalizeSessionUsage(ctx, id, launchID, sessionRevision, finalizer)

		const (
			terminationChanged = iota
			terminationApplied
			terminationAlreadyApplied
			terminationLaunchChanged
		)
		outcome := terminationChanged
		err = m.mutate(ctx, id, func(cur domain.SessionRecord, now time.Time) (domain.SessionRecord, bool) {
			switch {
			case cur.IsTerminated:
				outcome = terminationAlreadyApplied
				return cur, false
			case cur.Metadata.RuntimeLaunchID != launchID:
				outcome = terminationLaunchChanged
				return cur, false
			case cur.Revision != sessionRevision:
				return cur, false
			default:
				cur.IsTerminated = true
				cur.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: now}
				m.dropSessionState(id)
				outcome = terminationApplied
				return cur, true
			}
		})
		if err != nil {
			return err
		}
		switch outcome {
		case terminationApplied, terminationAlreadyApplied:
			m.reapSessionContainers(ctx, id)
			m.reapSessionProcesses(id)
			m.discardSessionScratch(id)
			return nil
		case terminationLaunchChanged:
			return fmt.Errorf("lifecycle: runtime launch changed while terminating session %q", id)
		default:
			// A same-launch session write changed revision after usage was
			// finalized. Retry from a fresh snapshot so termination and usage
			// finalization commit against the same durable revision.
			continue
		}
	}
}

// discardSessionScratch removes the ended session's temp directory (see
// sessionscratch). Like the container reap it rides every terminal path. The
// directory is renamed aside here and removed in the background, so a large
// tree never holds up teardown; a removal the daemon does not finish is retried
// from the trash on the next start. A restored session gets a fresh directory
// at launch.
func (m *Manager) discardSessionScratch(id domain.SessionID) {
	if m.dataDir == "" {
		return
	}
	moved, err := sessionscratch.Discard(m.dataDir, id, time.Now())
	if err != nil {
		slog.Default().Warn("lifecycle: session temp dir not discarded", "session", id, "err", err)
		return
	}
	if moved == "" {
		return
	}
	go func() {
		if err := sessionscratch.RemoveTree(moved); err != nil {
			slog.Default().Warn("lifecycle: session temp dir removal incomplete; retried on next start", "session", id, "err", err)
		}
	}()
}

// reapSessionContainers is the container leg of #2652 (the container-owning
// counterpart to session_manager.Manager's cleanupAgentWorkspace): every
// MarkTerminated call - Kill, daemon-shutdown teardown, Cleanup,
// RetireForReplacement, and tracker-driven termination - funnels through
// here, so this single hook covers every terminal-state path rather than
// only explicit tallpine session kill. Best-effort: logged on failure, never
// returned, matching the rest of Tallpine's terminal-state teardown. Standalone
// sessions have no project-level opt-out, so they use the default reap-enabled
// policy. For project sessions, a project-load error skips reaping rather than
// guessing - the package's stated bias is to spare on ambiguity, not to reap on
// it.
func (m *Manager) reapSessionContainers(ctx context.Context, id domain.SessionID) {
	if m.containers == nil {
		return
	}
	if m.projects != nil {
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil || !ok {
			slog.Default().Warn("lifecycle: container reap: session lookup failed, skipping", "session", id, "err", err)
			return
		}
		if !rec.IsStandalone() {
			project, ok, err := m.projects.GetProject(ctx, string(rec.ProjectID))
			if err != nil || !ok {
				slog.Default().Warn("lifecycle: container reap: project lookup failed or missing, skipping rather than guessing", "session", id, "project", rec.ProjectID, "err", err)
				return
			}
			if project.Config.ContainerReap.Disabled {
				return
			}
		}
	}
	removed, err := m.containers.ReapSessionContainers(ctx, id)
	if err != nil {
		slog.Default().Warn("lifecycle: container reap failed", "session", id, "err", err)
		return
	}
	if removed > 0 {
		slog.Default().Info("lifecycle: reaped session containers", "session", id, "removed", removed)
	}
}

func finalizeSessionUsage(
	ctx context.Context,
	id domain.SessionID,
	expectedRuntimeLaunchID string,
	expectedSessionRevision int64,
	finalizer sessionUsageFinalizer,
) {
	if finalizer == nil {
		return
	}
	if err := finalizer.FinalizeSession(ctx, id, expectedRuntimeLaunchID, expectedSessionRevision); err != nil {
		slog.Default().Warn("lifecycle: finalize session usage before termination", "session", id, "err", err)
	}
}

func reactivateSessionUsage(
	ctx context.Context,
	id domain.SessionID,
	expectedRuntimeLaunchID string,
	reactivator sessionUsageReactivator,
) {
	if reactivator == nil {
		return
	}
	if err := reactivator.ReactivateSession(ctx, id, expectedRuntimeLaunchID); err != nil {
		slog.Default().Warn("lifecycle: reactivate session usage after launch", "session", id, "err", err)
	}
}
