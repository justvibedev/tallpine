package sessionmanager

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B753: quitting the app must be able to say how many agents keep running and
// stop all of them. Exited, ended and sleeping agents are not running; one
// agent that cannot be stopped does not keep the others running.
func TestExitAllAgentsStopsEveryRunningAgentAndKeepsSessions(t *testing.T) {
	t.Parallel()

	m, st, runtime, _ := newManager()
	m.exitAllParallel = 1 // the fakes are not safe for concurrent use
	session := func(id string, state domain.ActivityState) domain.SessionRecord {
		return domain.SessionRecord{
			ID: domain.SessionID(id), ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
			Activity: domain.Activity{State: state},
			Metadata: domain.SessionMetadata{
				WorkspacePath: "/ws/" + id, RuntimeHandleID: "tmux-" + id, RuntimeLaunchID: "launch-" + id, AgentSessionID: "native-" + id,
			},
		}
	}
	st.sessions["mer-1"] = session("mer-1", domain.ActivityIdle)
	st.sessions["mer-2"] = session("mer-2", domain.ActivityActive)
	st.sessions["mer-3"] = session("mer-3", domain.ActivityExited)
	ended := session("mer-4", domain.ActivityIdle)
	ended.IsTerminated = true
	st.sessions["mer-4"] = ended
	asleep := session("mer-5", domain.ActivityIdle)
	slept := time.Now()
	asleep.HibernatedAt = &slept
	st.sessions["mer-5"] = asleep
	broken := session("mer-6", domain.ActivityActive)
	broken.Metadata.RuntimeHandleID = "" // nothing to stop it with
	st.sessions["mer-6"] = broken
	runtime.aliveByHandle = map[string]bool{"tmux-mer-1": true, "tmux-mer-2": true}

	running, err := m.RunningAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(running)
	if fmt.Sprint(running) != "[mer-1 mer-2 mer-6]" {
		t.Fatalf("running = %v, want the live, awake agents", running)
	}

	stopped, failed, err := m.ExitAllAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(stopped)
	if fmt.Sprint(stopped) != "[mer-1 mer-2]" {
		t.Fatalf("stopped = %v, want every running agent that could stop", stopped)
	}
	if len(failed) != 1 {
		t.Fatalf("failed = %v, want the one agent without a runtime handle", failed)
	}
	destroyed := slices.Clone(runtime.destroyedIDs)
	slices.Sort(destroyed)
	if fmt.Sprint(destroyed) != "[tmux-mer-1 tmux-mer-2]" {
		t.Fatalf("destroyed = %v", destroyed)
	}
	for _, id := range []domain.SessionID{"mer-1", "mer-2"} {
		if got := st.sessions[id]; got.IsTerminated || got.Activity.State != domain.ActivityExited {
			t.Fatalf("%s = %+v, want its agent exited and the session kept", id, got)
		}
	}
	if got := st.sessions["mer-5"]; got.Activity.State != domain.ActivityIdle {
		t.Fatalf("a sleeping agent changed: %+v", got.Activity)
	}
}
