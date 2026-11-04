// Package previewserver owns deterministic, session-scoped development server
// processes used by the desktop preview. It deliberately does not scan global
// localhost ports: a server is started from an explicit project configuration,
// so its worker, command, working directory, and URL are known.
package previewserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	// ConfigPath is the workspace-relative managed preview configuration file.
	ConfigPath = ".tallpine/launch.json"
	// LegacyConfigPath is the pre-rename location, still read when ConfigPath
	// does not exist. Nothing writes it.
	LegacyConfigPath        = ".ao/launch.json"
	defaultReadyTimeout     = 30 * time.Second
	maxReadyTimeout         = 55 * time.Second
	probeInterval           = 150 * time.Millisecond
	probeTimeout            = time.Second
	maxRedirects            = 5
	statusLogLines          = 40
	maxBufferedLogLines     = 200
	maxBufferedPartialBytes = 16 * 1024
	failedStatusRetention   = 5 * time.Minute
	processRegistryFile     = "preview-processes.json"
)

// State is the lifecycle state of a managed preview process.
type State string

const (
	// StateStopped indicates that no preview process is running.
	StateStopped State = "stopped"
	// StateStarting indicates that the preview process is waiting to become ready.
	StateStarting State = "starting"
	// StateReady indicates that the configured preview URL is responding.
	StateReady State = "ready"
	// StateStopping indicates that Tallpine is terminating the preview process.
	StateStopping State = "stopping"
	// StateFailed indicates that the preview process could not start, become ready, or stop.
	StateFailed State = "failed"
)

// TargetKind distinguishes browser applications from non-browser API servers.
type TargetKind string

const (
	// TargetApp is a preview that should open in the Browser panel.
	TargetApp TargetKind = "app"
	// TargetAPI is a server that should run without replacing the Browser panel.
	TargetAPI TargetKind = "api"
)

// Error is a stable service error that the HTTP controller can map without
// parsing platform-specific process errors.
type Error struct {
	Code    string
	Message string
}

func (e Error) Error() string { return e.Message }

// Status is the public state for one session's managed preview server.
type Status struct {
	SessionID     domain.SessionID `json:"sessionId"`
	State         State            `json:"state"`
	Configuration string           `json:"configuration,omitempty"`
	TargetKind    TargetKind       `json:"targetKind,omitempty"`
	URL           string           `json:"url,omitempty"`
	Port          int              `json:"port,omitempty"`
	StartedAt     time.Time        `json:"startedAt,omitempty"`
	Error         string           `json:"error,omitempty"`
	Logs          []string         `json:"logs"`
}

type serverRun struct {
	status     Status
	projectEnv map[string]string
	cmd        *exec.Cmd
	// startTime pins cmd's PID to the process Tallpine launched (kernel start time,
	// captured right after Start). Every group/tree kill re-verifies against
	// it: once the child is reaped the bare PID may already belong to an
	// unrelated process group (issue #3475). Written once before the run is
	// published; immutable afterwards.
	startTime string
	done      chan struct{}
	logs      *lineBuffer
	stopping  bool
}

type sessionOperation struct {
	mu   sync.Mutex
	refs int
}

// OnExitFunc is invoked from the manager's wait goroutine when a managed
// preview process exits on its own (i.e. the caller did not request the
// stop). It runs synchronously off the manager's lock, so it is safe to call
// back into the session service. The status passed in carries the post-failure
// State/Error so callers can decide whether to clear the session's preview URL.
type OnExitFunc func(ctx context.Context, sessionID domain.SessionID, status Status)

// Manager supervises at most one managed preview server per Tallpine session.
type Manager struct {
	log    *slog.Logger
	client *http.Client

	mu   sync.Mutex
	runs map[domain.SessionID]*serverRun

	operationsMu sync.Mutex
	operations   map[domain.SessionID]*sessionOperation
	registryPath string

	onExitMu sync.Mutex
	onExit   OnExitFunc
}

// New creates a managed preview-server supervisor.
func New(log *slog.Logger, dataDir ...string) *Manager {
	if log == nil {
		log = slog.Default()
	}
	transport := &http.Transport{}
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	}
	transport.Proxy = nil
	manager := &Manager{
		log:        log,
		runs:       make(map[domain.SessionID]*serverRun),
		operations: make(map[domain.SessionID]*sessionOperation),
	}
	if len(dataDir) > 0 && strings.TrimSpace(dataDir[0]) != "" {
		manager.registryPath = filepath.Join(dataDir[0], processRegistryFile)
		manager.reapPersistedProcesses()
	}
	manager.client = &http.Client{
		Transport: transport,
		Timeout:   probeTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many preview readiness redirects")
			}
			if !isLoopbackHost(req.URL.Hostname()) {
				return errors.New("preview readiness redirected away from loopback")
			}
			return nil
		},
	}
	return manager
}

