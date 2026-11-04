package sessionmanager

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func spawnInBackground(m *Manager) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
		done <- err
	}()
	return done
}

// An orchestrator fanning out workers into one project must not queue each
// spawn behind the previous one's fetch, checkout, provisioning and launch.
func TestSpawnDoesNotWaitForAnotherStartInTheProject(t *testing.T) {
	t.Parallel()
	m, _, _, _ := newManager()
	releaseOther := m.acquireSpawnWorkspaceGate("mer")
	defer releaseOther()
	select {
	case err := <-spawnInBackground(m):
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("spawn waited for another start in the same project")
	}
}

func TestSpawnWaitsForProjectTeardown(t *testing.T) {
	t.Parallel()
	m, _, _, _ := newManager()
	release := m.acquireWorkspaceGate("mer")
	done := spawnInBackground(m)
	select {
	case err := <-done:
		t.Fatalf("spawn ran while restore or cleanup held the project: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("spawn did not resume after teardown released the project")
	}
}

func TestProjectTeardownWaitsForEveryStart(t *testing.T) {
	t.Parallel()
	m, _, _, _ := newManager()
	first := m.acquireSpawnWorkspaceGate("mer")
	second := m.acquireSpawnWorkspaceGate("mer")
	acquired := make(chan func(), 1)
	go func() { acquired <- m.acquireWorkspaceGate("mer") }()
	first()
	select {
	case release := <-acquired:
		release()
		t.Fatal("teardown acquired the project while a start still held it")
	case <-time.After(100 * time.Millisecond):
	}
	second()
	select {
	case release := <-acquired:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("teardown did not acquire the project after every start released it")
	}
}
