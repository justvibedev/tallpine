package systeminstall

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestLaunchWaitsForAHarnessChangeInsteadOfFailing(t *testing.T) {
	s := newTestService("windows")
	gate := s.harnessGate(domain.AgentHarness(Target("auggie")))
	gate.Lock()
	s.setHarnessChanging(Target("auggie"), true)
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.setHarnessChanging(Target("auggie"), false)
		gate.Unlock()
	}()
	started := time.Now()
	release, ok := s.TryBeginHarnessUse(domain.AgentHarness(Target("auggie")))
	if !ok {
		t.Fatal("launch failed instead of waiting for the update batch")
	}
	release()
	if time.Since(started) < 250*time.Millisecond {
		t.Fatal("launch did not wait for the batch")
	}
}
