package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// A switch that cannot be committed must leave the previous account active:
// the device credential, the active account id and the reconciliation state do
// not move.
func TestFailedSwitchKeepsThePreviousAccountActive(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source func(t *testing.T, f *codexLaunchReadinessFixture) string
	}{
		{"credential source is unreadable", func(t *testing.T, _ *codexLaunchReadinessFixture) string {
			return filepath.Join(t.TempDir(), "missing-auth.json")
		}},
		{"credential belongs to another login", func(t *testing.T, f *codexLaunchReadinessFixture) string {
			path := filepath.Join(t.TempDir(), "auth.json")
			if err := os.WriteFile(path, testOAuthCredential("someone-else", "other-access"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCodexLaunchReadinessFixture(t)
			m := f.manager
			before, err := os.ReadFile(m.globalCredentialPath())
			if err != nil {
				t.Fatal(err)
			}
			activeBefore := m.activeAccountID()
			if activeBefore != f.active.Snapshot.ID {
				t.Fatalf("fixture active account = %q, want %q", activeBefore, f.active.Snapshot.ID)
			}

			err = m.activateFromCredentialLocked(context.Background(), f.other.Snapshot.ID, tc.source(t, f), nil)
			if !errors.Is(err, ports.ErrCodexAccountSwitchNotCommitted) {
				t.Fatalf("activation error = %v, want ErrCodexAccountSwitchNotCommitted", err)
			}
			if got := m.activeAccountID(); got != activeBefore {
				t.Fatalf("active account after failed switch = %q, want %q", got, activeBefore)
			}
			after, err := os.ReadFile(m.globalCredentialPath())
			if err != nil || string(after) != string(before) {
				t.Fatalf("device credential changed by a failed switch: err=%v", err)
			}
		})
	}
}
