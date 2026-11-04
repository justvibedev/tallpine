package bgtasks

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

type recorder struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recorder) send(_ context.Context, _ domain.SessionID, m string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, m)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.msgs)
}

func newTracker(idle bool) (*Tracker, *recorder) {
	r := &recorder{}
	return New(SendFunc(r.send), func(context.Context, domain.SessionID) bool { return idle }, 15*time.Millisecond), r
}

func spawn(id, name string) ports.ChatAsyncTask {
	return ports.ChatAsyncTask{ID: id, Name: name, TaskType: "monitor", State: ports.ChatAsyncTaskRunning}
}

func done(id string) ports.ChatAsyncTask {
	return ports.ChatAsyncTask{ID: id, State: ports.ChatAsyncTaskCompleted, Summary: "exit 0"}
}

func TestTaskStartAndCompleteUpdateWaiting(t *testing.T) {
	tr, _ := newTracker(false)
	tr.Apply("s", spawn("t1", "go test suites"))
	tr.Apply("s", spawn("t2", "ci watch"))
	if got := tr.Waiting("s"); len(got) != 2 || got[0] != "ci watch" || got[1] != "go test suites" {
		t.Fatalf("waiting = %v", got)
	}
	tr.Apply("s", done("t1"))
	if got := tr.Waiting("s"); len(got) != 1 || got[0] != "ci watch" {
		t.Fatalf("after completing t1 waiting = %v", got)
	}
	tr.Apply("s", done("t2"))
	if got := tr.Waiting("s"); len(got) != 0 {
		t.Fatalf("after completing all waiting = %v", got)
	}
}

func TestCompletionWakesSettledWorkerExactlyOnce(t *testing.T) {
	tr, rec := newTracker(true)
	tr.Apply("s", spawn("t1", "monitor"))
	tr.Apply("s", done("t1"))
	tr.Apply("s", done("t1")) // repeated terminal report
	time.Sleep(80 * time.Millisecond)
	if n := rec.count(); n != 1 {
		t.Fatalf("wake messages = %d, want 1", n)
	}
}

func TestNoWakeWhenHarnessWokeItselfOrWorkerBusy(t *testing.T) {
	tr, rec := newTracker(true)
	tr.Apply("s", spawn("t1", "monitor"))
	tr.Apply("s", done("t1"))
	tr.Apply("s", ports.ChatAsyncTask{ID: "turn-1", TaskType: "turn", State: ports.ChatAsyncTaskRunning})
	busy, rec2 := newTracker(false)
	busy.Apply("b", spawn("t1", "monitor"))
	busy.Apply("b", done("t1"))
	time.Sleep(80 * time.Millisecond)
	if rec.count() != 0 || rec2.count() != 0 {
		t.Fatalf("unexpected wake: self-woke=%d busy=%d", rec.count(), rec2.count())
	}
}

// Frame shapes of claude-agent-acp async-tasks.js: the autonomous turn task
// spawns with taskType "turn" (:339-349); its progress (:643) and end (:662)
// carry only the id and state.
func TestAutonomousTurnEndDoesNotWakeOrShowAsWaiting(t *testing.T) {
	tr, rec := newTracker(true)
	tr.Apply("s", ports.ChatAsyncTask{ID: "autonomous-turn-1", Name: "Claude is working", TaskType: "turn", State: ports.ChatAsyncTaskRunning})
	tr.Apply("s", ports.ChatAsyncTask{ID: "autonomous-turn-1", Description: "cause"})
	if got := tr.Waiting("s"); len(got) != 0 {
		t.Fatalf("turn progress shows as waiting: %v", got)
	}
	tr.Apply("s", ports.ChatAsyncTask{ID: "autonomous-turn-1", State: ports.ChatAsyncTaskCompleted})
	time.Sleep(80 * time.Millisecond)
	if rec.count() != 0 || len(tr.Waiting("s")) != 0 {
		t.Fatalf("turn end woke the worker (%d) or left waiting %v", rec.count(), tr.Waiting("s"))
	}
}

