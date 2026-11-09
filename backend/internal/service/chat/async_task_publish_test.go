package chat_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// The daemon wires background-task reports into a tracker whose change hook
// writes to the store (wire_chat.go). That write takes the store's single
// writer lock, so the report must reach the tracker only after the projection
// transaction, which holds the lock, commits. Delivered inside it, the first
// background task a session started hung every store write in the daemon.
func TestAsyncTaskReportDoesNotDeadlockTheStoreWriter(t *testing.T) {
	st := openStore(t)
	conv := newFakeConversation()
	notified := make(chan error, 4)
	tracker := bgtasks.New(nil, nil, 0).OnChange(func(id domain.SessionID) {
		notified <- st.NotifySessionRuntimeChanged(context.Background(), id)
	})
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers:     fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:         slog.New(slog.DiscardHandler),
		NewID:       func() string { return "async-task-writer" },
		OnAsyncTask: tracker.Apply,
	})
	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventAsyncTask, ProviderEventID: "task-1", AsyncTask: &ports.ChatAsyncTask{
			ID: "bg-1", Name: "sleep 60", TaskType: "local_bash", State: ports.ChatAsyncTaskRunning,
		}},
		ports.ChatEvent{Kind: ports.ChatEventThreadRenamed, ProviderEventID: "title-1", Title: "after"},
	)
	select {
	case err := <-notified:
		if err != nil {
			t.Fatalf("NotifySessionRuntimeChanged: %v", err)
		}
	case <-time.After(5 * time.Second):
		// The controller goroutine is stuck holding the writer lock; stopping it
		// would hang the test too, so leave it behind.
		t.Fatal("background task report deadlocked the store writer")
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	if got := tracker.Waiting(testSession); len(got) != 1 {
		t.Fatalf("tracker waiting = %v, want the one running task", got)
	}
	// Later writes still go through: the rename after the task commits.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		events, err := st.ProviderEventsSince(context.Background(), ctrl.ConversationID(), 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("archive = %d events, want the task and the rename", len(events))
		}
	}
}

// A task report whose archive row rolls back was never recorded, so the daemon's
// tracker must not hear about it; the provider's replay will.
func TestAsyncTaskIsNotPublishedWhenItsArchiveRollsBack(t *testing.T) {
	st := openStore(t)
	conv := newFakeConversation()
	failing := &failingProjectStore{
		Store: st, failMethod: string(ports.ChatEventAsyncTask), failed: make(chan struct{}),
	}
	var published atomic.Int32
	svc := chatsvc.New(chatsvc.Options{
		Store: failing, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "rolled-back-task" },
		OnAsyncTask: func(domain.SessionID, ports.ChatAsyncTask) {
			published.Add(1)
		},
	})
	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventAsyncTask, ProviderEventID: "task-1", AsyncTask: &ports.ChatAsyncTask{
			ID: "bg-1", Name: "sleep 60", State: ports.ChatAsyncTaskRunning,
		}},
		// A later event that commits proves the rolled-back one was fully handled.
		ports.ChatEvent{Kind: ports.ChatEventThreadRenamed, ProviderEventID: "title-1", Title: "after"},
	)
	<-failing.failed
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		events, err := st.ProviderEventsSince(context.Background(), ctrl.ConversationID(), 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("archive = %d events, want only the committed rename", len(events))
		}
	}
	if n := published.Load(); n != 0 {
		t.Fatalf("task published %d times for an archive row that rolled back", n)
	}
}

// Jobs re-seeded at boot for a host that survived the restart belong to that
// host. When the session instead gets a fresh provider (the reconnect failed),
// no controller ever closed to clear them, so the start must.
func TestFreshProviderStartDropsJobsRestoredForTheOldHost(t *testing.T) {
	st := openStore(t)
	tracker := bgtasks.New(nil, nil, 0)
	tracker.Restore(testSession, []bgtasks.Info{{ID: "old-job", Name: "CI watch", Kind: "monitor"}})
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers:          fakeRegistry{driver: fakeDriver{conv: newFakeConversation()}},
		Log:              slog.New(slog.DiscardHandler),
		NewID:            func() string { return "fresh-start" },
		OnAsyncTask:      tracker.Apply,
		OnAsyncTasksGone: tracker.Clear,
	})
	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	if got := tracker.Running(testSession); len(got) != 0 {
		t.Fatalf("running after a fresh provider start = %v, want none", got)
	}
}
