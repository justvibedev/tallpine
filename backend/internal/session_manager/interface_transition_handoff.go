package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (m *Manager) prepareSourceHandoff(
	ctx context.Context,
	rec domain.SessionRecord,
	policy domain.SessionInterfaceTransitionPolicy,
	lastTerminalInputAt time.Time,
) error {
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		handoff, ok := m.chat.(chatHandoffLauncher)
		if !ok {
			return ErrInterfaceHandoffUnsupported
		}
		return handoff.PrepareChatHandoff(ctx, rec.ID, policy)
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		if rec.Activity.State == domain.ActivityExited {
			return nil
		}
		return ErrIncompleteHandle
	}
	if policy == domain.SessionInterfaceTransitionInterrupt {
		return m.drainAfterInterrupt(ctx, rec, handle, lastTerminalInputAt)
	}

	var detector ports.TerminalActivityDetector
	var surfaceInspector ports.TerminalSurfaceInspector
	if agent, ok := m.agents.Agent(rec.Harness); ok {
		detector, _ = agent.(ports.TerminalActivityDetector)
		surfaceInspector, _ = agent.(ports.TerminalSurfaceInspector)
	}
	styledOutput, _ := m.runtime.(ports.StyledTerminalOutputReader)
	// Surface proof is a joint capability: the adapter must understand its TUI
	// and the runtime must provide a rendered viewport with the styling that
	// distinguishes provider chrome from human-authored text. Runtimes without
	// that capability retain the causally-fresh idle / legacy fallback below.
	surfaceProofAvailable := surfaceInspector != nil && styledOutput != nil
	inspectors := drainInspectors{detector: detector, surface: surfaceInspector, styled: styledOutput}
	ticker := time.NewTicker(m.interfaceTransition.pollInterval)
	defer ticker.Stop()
	idleSince := time.Time{}
	idleSamples := 0
	unverifiedIdleSince := time.Time{}
	for {
		current, ok, err := m.store.GetSession(ctx, rec.ID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if current.Activity.State == domain.ActivityExited {
			return nil
		}
		if current.Activity.State.NeedsInput() {
			// This typed provider state outranks terminal-screen heuristics. The
			// input gate is already closed, so continuing to wait would make the
			// decision impossible to answer and deadlock the drain on runtimes
			// without styled current-screen capture.
			return errDrainDecisionPending
		}
		now := time.Now()
		durableIdleProven := tuiIdleAfterInput(current, lastTerminalInputAt)
		idleProven := durableIdleProven && !surfaceProofAvailable
		unverifiedIdle := current.Activity.State == domain.ActivityIdle && !idleProven
		probeCtx := ctx
		var cancelProbe context.CancelFunc
		if unverifiedIdle {
			if unverifiedIdleSince.IsZero() {
				unverifiedIdleSince = now
			}
			probeCtx, cancelProbe = context.WithDeadline(ctx, unverifiedIdleSince.Add(m.interfaceTransition.staleIdleLimit))
		}
		// A screen can still show the idle composer briefly after Enter was
		// accepted. Give the last input a full settle window before treating
		// adapter markers as evidence, then require repeated samples below.
		inputQuiet := lastTerminalInputAt.IsZero() ||
			!now.Before(lastTerminalInputAt.Add(m.interfaceTransition.idleSettle))
		probe := m.probeDrainSurface(probeCtx, handle, current, durableIdleProven, inputQuiet, inspectors, drainProbeState{
			surfaceProofAvailable: surfaceProofAvailable, idleProven: idleProven, unverifiedIdle: unverifiedIdle, now: now,
		})
		surfaceProofAvailable, idleProven, now = probe.surfaceProofAvailable, probe.idleProven, probe.now
		surfaceKnownBusy := probe.surfaceKnownBusy
		if probe.decisionPending {
			if cancelProbe != nil {
				cancelProbe()
			}
			return errDrainDecisionPending
		}
		if current.Activity.State != domain.ActivityIdle || surfaceKnownBusy {
			// Active work may wait without a deadline. A recognized current-screen
			// state resets prior ambiguity; decisions returned above instead because
			// the closed input gate would prevent the user from answering them.
			unverifiedIdleSince = time.Time{}
			if surfaceKnownBusy && cancelProbe != nil {
				cancelProbe()
				cancelProbe = nil
				probeCtx = ctx
			}
		}
		if idleProven {
			idleSamples++
			if idleSince.IsZero() {
				idleSince = now
			} else if now.Sub(idleSince) >= m.interfaceTransition.idleSettle &&
				(!surfaceProofAvailable && durableIdleProven || idleSamples >= interfaceTransitionSurfaceIdleSamples) {
				if cancelProbe != nil {
					cancelProbe()
				}
				return nil
			}
		} else {
			idleSince = time.Time{}
			idleSamples = 0
		}
		alive, probeErr := m.runtime.IsAlive(probeCtx, handle)
		if cancelProbe != nil {
			cancelProbe()
		}
		if probeErr == nil && !alive {
			return nil
		}
		now = time.Now()
		// Bound the whole contradictory idle regime, not just consecutive
		// unknown captures. Partial terminal redraws may alternate between safe
		// and ambiguous forever; neither outcome is enough to stop the source.
		if current.Activity.State == domain.ActivityIdle && !idleProven && !surfaceKnownBusy &&
			!unverifiedIdleSince.IsZero() && now.Sub(unverifiedIdleSince) >= m.interfaceTransition.staleIdleLimit {
			return errDrainQuiescenceUnverified
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// tuiIdleAfterInput proves that the idle fact was written after every terminal
// frame accepted before the drain gate closed. Merely observing an old idle row
// is unsafe: the PTY may still be processing input that arrived afterward.
func tuiIdleAfterInput(rec domain.SessionRecord, lastInputAt time.Time) bool {
	if rec.Activity.State != domain.ActivityIdle {
		return false
	}
	return lastInputAt.IsZero() || !rec.Activity.LastActivityAt.Before(lastInputAt)
}

func (m *Manager) abortSourceHandoff(transition domain.SessionInterfaceTransition) {
	if transition.SourceMode != domain.SessionModeChat {
		return
	}
	if handoff, ok := m.chat.(chatHandoffLauncher); ok {
		handoff.AbortChatHandoff(transition.SessionID)
	}
}

func (m *Manager) stopSourceController(ctx context.Context, rec domain.SessionRecord) error {
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if m.chat == nil {
			return nil
		}
		return m.chat.StopChat(ctx, rec.ID)
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		return nil
	}
	return m.runtime.Destroy(ctx, handle)
}

// stopSourceControllerConclusive makes one retry because both shipped runtime
// teardown and Chat stop are idempotent. A TUI liveness probe can turn a failed
// tmux command into a conclusive success when the process did in fact exit. Chat
// Service retains a controller whose event stream has not stopped, so its retry
// waits for the same process rather than losing the only handle to it.
func (m *Manager) stopSourceControllerConclusive(rec domain.SessionRecord) error {
	var failures []error
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), interfaceTransitionStopLimit)
		err := m.stopSourceController(ctx, rec)
		cancel()
		if err == nil {
			return nil
		}
		failures = append(failures, err)
		if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
			probeCtx, probeCancel := context.WithTimeout(context.Background(), 5*time.Second)
			alive, probeErr := m.runtime.IsAlive(probeCtx, runtimeHandle(rec.Metadata))
			probeCancel()
			if probeErr == nil && !alive {
				return nil
			}
			if probeErr != nil {
				failures = append(failures, fmt.Errorf("verify terminal controller stopped: %w", probeErr))
			}
		}
	}
	return fmt.Errorf("could not prove the source controller stopped after retry: %w", errors.Join(failures...))
}

