package chat_test

import (
	"context"
	"log/slog"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

type emptyCatalog struct{}

func (emptyCatalog) Models(context.Context, string, string, bool) (ports.AgentModelCatalog, error) {
	return ports.AgentModelCatalog{}, nil
}

// A stopped or hibernated chat has no controller, yet its snapshot carries the
// same resolved capacity and threshold; without them the client would show the
// agent's reported compaction limit as the model window.
func TestSnapshotWithoutControllerCarriesTheResolvedCompactionView(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
			snapshot, err := st.LoadConversationSnapshot(ctx, conversationID)
			if err != nil {
				return chatsvc.ConversationRows{}, err
			}
			return chatsvc.ConversationRows{Conversation: snapshot.Conversation}, nil
		}),
		Drivers:            fakeRegistry{driver: fakeDriver{conv: newFakeConversation()}},
		Log:                slog.New(slog.DiscardHandler),
		NewID:              func() string { return "no-controller-view-id" },
		AutoCompactDefault: func(context.Context, domain.AgentHarness) string { return "40%" },
	})
	svc.SetModelCatalog(emptyCatalog{})
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		Model: "opus", WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.Stop(ctx, testSession); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	snapshot, err := svc.Snapshot(ctx, testSession)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	got := snapshot.Conversation.Settings
	// The fixture session is an orchestrator: its 120k default, not the 40%
	// Settings default (B398).
	if got.ContextCapacity != 1_000_000 || got.CompactThreshold != 120_000 {
		t.Fatalf("settings = capacity %d threshold %d (model %q), want 1000000 and 120000", got.ContextCapacity, got.CompactThreshold, got.Model)
	}
}
