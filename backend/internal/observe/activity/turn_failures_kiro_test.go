package activity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/kiro"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/lifecycle"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// kiro-cli 3.0.0 fires no Stop for a turn whose model request errored; the
// observer must idle the session from the session log's turn_end record.
func TestTurnFailureObserverIdlesAFailedKiroTurn(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	promptAt := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Millisecond)
	createdAt := promptAt.Add(-time.Minute)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "kirofail", Path: t.TempDir(), RegisteredAt: createdAt}); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "kirofail", Kind: domain.KindWorker, Harness: domain.HarnessKiro,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: createdAt}, CreatedAt: createdAt, UpdatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	session.Metadata.RuntimeHandleID, session.Metadata.RuntimeLaunchID = "handle-1", "launch-1"
	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "sess_x", "messages.jsonl")
	if err := os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, []byte(`{"timestamp":"`+promptAt.Format("2006-01-02T15:04:05.000Z")+`","payload":{"type":"user","content":"hi"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := lifecycle.New(store, nil)
	observer := NewTurnFailureObserver(store, manager, fakeAgents{domain.HarnessKiro: kiro.New()}, TurnFailureConfig{Logger: testLogger()})
	if err := manager.ApplyActivitySignal(ctx, session.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, Timestamp: promptAt, Event: "user-prompt-submit",
		LaunchID: "launch-1", AgentSessionID: "sess_x", TranscriptPath: log,
	}); err != nil {
		t.Fatal(err)
	}
	state := func() domain.ActivityState {
		if err := observer.Poll(ctx); err != nil {
			t.Fatal(err)
		}
		got, _, _ := store.GetSession(ctx, session.ID)
		return got.Activity.State
	}
	if got := state(); got != domain.ActivityActive {
		t.Fatalf("running turn: state = %q, want active", got)
	}
	failedAt := promptAt.Add(2 * time.Second)
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"timestamp":"` + failedAt.Format("2006-01-02T15:04:05.000Z") + `","payload":{"type":"turn_end","stopReason":"error"}}` + "\n")
	_ = f.Close()
	if got := state(); got != domain.ActivityIdle {
		t.Fatalf("failed turn: state = %q, want idle", got)
	}
}
