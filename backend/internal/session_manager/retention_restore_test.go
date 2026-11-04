package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// purgedStore is the fake store plus the retention purge check.
type purgedStore struct {
	*fakeStore
	purged map[domain.SessionID]bool
}

func (p purgedStore) SessionRetentionPurged(_ context.Context, id domain.SessionID) (bool, error) {
	return p.purged[id], nil
}

// A session whose history the retention policy purged is refused before any
// relaunch; an unpurged one restores as before.
func TestRestoreRefusesSessionPurgedByRetention(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	m.store = purgedStore{fakeStore: st, purged: map[domain.SessionID]bool{"gone-1": true}}
	seedTerminal(st, "gone-1", domain.SessionMetadata{WorkspacePath: "/ws/gone-1", Branch: "b", AgentSessionID: "agent-x"})
	seedTerminal(st, "kept-1", domain.SessionMetadata{WorkspacePath: "/ws/kept-1", Branch: "c", AgentSessionID: "agent-y"})

	if _, err := m.RestoreWithMode(ctx, "gone-1"); !errors.Is(err, ErrNotRestorable) {
		t.Fatalf("restore of a purged session = %v, want ErrNotRestorable", err)
	}
	if rt.created != 0 {
		t.Fatal("a purged session was relaunched")
	}
	if _, err := m.RestoreWithMode(ctx, "kept-1"); err != nil {
		t.Fatalf("restore of an unpurged session: %v", err)
	}
}
