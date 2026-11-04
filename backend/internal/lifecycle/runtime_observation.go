package lifecycle

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ApplyRuntimeObservation only writes when runtime liveness is unambiguous. A
// failed probe or liveness disagreement is ignored. Runtime death keeps the
// existing recent-activity guard; supervised workload death is independently
// fenced by the launch generation and never terminates the runtime.
func (m *Manager) ApplyRuntimeObservation(ctx context.Context, id domain.SessionID, f ports.RuntimeFacts) error {
	err := m.applyRuntimeObservation(ctx, id, f)
	if err == nil && f.Workload == ports.ProbeDead {
		m.autoCloseAfterSignal(ctx, id)
	}
	return err
}

func (m *Manager) applyRuntimeObservation(ctx context.Context, id domain.SessionID, f ports.RuntimeFacts) error {
	matchesLaunch := func(cur domain.SessionRecord) bool {
		currentLaunch := cur.Metadata.RuntimeLaunchID
		return currentLaunch == "" || f.LaunchID == currentLaunch
	}
	var (
		finalizer           sessionUsageFinalizer
		terminationLaunch   string
		terminationRevision int64
		shouldTerminate     bool
	)
	if err := m.mutate(ctx, id, func(cur domain.SessionRecord, now time.Time) (domain.SessionRecord, bool) {
		if cur.IsTerminated || !matchesLaunch(cur) {
			return cur, false
		}
		currentLaunch := cur.Metadata.RuntimeLaunchID
		if currentLaunch != "" && f.Runtime == ports.ProbeAlive && f.Workload == ports.ProbeDead {
			if cur.Activity.State == domain.ActivityExited {
				return cur, false
			}
			next := cur
			next.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: timeOr(f.ObservedAt, now)}
			m.dropToolFlight(id)
			return next, true
		}
		// Tallpine destroyed this launch's terminal on purpose and kept the session
		// (an agent exit); Resume builds a new one. Its death is not a crash.
		if cur.Activity.State == domain.ActivityExited && currentLaunch != "" &&
			cur.Metadata.RuntimeReleasedLaunchID == currentLaunch {
			return cur, false
		}
		if !runtimeClearlyDead(f, cur.Activity, now, m.window) {
			return cur, false
		}
		if m.sessionMutationInProgress(id) {
			return cur, false
		}
		finalizer = m.currentUsageFinalizer()
		terminationLaunch = currentLaunch
		terminationRevision = cur.Revision
		shouldTerminate = true
		return cur, false
	}); err != nil || !shouldTerminate {
		return err
	}

	finalizeSessionUsage(ctx, id, terminationLaunch, terminationRevision, finalizer)

	terminated := false
	err := m.mutate(ctx, id, func(cur domain.SessionRecord, now time.Time) (domain.SessionRecord, bool) {
		if cur.IsTerminated || cur.Revision != terminationRevision ||
			cur.Metadata.RuntimeLaunchID != terminationLaunch || !matchesLaunch(cur) ||
			!runtimeClearlyDead(f, cur.Activity, now, m.window) || m.sessionMutationInProgress(id) {
			return cur, false
		}
		next := cur
		next.IsTerminated = true
		next.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: timeOr(f.ObservedAt, now)}
		// Reaper-driven death (crash/SIGKILL) never fires a session-end hook,
		// so this is the last chance to release the session's tool-flight
		// state; a leaked entry would otherwise persist for the daemon's life
		// (later observations return early on cur.IsTerminated). Runs under
		// the session lock — mutate holds it across this callback.
		m.dropSessionState(id)
		terminated = true
		return next, true
	})
	if err != nil {
		return err
	}
	if terminated {
		// Route reaper-observed death through the same container-reap hook as
		// every other terminal path (#2652): a crash/SIGKILL detected by the
		// runtime reaper must not leave the session's Docker containers behind
		// just because it never called MarkTerminated directly.
		m.reapSessionContainers(ctx, id)
		m.reapSessionProcesses(id)
		// Same for its temp directory: a crashed session must not leak it.
		m.discardSessionScratch(id)
	}
	return nil
}