// SetOnExit registers a callback fired from the wait goroutine when a managed
// preview process exits without being stopped. Pass nil to clear. The callback
// is invoked with a fresh context that is not tied to any request; it runs off
// the manager's mutex so it may safely call back into the session service.
func (m *Manager) SetOnExit(fn OnExitFunc) {
	m.onExitMu.Lock()
	m.onExit = fn
	m.onExitMu.Unlock()
}

func (m *Manager) onExitCallback() OnExitFunc {
	m.onExitMu.Lock()
	defer m.onExitMu.Unlock()
	return m.onExit
}

// Stop terminates the exact process tree Tallpine launched for the session.
func (m *Manager) Stop(ctx context.Context, sessionID domain.SessionID) (Status, error) {
	releaseOperation := m.acquireOperation(sessionID)
	defer releaseOperation()
	return m.stop(ctx, sessionID)
}

func (m *Manager) stop(ctx context.Context, sessionID domain.SessionID) (Status, error) {
	m.mu.Lock()
	run := m.runs[sessionID]
	if run == nil {
		m.mu.Unlock()
		return stoppedStatus(sessionID), nil
	}
	if run.cmd == nil {
		run.status.State = StateStopped
		run.status.Error = ""
		status := m.statusForLocked(run)
		delete(m.runs, sessionID)
		m.persistProcessesLocked()
		m.mu.Unlock()
		return status, nil
	}
	run.stopping = true
	run.status.State = StateStopping
	cmd := run.cmd
	startTime := run.startTime
	done := run.done
	m.mu.Unlock()

	if err := terminatePreviewProcess(cmd, startTime); err != nil {
		m.log.Warn("stop preview process tree", "session", sessionID, "err", err)
	}
	select {
	case <-done:
		// The root has exited and been reaped. Descendants that ignored
		// SIGTERM may survive it, but the PID no longer provably belongs to
		// Tallpine's preview, so nothing is killed: a leaked preview process is
		// safer than a group kill landing on a recycled PID (issue #3475).
	case <-ctx.Done():
		go m.forceStopAfterGrace(sessionID, run, cmd, startTime, done)
		return m.Status(sessionID), ctx.Err()
	case <-time.After(5 * time.Second):
		_ = forceKillPreviewProcess(cmd, startTime)
		select {
		case <-done:
		case <-ctx.Done():
			go m.forceStopAfterGrace(sessionID, run, cmd, startTime, done)
			return m.Status(sessionID), ctx.Err()
		case <-time.After(time.Second):
			message := "preview server process did not exit after it was killed"
			m.mu.Lock()
			if m.runs[sessionID] == run {
				run.status.State = StateFailed
				run.status.Error = message
			}
			status := m.statusForLocked(run)
			m.mu.Unlock()
			return status, serviceError("PREVIEW_STOP_FAILED", message)
		}
	}

	m.mu.Lock()
	if m.runs[sessionID] == run {
		run.status.State = StateStopped
		run.status.Error = ""
	}
	status := m.statusForLocked(run)
	delete(m.runs, sessionID)
	m.persistProcessesLocked()
	m.mu.Unlock()
	return status, nil
}

func (m *Manager) acquireOperation(sessionID domain.SessionID) func() {
	m.operationsMu.Lock()
	operation := m.operations[sessionID]
	if operation == nil {
		operation = &sessionOperation{}
		m.operations[sessionID] = operation
	}
	operation.refs++
	m.operationsMu.Unlock()
	operation.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			operation.mu.Unlock()
			m.operationsMu.Lock()
			operation.refs--
			if operation.refs == 0 {
				delete(m.operations, sessionID)
			}
			m.operationsMu.Unlock()
		})
	}
}

// Status returns the latest managed preview state for a session.
func (m *Manager) Status(sessionID domain.SessionID) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	run := m.runs[sessionID]
	if run == nil {
		return stoppedStatus(sessionID)
	}
	return m.statusForLocked(run)
}

// StopSession implements session_manager.PreviewLifecycle without exposing status
// to lifecycle callers that only need best-effort teardown.
func (m *Manager) StopSession(ctx context.Context, sessionID domain.SessionID) error {
	_, err := m.Stop(ctx, sessionID)
	return err
}

