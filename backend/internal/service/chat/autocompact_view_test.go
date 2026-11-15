package chat_test

import (
	"context"
	"log/slog"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/chat"
)

// A started chat reads its baseline from the real store at launch (with no
// measured sessions, the 70k default) and reports it with its window; a
// session without a running controller has no chat readout (B735).
func TestAutoCompactViewReportsTheLaunchBaselineFromTheStore(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	svc := chat.New(chat.Options{
		Store: st, Sessions: st, AutoCompactBaselines: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation()}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "compaction-view-id" },
	})
	if _, ok := svc.AutoCompactView(ctx, testSession); ok {
		t.Fatal("a session without a running controller reported a chat readout")
	}
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	if _, err := svc.Start(ctx, chat.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	view, ok := svc.AutoCompactView(ctx, testSession)
	if !ok || view.Baseline != domain.AutoCompactBaselineDefault {
		t.Fatalf("view = %+v ok=%v, want the %d default baseline", view, ok, domain.AutoCompactBaselineDefault)
	}
}
