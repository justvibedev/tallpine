package bgtasks

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// B562: a background subagent is listed with its model and the time it was
// first seen, so the chat can say what runs and for how long.
func TestRunningListsSubagentWithModelAndStart(t *testing.T) {
	tr := New(nil, nil, 0)
	start := time.Date(2026, 11, 5, 8, 45, 2, 0, time.UTC)
	tr.now = func() time.Time { return start }
	tr.Apply("s", ports.ChatAsyncTask{ID: "a1", Name: "Fable planner", TaskType: ports.ChatAsyncTaskSubagent, Model: "fable", State: ports.ChatAsyncTaskRunning})
	tr.now = func() time.Time { return start.Add(time.Minute) }
	tr.Apply("s", ports.ChatAsyncTask{ID: "a1", State: ports.ChatAsyncTaskRunning}) // a later report keeps the first start
	got := tr.Running("s")
	if len(got) != 1 || got[0].Kind != ports.ChatAsyncTaskSubagent || got[0].Model != "fable" ||
		got[0].Name != "Fable planner" || !got[0].StartedAt.Equal(start) {
		t.Fatalf("running = %+v, want the subagent with its model and first start", got)
	}
}

// The parent wakes itself on the subagent's task notification; a second wake
// from the tracker would be a duplicate prompt.
func TestSubagentEndNeverWakesTheAgent(t *testing.T) {
	tr, rec := newTracker(true)
	tr.Apply("s", ports.ChatAsyncTask{ID: "a1", Name: "planner", TaskType: ports.ChatAsyncTaskSubagent, State: ports.ChatAsyncTaskRunning})
	tr.Apply("s", ports.ChatAsyncTask{ID: "a1", State: ports.ChatAsyncTaskCompleted})
	time.Sleep(80 * time.Millisecond)
	if n := rec.count(); n != 0 {
		t.Fatalf("wake messages = %d, want 0 for a subagent", n)
	}
	if got := tr.Running("s"); len(got) != 0 {
		t.Fatalf("running after end = %+v", got)
	}
}

// Clients re-read a session only when told, so every add and removal of a
// running task (not progress) notifies.
func TestOnChangeFiresOnStartEndAndClear(t *testing.T) {
	var changes []domain.SessionID
	tr := New(nil, nil, time.Hour).OnChange(func(id domain.SessionID) { changes = append(changes, id) })
	tr.Apply("s", ports.ChatAsyncTask{ID: "a1", TaskType: ports.ChatAsyncTaskSubagent, State: ports.ChatAsyncTaskRunning})
	tr.Apply("s", ports.ChatAsyncTask{ID: "a1", State: ports.ChatAsyncTaskRunning})
	tr.Apply("s", ports.ChatAsyncTask{ID: "a1", State: ports.ChatAsyncTaskCompleted})
	tr.Apply("s", ports.ChatAsyncTask{ID: "a1", State: ports.ChatAsyncTaskCompleted})
	tr.Apply("s", ports.ChatAsyncTask{ID: "a2", TaskType: ports.ChatAsyncTaskSubagent, State: ports.ChatAsyncTaskRunning})
	tr.Clear("s")
	tr.Clear("s")
	if len(changes) != 4 {
		t.Fatalf("changes = %v, want start, end, start, clear", changes)
	}
}