func TestClearDropsTasksOfReplacedHarness(t *testing.T) {
	tr, rec := newTracker(true)
	tr.Apply("s", spawn("t1", "monitor"))
	tr.Clear("s")
	if got := tr.Waiting("s"); len(got) != 0 {
		t.Fatalf("stale waiting after harness replaced: %v", got)
	}
	time.Sleep(40 * time.Millisecond)
	if rec.count() != 0 {
		t.Fatalf("cleared session woke %d times", rec.count())
	}
}

func TestNoWakeWhenTurnTaskOpenAtEnd(t *testing.T) {
	tr, rec := newTracker(true)
	tr.Apply("s", spawn("t1", "monitor"))
	tr.Apply("s", ports.ChatAsyncTask{ID: "autonomous-turn-1", TaskType: "turn", State: ports.ChatAsyncTaskRunning})
	tr.Apply("s", done("t1")) // the open turn receives the result
	tr.Apply("s", ports.ChatAsyncTask{ID: "autonomous-turn-1", State: ports.ChatAsyncTaskCompleted})
	time.Sleep(80 * time.Millisecond)
	if rec.count() != 0 {
		t.Fatalf("wake sent while a turn task was open: %d", rec.count())
	}
}

func TestNoWakeWhenSessionBusyAtEnd(t *testing.T) {
	var idle atomic.Bool
	r := &recorder{}
	tr := New(SendFunc(r.send), func(context.Context, domain.SessionID) bool { return idle.Load() }, 15*time.Millisecond)
	tr.Apply("s", spawn("t1", "monitor"))
	tr.Apply("s", done("t1")) // a prompted turn is running now
	idle.Store(true)          // and finished inside the grace window
	time.Sleep(80 * time.Millisecond)
	if r.count() != 0 {
		t.Fatalf("wake sent although a running turn took the result: %d", r.count())
	}
}

func TestRunningListsLiveTasksWithTheirIDsAndNotTurns(t *testing.T) {
	tr := New(nil, nil, 0)
	tr.Apply("s1", ports.ChatAsyncTask{ID: "b", Name: "tail -f", State: ports.ChatAsyncTaskRunning})
	tr.Apply("s1", ports.ChatAsyncTask{ID: "a", Description: "watch ci", State: ports.ChatAsyncTaskRunning})
	tr.Apply("s1", ports.ChatAsyncTask{ID: "turn-1", TaskType: "turn", State: ports.ChatAsyncTaskRunning})
	got := tr.Running("s1")
	if len(got) != 2 || got[0].ID != "a" || got[0].Name != "watch ci" || got[1].ID != "b" || got[1].Name != "tail -f" {
		t.Fatalf("running = %+v, want a then b and no turn task", got)
	}
	tr.Apply("s1", ports.ChatAsyncTask{ID: "a", State: ports.ChatAsyncTaskStopped})
	if got := tr.Running("s1"); len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("after stop = %+v, want only b", got)
	}
	if tr.Running("other") != nil {
		t.Fatal("unknown session lists tasks")
	}
}

// A task a person stopped ends like any other, but the agent is not prompted to
// "continue": it would pay a turn, and might restart the task just stopped.
func TestDeliberatelyStoppedTaskDoesNotWakeTheAgent(t *testing.T) {
	tr, rec := newTracker(true)
	tr.Apply("s", spawn("t1", "watch ci"))
	tr.ExpectStop("s", "t1")
	tr.Apply("s", done("t1"))
	time.Sleep(150 * time.Millisecond)
	if n := rec.count(); n != 0 {
		t.Fatalf("a stopped task woke the agent %d time(s)", n)
	}
	// A task that ends on its own still does.
	tr.Apply("s", spawn("t2", "tests"))
	tr.Apply("s", done("t2"))
	time.Sleep(150 * time.Millisecond)
	if n := rec.count(); n != 1 {
		t.Fatalf("an ordinary end woke the agent %d time(s), want 1", n)
	}
}

func TestCancelledStopStillWakesOnTheTasksEnd(t *testing.T) {
	tr, rec := newTracker(true)
	tr.Apply("s", spawn("t1", "watch ci"))
	tr.ExpectStop("s", "t1")
	tr.CancelStop("s", "t1") // the stop was refused
	tr.Apply("s", done("t1"))
	time.Sleep(150 * time.Millisecond)
	if n := rec.count(); n != 1 {
		t.Fatalf("woke %d time(s), want 1", n)
	}
}