// Close stops every server owned by the daemon. It is safe to call repeatedly.
func (m *Manager) Close() {
	m.mu.Lock()
	ids := make([]domain.SessionID, 0, len(m.runs))
	for id := range m.runs {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		_, _ = m.Stop(ctx, id)
		cancel()
	}
}

func (m *Manager) waitForExit(ctx context.Context, sessionID domain.SessionID, run *serverRun) {
	err := run.cmd.Wait()
	// Wait has returned, so the PID is back in the OS pool and no longer
	// provably Tallpine's. No escalation happens here: descendants the dead root
	// left behind are leaked rather than group-killed on a number that may
	// already belong to something else (issue #3475).
	crashed := false
	m.mu.Lock()
	if m.runs[sessionID] == run {
		run.cmd = nil
		if run.stopping {
			if run.status.State != StateFailed {
				run.status.State = StateStopped
				run.status.Error = ""
			}
		} else {
			run.status.State = StateFailed
			if err != nil {
				run.status.Error = fmt.Sprintf("preview server exited: %v", err)
			} else {
				run.status.Error = "preview server exited"
			}
			crashed = true
		}
	}
	m.persistProcessesLocked()
	m.mu.Unlock()
	close(run.done)
	if crashed {
		// Snapshot the failed status under the lock and notify the registered
		// listener (e.g. the daemon's session service) so the Browser panel
		// can be told the backing server is gone. After failedStatusRetention
		// the failed run is removed and status reports "stopped" with no
		// error, so this is the only window to surface the failure.
		// ctx is the request-scoped context passed by Start with
		// context.WithoutCancel, so caller cancellation cannot kill the
		// callback (gosec G118 wants a request-scoped ancestor).
		status := m.statusFor(run)
		if fn := m.onExitCallback(); fn != nil {
			notifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			fn(notifyCtx, sessionID, status)
			cancel()
		}
	}
	time.AfterFunc(failedStatusRetention, func() {
		m.mu.Lock()
		if m.runs[sessionID] == run && run.cmd == nil {
			delete(m.runs, sessionID)
			m.persistProcessesLocked()
		}
		m.mu.Unlock()
	})
}

func (m *Manager) failAndStop(
	sessionID domain.SessionID,
	run *serverRun,
	code string,
	message string,
) (Status, error) {
	m.mu.Lock()
	if m.runs[sessionID] == run {
		run.status.State = StateFailed
		run.status.Error = agentlaunch.RedactValues(message, run.projectEnv)
		run.stopping = true
	}
	cmd := run.cmd
	startTime := run.startTime
	m.mu.Unlock()
	if cmd != nil {
		_ = terminatePreviewProcess(cmd, startTime)
		select {
		case <-run.done:
			// Reaped: the PID is no longer provably Tallpine's, nothing to escalate.
		case <-time.After(3 * time.Second):
			_ = forceKillPreviewProcess(cmd, startTime)
		}
	}
	return m.statusFor(run), serviceError(code, agentlaunch.RedactValues(message, run.projectEnv))
}

func (m *Manager) probe(ctx context.Context, target string) error {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.CopyN(io.Discard, resp.Body, 1024)
	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("preview returned %s", resp.Status)
	}
	return nil
}

func (m *Manager) statusFor(run *serverRun) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusForLocked(run)
}

func (m *Manager) statusForLocked(run *serverRun) Status {
	status := run.status
	status.Logs = run.logs.Last(statusLogLines)
	status.Error = agentlaunch.RedactValues(status.Error, run.projectEnv)
	for i := range status.Logs {
		status.Logs[i] = agentlaunch.RedactValues(status.Logs[i], run.projectEnv)
	}
	if status.Logs == nil {
		status.Logs = []string{}
	}
	return status
}

func (m *Manager) forceStopAfterGrace(
	sessionID domain.SessionID,
	run *serverRun,
	cmd *exec.Cmd,
	startTime string,
	done <-chan struct{},
) {
	select {
	case <-done:
		// Reaped: the PID is no longer provably Tallpine's, nothing to escalate.
	case <-time.After(5 * time.Second):
		_ = forceKillPreviewProcess(cmd, startTime)
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
	m.mu.Lock()
	if m.runs[sessionID] == run && run.cmd == nil {
		delete(m.runs, sessionID)
		m.persistProcessesLocked()
	}
	m.mu.Unlock()
}

func stoppedStatus(sessionID domain.SessionID) Status {
	return Status{SessionID: sessionID, State: StateStopped, Logs: []string{}}
}

func serviceError(code, message string) Error {
	return Error{Code: code, Message: message}
}