// drainAfterInterrupt interrupts a live terminal controller and waits a short,
// bounded window for its native history to flush.
func (m *Manager) drainAfterInterrupt(ctx context.Context, rec domain.SessionRecord, handle ports.RuntimeHandle, lastTerminalInputAt time.Time) error {
	if rec.Activity.State == domain.ActivityExited {
		return nil
	}
	interrupter, ok := m.runtime.(runtimeInterrupter)
	if !ok {
		return fmt.Errorf("runtime cannot interrupt a live terminal controller")
	}
	// Explicit Stop now is authoritative even when the durable activity row
	// says idle: approval screens can be user-blocked while that row is idle.
	// Sending the interrupt is what makes “Cancel request and switch” true.
	if err := interrupter.Interrupt(ctx, handle); err != nil {
		return err
	}
	// Let the provider observe Ctrl-C and begin flushing its native history
	// before an already-idle activity row can end the loop immediately.
	initialSettle := time.NewTimer(m.interfaceTransition.pollInterval)
	select {
	case <-ctx.Done():
		initialSettle.Stop()
		return ctx.Err()
	case <-initialSettle.C:
	}
	// Give the provider a short, bounded window to flush its native transcript
	// after Ctrl-C. The subsequent Destroy is still authoritative, so a stale
	// activity hook cannot turn "stop now" into an unbounded drain.
	deadline := time.NewTimer(interfaceInterruptSettle)
	defer deadline.Stop()
	ticker := time.NewTicker(interfaceTransitionPoll)
	defer ticker.Stop()
	for {
		current, found, readErr := m.store.GetSession(ctx, rec.ID)
		if readErr != nil {
			return readErr
		}
		if !found {
			return ErrNotFound
		}
		if current.Activity.State == domain.ActivityExited || tuiIdleAfterInput(current, lastTerminalInputAt) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return nil
		case <-ticker.C:
		}
	}
}

