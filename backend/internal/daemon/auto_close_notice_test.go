package daemon

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// B560: the closed notice is skipped only when the session that landed the PR
// is the orchestrator the notice would go to; a human, an unscoped caller or
// another agent landing it still notifies the owning orchestrator.
func TestAutoCloseNotifyClosed_SkipsOnlyTheRequestingOrchestrator(t *testing.T) {
	for _, tc := range []struct {
		requester domain.SessionID
		wantSent  int
	}{{"orch-1", 0}, {"", 1}, {"orch-2", 1}} {
		var sent []domain.SessionID
		h := autoCloseHooks{
			store:        sqlitetest.MustOpen(t),
			orchestrator: func(context.Context, domain.ProjectID, string) (domain.SessionID, bool) { return "orch-1", true },
			send:         func(_ context.Context, to domain.SessionID, _ string) error { sent = append(sent, to); return nil },
		}
		h.NotifyClosed(context.Background(), domain.SessionRecord{ID: "w-1", ProjectID: "p1"}, tc.requester, "")
		if len(sent) != tc.wantSent {
			t.Fatalf("requester %q: sent %v, want %d notices", tc.requester, sent, tc.wantSent)
		}
	}
}
