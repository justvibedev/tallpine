package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// kiroModeConversation also advertises an agent selector under a non-"mode" id
// whose category is "mode", as a later kiro-cli might.
type kiroModeConversation struct{ *claudeDefaultsConversation }

func (c *kiroModeConversation) ListConfigOptions(ctx context.Context) ([]ports.ChatConfigOption, error) {
	options, err := c.claudeDefaultsConversation.ListConfigOptions(ctx)
	return append(options, ports.ChatConfigOption{ID: "agent", Category: "mode", Type: ports.ChatConfigOptionSelect}), err
}

func (c *kiroModeConversation) SetConfigOption(ctx context.Context, id string, value ports.ChatConfigOptionValue) ([]ports.ChatConfigOption, error) {
	if _, err := c.claudeDefaultsConversation.SetConfigOption(ctx, id, value); err != nil {
		return nil, err
	}
	return c.ListConfigOptions(ctx)
}

// Kiro's agent selector is fixed at launch (the profile carries the permission
// rules), so a set of its `mode` option is refused before it reaches the
// conversation, while other options still pass through.
func TestKiroModeConfigOptionIsRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openStore(t)
	created, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessKiro,
		Mode: domain.SessionModeChat, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created.ID
	conv := &kiroModeConversation{claudeDefaultsConversation: &claudeDefaultsConversation{fakeConversation: newFakeConversation(), model: "default", effort: "default"}}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Reader: fullSnapshotReader(st), Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler), NewID: uuid.NewString,
	})
	if _, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: id, ProjectID: testProject, Harness: domain.HarnessKiro, WorkspacePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx, id) })

	for _, configID := range []string{"mode", "agent"} {
		_, err = svc.SetConfigOption(ctx, id, configID, ports.ChatConfigOptionValue{Select: "other-agent"})
		if !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
			t.Fatalf("set %s error = %v, want ErrChatConfigOptionInvalid", configID, err)
		}
	}
	if len(conv.calls) != 0 {
		t.Fatalf("refused set reached the conversation: %v", conv.calls)
	}
	if _, err := svc.SetConfigOption(ctx, id, "model", ports.ChatConfigOptionValue{Select: "opus-4.7"}); err != nil {
		t.Fatal(err)
	}
}
