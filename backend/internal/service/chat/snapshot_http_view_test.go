package chat_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// The conversation endpoint serves SnapshotPage, not Snapshot. A resolved view
// added only to Snapshot left every installed chat without contextCapacity,
// compactThreshold and modelLabel. This drives the real router, controller and
// chat service (wired the way the daemon wires them) and reads the JSON.
func TestConversationEndpointCarriesTheResolvedCompactionView(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, model, def string
		capacity, thresh float64
	}{
		{"opus with a 40% default", "opus", "40%", 1_000_000, 400_000},
		{"sonnet at 1m", "sonnet", "1m", 1_000_000, 1_000_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := openStore(t)
			ctx := context.Background()
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st,
				Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, id string) (chatsvc.ConversationRows, error) {
					rows, err := st.LoadConversationSnapshot(ctx, id)
					if err != nil {
						return chatsvc.ConversationRows{}, err
					}
					return chatsvc.ConversationRows{Conversation: rows.Conversation}, nil
				}),
				PageReader: chatsvc.SnapshotPageReaderFunc(func(ctx context.Context, id string, before, limit int64) (chatsvc.ConversationRows, error) {
					rows, err := st.LoadConversationSnapshotPage(ctx, id, before, limit)
					if err != nil {
						return chatsvc.ConversationRows{}, err
					}
					return chatsvc.ConversationRows{Conversation: rows.Conversation}, nil
				}),
				Drivers:            fakeRegistry{driver: fakeDriver{conv: newFakeConversation()}},
				Log:                slog.New(slog.DiscardHandler),
				NewID:              func() string { return "http-view-id" },
				AutoCompactDefault: func(context.Context, domain.AgentHarness) string { return tc.def },
			})
			svc.SetModelCatalog(emptyCatalog{})
			if _, err := svc.Start(ctx, chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
				Model: tc.model, WorkspacePath: t.TempDir(),
			}); err != nil {
				t.Fatalf("Start: %v", err)
			}
			handler := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
				httpd.APIDeps{Sessions: controllers.SessionService(nil), Conversations: svc}, httpd.ControlDeps{})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+string(testSession)+"/conversation", nil)
			req.Host = "127.0.0.1:3101"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			var body struct {
				Settings map[string]any `json:"settings"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Settings["contextCapacity"] != tc.capacity || body.Settings["compactThreshold"] != tc.thresh || body.Settings["modelLabel"] == "" {
				t.Fatalf("settings = %v, want capacity %v threshold %v and a model label", body.Settings, tc.capacity, tc.thresh)
			}
		})
	}
}
