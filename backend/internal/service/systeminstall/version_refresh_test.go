package systeminstall

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type updateObserverStub struct {
	mu       sync.Mutex
	marked   []string
	stale    map[domain.AgentHarness]int64
	gateHeld func() bool
	heldMark bool
}

func (o *updateObserverStub) MarkHarnessUpdated(_ context.Context, harness domain.AgentHarness, version string, _ time.Time) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.marked = append(o.marked, string(harness)+"@"+version)
	if o.gateHeld != nil {
		o.heldMark = o.gateHeld()
	}
	return nil
}

func (o *updateObserverStub) CountStaleBinarySessions(context.Context) (map[domain.AgentHarness]int64, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stale, nil
}

func TestFailedUpdateMarksNothing(t *testing.T) {
	s, _ := updateWhileRunningService(t, nil)
	observer := &updateObserverStub{}
	s.SetHarnessUpdateObserver(observer)
	s.installCommands = installCommandRunnerFunc(func(_ context.Context, command ports.InstallCommand, _, _ io.Writer) error {
		if command.Argv[1] == "upgrade" {
			return errors.New("upgrade failed")
		}
		return nil
	})
	if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "homebrew", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	waitForStatus(t, s, TargetCodex, StatusFailed)
	if len(observer.marked) != 0 {
		t.Fatalf("marked = %v after a failed update", observer.marked)
	}
}

func TestUpdateWithoutSessionsDefersNoCleanup(t *testing.T) {
	s, ran := updateWhileRunningService(t, nil)
	s.SetHarnessUpdateObserver(&updateObserverStub{})
	if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "homebrew", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	waitForStatus(t, s, TargetCodex, StatusSucceeded)
	before := len(*ran)
	s.RunDeferredCleanups(context.Background())
	if len(*ran) != before {
		t.Fatalf("ran %v without a kept keg", (*ran)[before:])
	}
}
