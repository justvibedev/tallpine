package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type launchCommandFailAgent struct{ fakeAgent }

func (launchCommandFailAgent) GetLaunchCommand(context.Context, ports.LaunchConfig) ([]string, error) {
	return nil, errors.New("no launch command")
}

// A failure in a middle Spawn stage (building the launch command) must roll the
// worktree and seed row back and release the installer guard held since the
// harness stage.
func TestSpawn_LaunchCommandFailureRollsBackWorkspaceSeedAndHarnessGuard(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	m.agents = singleAgent{agent: launchCommandFailAgent{}}
	gate := &countingHarnessUseGate{}
	m.SetHarnessUseGate(gate)

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer"})
	if !errors.Is(err, ErrSpawnLaunchCommand) {
		t.Fatalf("Spawn error = %v, want %v", err, ErrSpawnLaunchCommand)
	}
	if ws.destroyed != 1 {
		t.Fatalf("workspace destroyed %d times, want 1", ws.destroyed)
	}
	if rec, present := st.sessions["mer-1"]; present {
		t.Fatalf("seed row must be rolled back, got %+v", rec)
	}
	if gate.active != 0 {
		t.Fatalf("harness installer guard still held (%d) after failed spawn", gate.active)
	}
}

// A runtime-create failure comes after the launch was registered with the
// lifecycle manager and the Codex-admission step: the registered launch must be
// cancelled, the workspace rolled back, and the installer guard released.
func TestSpawn_RuntimeFailureCancelsLaunchAndReleasesHarnessGuard(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	m.runtime = &fakeRuntime{createErr: errors.New("boom")}
	gate := &countingHarnessUseGate{}
	m.SetHarnessUseGate(gate)

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer"})
	if !errors.Is(err, ErrRuntimeCreate) {
		t.Fatalf("Spawn error = %v, want %v", err, ErrRuntimeCreate)
	}
	lcm := m.lcm.(*fakeLCM)
	if len(lcm.prepared) != 1 || len(lcm.cancelled) != 1 || lcm.cancelled[0] != lcm.prepared[0] {
		t.Fatalf("prepared launches %v, cancelled %v; want the prepared launch cancelled once", lcm.prepared, lcm.cancelled)
	}
	if ws.destroyed != 1 {
		t.Fatalf("workspace destroyed %d times, want 1", ws.destroyed)
	}
	if _, present := st.sessions["mer-1"]; present {
		t.Fatal("seed row must be deleted before a runtime handle is live")
	}
	if gate.active != 0 {
		t.Fatalf("harness installer guard still held (%d) after failed spawn", gate.active)
	}
}

// A provisioning failure happens after the worktree exists but before any
// launch work: only the workspace and seed row are rolled back.
func TestSpawn_ProvisionFailureRollsBackWorkspaceAndSeed(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{
		Symlinks: []string{"../outside"},
		Worker:   domain.RoleOverride{Harness: domain.HarnessClaudeCode},
	}}
	ws.path = t.TempDir()

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer"})
	if !errors.Is(err, ErrWorkspaceProvision) || !strings.Contains(err.Error(), "mer-1") {
		t.Fatalf("Spawn error = %v, want %v for mer-1", err, ErrWorkspaceProvision)
	}
	if ws.destroyed != 1 {
		t.Fatalf("workspace destroyed %d times, want 1", ws.destroyed)
	}
	if rt.created != 0 {
		t.Fatalf("runtime created %d times before provisioning succeeded", rt.created)
	}
	if rec, present := st.sessions["mer-1"]; present {
		t.Fatalf("seed row must be rolled back, got %+v", rec)
	}
}
