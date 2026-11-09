package previewserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// Start loads a named configuration, replaces any existing managed server for
// the session, and waits until the configured loopback URL responds.
func (m *Manager) Start(
	ctx context.Context,
	sessionID domain.SessionID,
	workspacePath string,
	configurationName string,
	projectEnv map[string]string,
	fence Fence,
) (Status, error) {
	releaseOperation := m.acquireOperation(sessionID)
	operationLocked := true
	defer func() {
		if operationLocked {
			releaseOperation()
		}
	}()

	cfg, err := loadConfiguration(workspacePath, configurationName)
	if err != nil {
		return stoppedStatus(sessionID), err
	}
	workingDir, err := resolveWorkingDirectory(workspacePath, cfg.Cwd)
	if err != nil {
		return stoppedStatus(sessionID), err
	}
	validationPort := cfg.Port
	if validationPort == 0 {
		validationPort = 1
	}
	if _, err := resolveTargetURL(cfg.URL, validationPort); err != nil {
		return stoppedStatus(sessionID), err
	}
	// Before the stop below, so a fence that cannot be built leaves a running
	// server alone.
	fenceProfile, fenceEnv, err := fence.resolve(workspacePath)
	if err != nil {
		return stoppedStatus(sessionID), err
	}
	if _, err := m.stop(context.Background(), sessionID); err != nil {
		return stoppedStatus(sessionID), err
	}

	port, reservation, err := reservePort(cfg.Port, cfg.AutoPort)
	if err != nil {
		var previewErr Error
		if errors.As(err, &previewErr) {
			return stoppedStatus(sessionID), previewErr
		}
		return stoppedStatus(sessionID), serviceError("PREVIEW_PORT_UNAVAILABLE", err.Error())
	}
	defer func() {
		if reservation != nil {
			_ = reservation.Close()
		}
	}()
	targetURL, err := resolveTargetURL(cfg.URL, port)
	if err != nil {
		return stoppedStatus(sessionID), err
	}
	executable := interpolatePort(cfg.RuntimeExecutable, port)
	if strings.ContainsAny(executable, `/\`) && !filepath.IsAbs(executable) {
		executable = filepath.Join(workingDir, executable)
	}
	args := make([]string, len(cfg.RuntimeArgs))
	for i, arg := range cfg.RuntimeArgs {
		args[i] = interpolatePort(arg, port)
	}

	argv := append([]string{executable}, args...)
	if fenceProfile != "" {
		argv = statefence.Wrap(argv, fenceProfile)
	}
	cmd := previewCommand(argv[0], argv[1:]...)
	cmd.Dir = workingDir
	// Appended last so a launch.json env cannot move TMPDIR out of the fence.
	cmd.Env = append(previewEnvironment(os.Environ(), projectEnv, cfg.Env, sessionID, port), fenceEnv...)
	logs := newLineBuffer(maxBufferedLogLines)
	cmd.Stdout = logs
	cmd.Stderr = logs

	run := &serverRun{
		projectEnv: agentlaunch.MergeEnv(projectEnv, nil),
		status: Status{
			SessionID:     sessionID,
			State:         StateStarting,
			Configuration: cfg.Name,
			TargetKind:    cfg.TargetKind,
			URL:           targetURL,
			Port:          port,
			StartedAt:     time.Now().UTC(),
			Logs:          []string{},
		},
		cmd:  cmd,
		done: make(chan struct{}),
		logs: logs,
	}
	// Arbitrary project servers cannot inherit a generic pre-bound socket. Keep
	// the selected port reserved through command construction, then release it
	// at the last possible moment before launch to minimize the bind race.
	if err := reservation.Close(); err != nil {
		return stoppedStatus(sessionID), serviceError("PREVIEW_PORT_UNAVAILABLE", fmt.Sprintf("release selected preview port: %v", err))
	}
	reservation = nil
	if err := cmd.Start(); err != nil {
		run.cmd = nil
		run.status.State = StateFailed
		run.status.Error = agentlaunch.RedactValues(fmt.Sprintf("start preview server: %v", err), projectEnv)
		return m.statusFor(run), serviceError("PREVIEW_START_FAILED", run.status.Error)
	}
	run.startTime = previewProcessStartTime(cmd.Process.Pid)
	if run.startTime == "" {
		m.log.Warn("could not capture preview process start time; teardown will only signal the direct child",
			"session", sessionID, "pid", cmd.Process.Pid)
	}

	m.mu.Lock()
	m.runs[sessionID] = run
	m.persistProcessesLocked()
	m.mu.Unlock()
	// Pass the request-scoped context into the wait goroutine so OnExit
	// callbacks (issue #4500) inherit a context the gosec G118 check accepts.
	// The wait goroutine detaches cancellation with context.WithoutCancel so a
	// caller request ending after launch does not cancel the OnExit call.
	go m.waitForExit(context.WithoutCancel(ctx), sessionID, run)
	releaseOperation()
	operationLocked = false

	timeout := readyTimeout(cfg.ReadyTimeoutMillis)
	deadline := time.Now().Add(timeout)
	for {
		if err := m.probe(ctx, targetURL); err == nil {
			m.mu.Lock()
			ready := false
			if m.runs[sessionID] == run && run.cmd != nil && !run.stopping {
				run.status.State = StateReady
				run.status.Error = ""
				ready = true
			}
			status := m.statusForLocked(run)
			m.mu.Unlock()
			if !ready {
				return status, serviceError("PREVIEW_START_CANCELED", "preview server was stopped before becoming ready")
			}
			m.log.Info("preview server ready", "session", sessionID, "configuration", cfg.Name, "url", targetURL)
			return status, nil
		}

		select {
		case <-ctx.Done():
			message := "preview start canceled: " + ctx.Err().Error()
			return m.failAndStop(sessionID, run, "PREVIEW_START_CANCELED", message)
		case <-run.done:
			status := m.statusFor(run)
			if status.Error == "" {
				status.Error = "preview server exited before becoming ready"
			}
			return status, serviceError("PREVIEW_EXITED", status.Error)
		default:
		}
		if time.Now().After(deadline) {
			message := fmt.Sprintf("preview server was not ready at %s within %s", targetURL, timeout)
			return m.failAndStop(sessionID, run, "PREVIEW_NOT_READY", message)
		}
		timer := time.NewTimer(probeInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			message := "preview start canceled: " + ctx.Err().Error()
			return m.failAndStop(sessionID, run, "PREVIEW_START_CANCELED", message)
		case <-run.done:
			timer.Stop()
			status := m.statusFor(run)
			if status.Error == "" {
				status.Error = "preview server exited before becoming ready"
			}
			return status, serviceError("PREVIEW_EXITED", status.Error)
		case <-timer.C:
		}
	}
}
