package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A concurrent session writer can advance updated_at between lifecycle's read
// and its guarded projection. Retry from the new durable record a bounded
// number of times so the signal is reduced against the facts that actually won
// instead of overwriting them with a stale full projection.
const maxActivitySignalProjectionRetries = 3

// nonInteractiveNativeEntrypoint reports whether a hook came from a headless
// Claude process. Claude forces CLAUDE_CODE_ENTRYPOINT to sdk-cli for -p runs,
// even when the parent interactive session exported "cli", while SDK hosts set
// sdk-ts/sdk-py. Other values, including unset, keep today's semantics.
func nonInteractiveNativeEntrypoint(entrypoint string) bool {
	switch strings.TrimSpace(entrypoint) {
	case "sdk-cli", "sdk-ts", "sdk-py":
		return true
	}
	return false
}

// ApplyActivitySignal records an authoritative agent activity signal and any
// native agent session id carried alongside it. Metadata-only hooks leave the
// existing activity and first-signal facts untouched.
func (m *Manager) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	err := m.applyActivitySignal(ctx, id, s)
	if err == nil && s.Valid && s.State == domain.ActivityIdle {
		m.autoCloseAfterSignal(ctx, id)
	}
	return err
}

func (m *Manager) applyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	s, accepted := normalizeActivitySignal(s)
	if !accepted {
		return nil
	}
	if err := m.waitPendingLaunch(ctx, id, s.LaunchID); err != nil {
		return err
	}
	// Only this session's reducers serialize with each other. A pending launch
	// is waited out above without it: MarkSpawned holds it while committing.
	unlockSession := m.sessionLocks.lock(id)
	projectionAttempts := 0
	terminalTurnRecorded := false
	originalSignal := s
retryProjection:
	s = originalSignal
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		unlockSession()
		return err
	}
	if !ok {
		unlockSession()
		return fmt.Errorf("%w: %s", ports.ErrSessionNotFound, id)
	}
	observedRevision := rec.Revision
	now := m.clock()
	fence := m.fenceActivitySignal(ctx, id, rec, s)
	if fence.err != nil || !fence.accepted {
		unlockSession()
		return fence.err
	}
	mode, currentChatController, nativeIdentityChanged := fence.mode, fence.currentChatController, fence.nativeIdentityChanged
	// Every ownership fence, the native thread's included, has accepted the
	// signal. A revision retry reduces the same signal again, so the turn is
	// recorded once.
	if !terminalTurnRecorded {
		terminalTurnRecorded = true
		m.recordActivityTurn(ctx, rec, s, mode, now)
	}
	checkpoint, s, acceptedPrompt := reduceActivityCheckpoint(rec, s, mode, nativeIdentityChanged, now)
	if exitedWorkloadResurrection(rec, s, currentChatController) {
		unlockSession()
		return nil
	}
	s, subagentFacts, err := reduceSubagentActivity(rec, s, now)
	if err != nil {
		unlockSession()
		return err
	}
	storedSubagentFacts := storeSubagentFacts(rec, &checkpoint, subagentFacts)
	// Event-tagged signals fold through the session's tool-flight state first:
	// they may be suppressed (state write skipped) by the blocked-precedence
	// rule, while their tracking side effects still land. Untagged signals
	// (old CLIs, adapters without tool identity) retain their activity semantics,
	// but only event-tagged main-turn facts may advance checkpoint text or time.
	metadataChanged := activityMetadataChanged(rec, s, checkpoint, subagentFacts != storedSubagentFacts)
	m.mu.Lock()
	toolFlightBeforeProjection := cloneToolFlight(m.flights[id])
	if s.Valid {
		s = m.applyToolPrecedenceLocked(id, rec.Activity.State, rec.Harness, s)
	}
	m.mu.Unlock()
	if !s.Valid && !metadataChanged {
		unlockSession()
		return nil
	}
	// Every fence has accepted the signal, so its tool step may land. A
	// projection that loses its revision race puts the steps back below, and
	// the retry records the step again against the fresh row.
	m.mu.Lock()
	stepsBeforeProjection := m.stepsSnapshotLocked(id)
	m.recordStepLocked(id, s, now)
	m.mu.Unlock()
	proj := activityProjection{
		id: id, rec: &rec, signal: originalSignal, event: s.Event, observedRevision: observedRevision,
		acceptedTUIPrompt: acceptedPrompt && mode == domain.SessionModeTUI, now: now,
		toolFlight: toolFlightBeforeProjection, steps: stepsBeforeProjection, attempts: &projectionAttempts,
	}
	if !s.Valid {
		rec.Metadata = checkpoint
		applyActivityMetadata(&rec.Metadata, s)
		rec.UpdatedAt = now
		_, retry, err := m.project(ctx, proj, rec)
		if err != nil {
			unlockSession()
			return err
		}
		if retry {
			goto retryProjection
		}
		unlockSession()
		return nil
	}
	if metadataChanged {
		// Fold metadata into rec before copying it into next below, so the
		// activity and resume handle land in one store update.
		rec.Metadata = checkpoint
		applyActivityMetadata(&rec.Metadata, s)
	}
	prevState := rec.Activity.State
	act := domain.Activity{State: s.State, LastActivityAt: timeOr(s.Timestamp, now)}
	sameState := sameActivity(rec.Activity, act)
	completedIdleTurn := completedIdleTurnProof(rec, s, act)
	if sameState && !rec.FirstSignalAt.IsZero() && !completedIdleTurn {
		if metadataChanged || s.Event == "user-prompt-submit" {
			rec.UpdatedAt = now
			_, retry, err := m.project(ctx, proj, rec)
			if err != nil {
				unlockSession()
				return err
			}
			if retry {
				goto retryProjection
			}
			unlockSession()
			return nil
		}
		unlockSession()
		return nil
	}
	next := rec
	next.Activity = act
	if next.FirstSignalAt.IsZero() {
		next.FirstSignalAt = timeOr(s.Timestamp, now)
	}
	m.dropFlightOnAgentExit(id, s)
	next.UpdatedAt = now
	applied, retry, err := m.project(ctx, proj, next)
	if err != nil {
		unlockSession()
		return err
	}
	if retry {
		goto retryProjection
	}
	if !applied {
		unlockSession()
		return nil
	}
	intent := needsInputIntent(rec, next)
	// Leaving the needs-input family is the user answering: the notification
	// that pinged them has nothing left to resolve.
	resolutions := needsInputResolutions(rec, next, now)
	unlockSession()
	m.afterActivityCommit(ctx, next, prevState)
	m.emitNotification(ctx, intent)
	m.resolveNotifications(ctx, resolutions...)
	return nil
}
