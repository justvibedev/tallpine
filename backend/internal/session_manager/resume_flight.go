package sessionmanager

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// manualResumeBudget bounds a user's Resume. It runs detached from the request:
// the REST budget (1m) is shorter than a provider handshake plus the history
// import on a loaded machine, and a resume cut off by its request left nothing
// for the next press but a 503 or a 409.
var manualResumeBudget = 3 * time.Minute

// manualResumeWait bounds how long one Resume request waits for its resume,
// under the REST budget. Past it the request answers ErrResumeStillRunning and
// the resume carries on; the next press joins it.
var manualResumeWait = 45 * time.Second

// resumeFlight is one resume of one session that every concurrent Resume,
// manual or automatic, shares: a second press or a press during a startup
// auto-resume waits for the same attempt and gets its result instead of a 409.
// Fields after done are written before done closes; userJoined is guarded by
// agentOpMu.
type resumeFlight struct {
	done   chan struct{}
	result RestoreResult
	err    error
	auto   bool
	// userJoined records that a user Resume is waiting on (or owns) this
	// attempt: an automatic caller then leaves retries to the user.
	userJoined bool
	// joiners counts the callers that joined instead of starting an attempt.
	joiners int
}

// ResumeAgentWithMode replaces an exited agent inside its still-live session.
// Unlike RestoreWithMode, it preserves the existing worktree and terminal
// identity and never changes the durable terminated flag as an intermediate
// step. It joins a resume already running for the session; its own resume runs
// detached from ctx under manualResumeBudget.
func (m *Manager) ResumeAgentWithMode(ctx context.Context, id domain.SessionID) (RestoreResult, error) {
	if err := ctx.Err(); err != nil {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, err)
	}
	flight, owner := m.claimResumeFlight(id, false)
	if owner {
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), manualResumeBudget)
		stop := context.AfterFunc(m.backgroundContext, cancel)
		// Tracked for shutdown like spawn work, but not through the
		// runBackground seam: tests queue that seam and would never run it.
		m.backgroundWorkers.Add(1)
		go func() {
			defer m.backgroundWorkers.Done()
			defer cancel()
			defer stop()
			result, err := m.resumeAgentWithMode(runCtx, id, false)
			m.finishResumeFlight(id, flight, result, err)
		}()
	}
	wait := time.NewTimer(manualResumeWait)
	defer wait.Stop()
	select {
	case <-flight.done:
		return flight.result, flight.err
	case <-ctx.Done():
	case <-wait.C:
	}
	return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrResumeStillRunning)
}

// autoResume is resumeAgentWithMode for automatic callers (wake, startup
// recovery, version move).
func (m *Manager) autoResume(ctx context.Context, id domain.SessionID, requireNativeHistory bool) error {
	_, err := m.autoResumeJoined(ctx, id, requireNativeHistory)
	return err
}

// autoResumeJoined runs an automatic resume as the session's resume flight, or
// waits for the one already running. userOwned reports that a user Resume
// shared the attempt: the user saw its result and owns any retry.
func (m *Manager) autoResumeJoined(ctx context.Context, id domain.SessionID, requireNativeHistory bool) (userOwned bool, err error) {
	flight, owner := m.claimResumeFlight(id, true)
	if !owner {
		select {
		case <-flight.done:
			return !flight.auto, flight.err
		case <-ctx.Done():
			return !flight.auto, fmt.Errorf("resume agent %s: %w", id, ctx.Err())
		}
	}
	// Marks the session so beginAgentResumeJoining lets a user wait out this
	// resume's operation lock rather than fail.
	m.agentOpMu.Lock()
	if m.autoResumes == nil {
		m.autoResumes = make(map[domain.SessionID]int)
	}
	m.autoResumes[id]++
	m.agentOpMu.Unlock()
	defer func() {
		m.agentOpMu.Lock()
		m.autoResumes[id]--
		if m.autoResumes[id] <= 0 {
			delete(m.autoResumes, id)
		}
		m.agentOpMu.Unlock()
	}()
	result, err := m.resumeAgentWithMode(ctx, id, requireNativeHistory)
	m.finishResumeFlight(id, flight, result, err)
	m.agentOpMu.Lock()
	userOwned = flight.userJoined
	m.agentOpMu.Unlock()
	return userOwned, err
}

// claimResumeFlight returns the session's running flight, or registers a new
// one the caller (owner) must finish.
func (m *Manager) claimResumeFlight(id domain.SessionID, auto bool) (flight *resumeFlight, owner bool) {
	m.agentOpMu.Lock()
	defer m.agentOpMu.Unlock()
	if current := m.resumeFlights[id]; current != nil {
		if !auto {
			current.userJoined = true
		}
		current.joiners++
		m.logger.Info("resume agent: joined the resume already running", "sessionID", id,
			"automaticAttempt", current.auto, "manualCaller", !auto)
		return current, false
	}
	if m.resumeFlights == nil {
		m.resumeFlights = make(map[domain.SessionID]*resumeFlight)
	}
	flight = &resumeFlight{done: make(chan struct{}), auto: auto, userJoined: !auto}
	m.resumeFlights[id] = flight
	return flight, true
}

// finishResumeFlight publishes the result to every waiter. The flight leaves
// the registry first, so a press after a failure starts a fresh attempt.
func (m *Manager) finishResumeFlight(id domain.SessionID, flight *resumeFlight, result RestoreResult, err error) {
	m.agentOpMu.Lock()
	if m.resumeFlights[id] == flight {
		delete(m.resumeFlights, id)
	}
	m.agentOpMu.Unlock()
	flight.result, flight.err = result, err
	close(flight.done)
}
