package sessionmanager

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestSpawn_AfterStartPromptReservesCallerDeadlineForFallbackDelivery(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{outputs: []string{"still booting"}}
	msg := &fakeMessenger{}
	agent := &recordingAgent{}
	m := New(Deps{
		Runtime: rt,
		Agents: singleAgent{agent: readinessAgent{
			afterStartAgent: afterStartAgent{recordingAgent: agent},
			hints: ports.PromptReadinessHints{
				InitialDelay: time.Second,
				Patterns:     []string{"Ready..."},
				PollInterval: time.Millisecond,
				Timeout:      time.Second,
			},
		}},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: msg,
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})

	spawnCtx, cancel := context.WithTimeout(context.Background(), promptDeliveryDeadlineReserve)
	defer cancel()
	if _, _, _, err := m.Spawn(spawnCtx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "fix the button"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := spawnCtx.Err(); err != nil {
		t.Fatalf("spawn exhausted caller deadline before fallback delivery: %v", err)
	}
	// B346: assert the reservation itself, not elapsed wall-clock. The caller
	// deadline equals the reserve, so readiness must be skipped outright (no
	// output polling); without the reserve it would sleep InitialDelay and poll.
	if rt.outputCalls != 0 {
		t.Fatalf("readiness polled output %d times, want skipped to keep the delivery reserve", rt.outputCalls)
	}
	if len(msg.msgs) != 1 || msg.msgs[0] != "fix the button" {
		t.Fatalf("delivered prompts = %#v, want fallback prompt delivery", msg.msgs)
	}
}
