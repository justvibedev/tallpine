package systeminstall

import (
	"context"
	"io"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// noopIdleCoordinator reports every session idle and stops nothing.
type noopIdleCoordinator struct{}

func (noopIdleCoordinator) BusyHarnessSessions(context.Context, domain.AgentHarness) ([]domain.SessionWait, error) {
	return nil, nil
}

func (noopIdleCoordinator) StopHarnessSessions(context.Context, domain.AgentHarness) ([]domain.SessionID, error) {
	return nil, nil
}

func (noopIdleCoordinator) ResumeHarnessSessions(context.Context, []domain.SessionID) error {
	return nil
}

func TestSingleBinaryCaskUpdatesBesideRunningSessions(t *testing.T) {
	for _, tt := range []struct {
		target Target
		pkg    string
		want   UpdateWhileRunning
	}{
		{TargetClaudeCode, "claude-code", UpdateWhileRunningSafe},
		// Only the audited cask is known to ship a single binary.
		{TargetClaudeCode, "claude-code@latest", UpdateWhileRunningWarn},
		{TargetClaudeCode, "", UpdateWhileRunningWarn},
		// Codex's cask also ships codex-resources.
		{TargetCodex, "codex", UpdateWhileRunningWarn},
	} {
		if got := updateWhileRunningLevel("darwin", tt.target, "homebrew", true, tt.pkg); got != tt.want {
			t.Errorf("%s cask %q = %s, want %s", tt.target, tt.pkg, got, tt.want)
		}
	}
	if got := updateWhileRunningLevel("windows", TargetClaudeCode, "homebrew", true, "claude-code"); got != UpdateWhileRunningBlocked {
		t.Errorf("windows = %s, want blocked", got)
	}
}

func TestUpdatesThatCannotRunBesideSessionsWaitForIdleOnEveryPlatform(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		s := newTestService(goos)
		s.updateWhenIdle = true
		s.SetIdleUpdateCoordinator(noopIdleCoordinator{})
		for _, level := range []UpdateWhileRunning{UpdateWhileRunningWarn, UpdateWhileRunningBlocked} {
			if got := s.effectiveUpdateLevel(level); got != UpdateWhileRunningIdle {
				t.Errorf("%s %s = %s, want idle", goos, level, got)
			}
		}
		if got := s.effectiveUpdateLevel(UpdateWhileRunningSafe); got != UpdateWhileRunningSafe {
			t.Errorf("%s safe = %s", goos, got)
		}
		s.updateWhenIdle = false
		if got := s.effectiveUpdateLevel(UpdateWhileRunningWarn); got != UpdateWhileRunningBlocked {
			t.Errorf("%s warn without update-when-idle = %s, want blocked", goos, got)
		}
	}
}

// Regression: StartAgentOperation recorded the level under s.mu, and
// effectiveUpdateLevel locks s.mu to check the coordinator, so an update with
// update-when-idle on and no live session hung the daemon.
func TestUpdateWhenIdleWithoutSessionsDoesNotDeadlock(t *testing.T) {
	s := newTestService("darwin", "bash", "sh")
	s.updateWhenIdle = true
	s.SetIdleUpdateCoordinator(noopIdleCoordinator{})
	s.installScripts = installScriptRunnerFunc(func(context.Context, ports.InstallScriptCommand, io.Writer, io.Writer) (ports.InstallScriptResult, error) {
		return ports.InstallScriptResult{}, nil
	})
	s.mu.Lock()
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetCodex: {
		AgentID: string(TargetCodex), Status: UpdateStatusBehindLatest, LatestVersion: "1.19.3",
		MaintenanceMethod: "official-installer", CheckedAt: time.Now(),
	}}
	s.mu.Unlock()
	started := make(chan error, 1)
	go func() {
		_, err := s.StartAgentOperation(context.Background(), TargetCodex, "official-installer", AgentOperationUpdate, "")
		started <- err
	}()
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("update = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartAgentOperation deadlocked")
	}
	s.workers.Wait()
}
