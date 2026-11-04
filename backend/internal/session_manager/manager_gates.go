package sessionmanager

import (
	"context"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// acquireWorkspaceGate acquires the per-project workspace gate exclusively,
// returning a release function that must be deferred or called when the
// operation finishes. Restore, cleanup and rollback teardown hold it this way.
func (m *Manager) acquireWorkspaceGate(projectID domain.ProjectID) func() {
	mu := m.workspaceGate(projectID)
	mu.Lock()
	return mu.Unlock
}

// acquireSpawnWorkspaceGate acquires the per-project workspace gate shared.
// Starts that only add a session's own worktree (spawn, speculative task
// preparation, task-branch naming) exclude teardown but not each other: each
// works on a distinct path and branch, and the few writes they share in the
// repository (.git/config, info/exclude, the default-branch fetch) are
// serialized by the workspace adapter. Holding it exclusively made an
// orchestrator's burst of spawns into one project queue end to end behind each
// other's fetch, checkout, provisioning and agent launch.
func (m *Manager) acquireSpawnWorkspaceGate(projectID domain.ProjectID) func() {
	mu := m.workspaceGate(projectID)
	mu.RLock()
	return mu.RUnlock
}

func (m *Manager) workspaceGate(projectID domain.ProjectID) *sync.RWMutex {
	m.workspaceGateMu.Lock()
	defer m.workspaceGateMu.Unlock()
	if m.workspaceGates == nil {
		m.workspaceGates = make(map[domain.ProjectID]*sync.RWMutex)
	}
	mu := m.workspaceGates[projectID]
	if mu == nil {
		mu = &sync.RWMutex{}
		m.workspaceGates[projectID] = mu
	}
	return mu
}

// Spawn rollback must release its project's gate before untimed cleanup.
// OnceFunc makes the enclosing spawn's deferred release safe after that handoff.
type spawnWorkspaceGateKey struct{}

// SetHarnessUseGate late-binds the installer interlock after daemon wiring.
func (m *Manager) SetHarnessUseGate(gate HarnessUseGate) {
	m.harnessUseGate = gate
}

// TryBeginHarnessUse exposes the late-bound installer interlock to launchers
// built alongside the manager, such as reviewers. Without an installer it
// always succeeds.
func (m *Manager) TryBeginHarnessUse(harness domain.AgentHarness) (func(), bool) {
	release, err := m.beginHarnessUse(harness)
	return release, err == nil
}

func (m *Manager) beginHarnessUse(harness domain.AgentHarness) (func(), error) {
	if m.harnessUseGate == nil {
		return func() {}, nil
	}
	release, ok := m.harnessUseGate.TryBeginHarnessUse(harness)
	if !ok {
		return nil, ErrHarnessInstallActive
	}
	return release, nil
}

// SetModelCatalog late-binds the catalog service after daemon construction.
func (m *Manager) SetModelCatalog(catalog interface {
	Models(context.Context, string, string, bool) (ports.AgentModelCatalog, error)
}) {
	m.modelCatalog = catalog
}

// latestUserPromptRecorder narrows the post-delivery write to the pane prompt's
// coherent fallback checkpoint. A full SessionRecord update here could race a
// provider switch and resurrect stale harness/runtime ownership read before the
// pane write.
type latestUserPromptRecorder interface {
	RecordSessionLatestUserPrompt(context.Context, domain.SessionID, string, time.Time) (bool, error)
}

// SetShellTerminalCloser wires every worktree-releasing path to gate the
// session's scoped shell terminals shut first. Safe to leave unset: a nil
// closer makes beginShellTerminalTeardown a no-op (release=nil, err=nil),
// which is what every test in this package that does not care about shell
// terminals relies on.
func (m *Manager) SetShellTerminalCloser(closer ShellTerminalCloser) {
	m.shellTerminalsMu.Lock()
	defer m.shellTerminalsMu.Unlock()
	m.shellTerminals = closer
}

// SetTerminalInputGate late-binds the daemon's terminal mux after Session
// Manager is constructed. Nil preserves the no-op behavior used by narrow tests.
func (m *Manager) SetTerminalInputGate(gate TerminalInputGate) {
	m.terminalInputGateMu.Lock()
	defer m.terminalInputGateMu.Unlock()
	m.terminalInputGate = gate
}

// SetStartFailureObserver completes daemon wiring before request handling
// begins. observe runs after a background chat start is recorded as failed.
func (m *Manager) SetStartFailureObserver(observe func(domain.SessionID, string)) {
	m.startFailed = observe
}

// SetAgentReadiness completes daemon wiring before request handling begins.
func (m *Manager) SetAgentReadiness(provider ports.AgentReadinessProvider) {
	m.agentReadiness = provider
}

// SetAgentAccounts completes daemon wiring before request handling begins.
// Nil launches every agent under the computer's own CLI login.
func (m *Manager) SetAgentAccounts(accounts ports.AgentAccountLaunchEnv) {
	m.agentAccounts = accounts
}

// SetPersistentHostReconcileDone fences cold Chat resumes until startup has
// finished reaping hosts for sessions that were already hibernated on disk.
// Daemon wiring installs the channel before opening the HTTP listener.
func (m *Manager) SetPersistentHostReconcileDone(done <-chan struct{}) {
	m.persistentHostReconcileDone = done
}

func (m *Manager) beginTerminalInputDrain(rec domain.SessionRecord) (lastInputAt time.Time, release func()) {
	gate, terminalID := m.terminalInputGateFor(rec)
	if gate == nil {
		return time.Time{}, nil
	}
	return gate.BeginInputDrain(terminalID)
}

// terminalInputGateFor returns the input gate and terminal id for a TUI
// session's terminal; gate is nil when the session has none.
func (m *Manager) terminalInputGateFor(rec domain.SessionRecord) (gate TerminalInputGate, terminalID string) {
	handle := runtimeHandle(rec.Metadata)
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI || handle.ID == "" {
		return nil, ""
	}
	m.terminalInputGateMu.Lock()
	gate = m.terminalInputGate
	m.terminalInputGateMu.Unlock()
	return gate, handle.ID
}

// lastTerminalInputAt reports the newest keystroke Tallpine accepted for a TUI
// session's terminal, read-only. It is zero when nothing was typed through Tallpine
// or the gate cannot be read without closing input.
func (m *Manager) lastTerminalInputAt(rec domain.SessionRecord) time.Time {
	gate, terminalID := m.terminalInputGateFor(rec)
	observer, ok := gate.(TerminalInputObserver)
	if !ok {
		return time.Time{}
	}
	return observer.LastInputAt(terminalID)
}

// fenceTerminalInput holds a TUI session's terminal write fence so a human
// keystroke cannot interleave with the pane write the caller is about to make;
// keystrokes block (not dropped) until release. lastInputAt is the newest
// keystroke accepted before the fence closed. release is nil when there is
// nothing to fence (non-TUI, no handle, no gate, or a gate without the fencer
// capability), in which case the caller proceeds unsynchronized exactly as before.
func (m *Manager) fenceTerminalInput(rec domain.SessionRecord) (lastInputAt time.Time, release func()) {
	gate, terminalID := m.terminalInputGateFor(rec)
	fencer, ok := gate.(TerminalInputFencer)
	if !ok {
		return time.Time{}, nil
	}
	return fencer.HoldInput(terminalID)
}

// beginShellTerminalTeardown starts the shell-terminal gate for id ahead of
// releasing its worktree. release==nil, err==nil means no closer is wired
// (nothing to gate; proceed exactly as before this mechanism existed).
// err!=nil means some scoped shell terminal could not be confirmed closed —
// the caller MUST NOT touch the worktree, and release is nil (the gate
// already released itself). On success release is non-nil and tied to this
// specific acquisition; the caller MUST call it exactly once, typically via
// defer, once its own worktree work finishes.
func (m *Manager) beginShellTerminalTeardown(ctx context.Context, id domain.SessionID) (release func(), err error) {
	m.shellTerminalsMu.Lock()
	closer := m.shellTerminals
	m.shellTerminalsMu.Unlock()
	if closer == nil {
		return nil, nil
	}
	return closer.BeginSessionTeardown(ctx, id)
}

// SetReviewerTerminator wires worker lifecycle paths to the worker's reviewer
// pane. Safe to leave unset: a nil terminator is a no-op.
func (m *Manager) SetReviewerTerminator(terminator ReviewerTerminator) {
	m.reviewersMu.Lock()
	defer m.reviewersMu.Unlock()
	m.reviewers = terminator
}

func (m *Manager) terminateReviewer(ctx context.Context, id domain.SessionID, body string) error {
	m.reviewersMu.Lock()
	terminator := m.reviewers
	m.reviewersMu.Unlock()
	if terminator == nil {
		return nil
	}
	return terminator.TerminateReviewer(ctx, id, body)
}

func (m *Manager) teardownReviewerTerminal(ctx context.Context, id domain.SessionID) error {
	m.reviewersMu.Lock()
	terminator := m.reviewers
	m.reviewersMu.Unlock()
	if terminator == nil {
		return nil
	}
	return terminator.TeardownReviewerTerminal(ctx, id)
}

func (m *Manager) restoreReviewer(ctx context.Context, id domain.SessionID) error {
	m.reviewersMu.Lock()
	reviewer := m.reviewers
	m.reviewersMu.Unlock()
	if reviewer == nil {
		return nil
	}
	return reviewer.RestoreReviewer(ctx, id)
}

// SetPlannerEnabled turns on the planner section of orchestrator prompts
// (TALLPINE_PLANNER, B205).
func (m *Manager) SetPlannerEnabled(on bool) { m.plannerEnabled = on }