// drainInspectors are the optional terminal capabilities of the source drain.
type drainInspectors struct {
	detector ports.TerminalActivityDetector
	surface  ports.TerminalSurfaceInspector
	styled   ports.StyledTerminalOutputReader
}

// drainProbeState is what one drain sample learned about the terminal.
type drainProbeState struct {
	surfaceProofAvailable, idleProven, unverifiedIdle, surfaceKnownBusy, decisionPending bool
	now                                                                                  time.Time
}

// probeDrainSurface inspects the rendered terminal (or, failing that, the
// activity detector) for proof that the source is idle, busy or waiting on a
// decision.
func (m *Manager) probeDrainSurface(probeCtx context.Context, handle ports.RuntimeHandle, current domain.SessionRecord, durableIdleProven, inputQuiet bool, in drainInspectors, st drainProbeState) drainProbeState {
	if st.surfaceProofAvailable && inputQuiet && in.styled != nil {
		output, outputErr := in.styled.GetStyledOutput(probeCtx, handle, interfaceTransitionOutputLines)
		st.now = time.Now()
		if errors.Is(outputErr, ports.ErrStyledTerminalOutputUnavailable) {
			// Detached ConPTY hosts survive daemon/app upgrades. A host from a
			// protocol version without current-screen support is a per-handle
			// capability miss, so retain the same durable/legacy fallback as a
			// runtime that never implemented styled output at all.
			st.surfaceProofAvailable = false
			st.idleProven = durableIdleProven
			st.unverifiedIdle = current.Activity.State == domain.ActivityIdle && !st.idleProven
		} else if outputErr == nil {
			observation := in.surface.InspectTerminalSurface(output)
			switch {
			case observation.Work == ports.TerminalSurfaceWorkWaitingInput,
				observation.Work == ports.TerminalSurfaceWorkBlocked:
				// Provider-owned approval and input requests are not composer
				// drafts. The terminal input gate is closed while draining, so
				// waiting here would also make the request impossible to answer.
				// Abort the drain and return control to the source interface.
				st.decisionPending = true
				return st
			case current.Activity.State == domain.ActivityIdle &&
				observation.Work == ports.TerminalSurfaceWorkIdle:
				st.idleProven = true
			case observation.Work == ports.TerminalSurfaceWorkActive:
				st.surfaceKnownBusy = true
			}
		}
	}
	if !st.surfaceProofAvailable && st.unverifiedIdle && in.detector != nil && inputQuiet {
		// Compatibility path for terminal activity adapters that have not yet
		// adopted the richer surface observation capability.
		if output, outputErr := m.runtime.GetOutput(probeCtx, handle, interfaceTransitionOutputLines); outputErr == nil {
			st.now = time.Now()
			state, authoritative := in.detector.DetectTerminalActivity(output)
			st.idleProven = authoritative && state == domain.ActivityIdle
		}
	}
	return st
}
