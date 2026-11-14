package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// A session stored under a harness that was removed must still be readable: the
// read model carries the read-only explanation instead of failing the listing.
func TestToSessionMarksRemovedHarnessReadOnly(t *testing.T) {
	t.Parallel()

	svc := &Service{store: &fakeStore{}, clock: func() time.Time { return time.Unix(1, 0) }}
	for _, tc := range []struct {
		harness     domain.AgentHarness
		wantMessage bool
	}{
		{"aider", true},
		{"opencode-v2", true},
		{domain.HarnessClaudeCode, false},
		{domain.HarnessCodex, false},
		{domain.HarnessKiro, false},
		{"", false},
	} {
		session, err := svc.toSessionWithFacts(context.Background(), domain.SessionRecord{
			ID: "s", Harness: tc.harness, Mode: domain.SessionModeTUI,
		}, nil, nil, false, "", true, nil, nil, nil)
		if err != nil {
			t.Fatalf("%q: %v", tc.harness, err)
		}
		if got := session.UnsupportedHarnessMessage != ""; got != tc.wantMessage {
			t.Fatalf("%q: message %q, want message=%v", tc.harness, session.UnsupportedHarnessMessage, tc.wantMessage)
		}
		if tc.wantMessage && !strings.Contains(session.UnsupportedHarnessMessage, string(tc.harness)) {
			t.Fatalf("%q: message %q does not name the harness", tc.harness, session.UnsupportedHarnessMessage)
		}
	}
}
