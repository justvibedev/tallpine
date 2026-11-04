package chat_test

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// A side chat's model is policed like the main chat's, and fails closed.
func TestSideChatSettingsRefuseDeniedModel(t *testing.T) {
	h := newSideWorkflow(t, "")
	side := h.side(t, "policy", false)
	ctx := context.Background()

	h.svc.SetProjects(fakeProjects{denylist: []string{"blocked-model"}})
	err := h.svc.UpdateSideSettings(ctx, testSession, side.ID, "Blocked-Model", "low")
	var denied *domain.ModelDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("denied side-chat model = %v, want ModelDeniedError", err)
	}
	if err := h.svc.UpdateSideSettings(ctx, testSession, side.ID, "fine-model", "low"); err != nil {
		t.Fatalf("allowed side-chat model: %v", err)
	}

	h.svc.SetProjects(fakeProjects{err: errors.New("db locked")})
	if err := h.svc.UpdateSideSettings(ctx, testSession, side.ID, "fine-model", "low"); !errors.Is(err, chatsvc.ErrModelPolicyUnavailable) {
		t.Fatalf("unreadable policy = %v, want ErrModelPolicyUnavailable (fail closed)", err)
	}
}
