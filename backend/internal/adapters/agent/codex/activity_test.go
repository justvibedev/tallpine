package codex

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestDeriveActivityState(t *testing.T) {
	tests := []struct {
		name   string
		event  string
		want   domain.ActivityState
		wantOK bool
	}{
		{"user prompt -> active", "user-prompt-submit", domain.ActivityActive, true},
		{"permission request -> waiting_input", "permission-request", domain.ActivityWaitingInput, true},
		{"pre tool use -> active", "pre-tool-use", domain.ActivityActive, true},
		{"pre compact -> no signal", "pre-compact", "", false},
		{"post compact -> no signal", "post-compact", "", false},
		{"session end -> exited", "session-end", domain.ActivityExited, true},
		{"stop -> idle", "stop", domain.ActivityIdle, true},
		{"session start -> no signal", "session-start", "", false},
		{"unknown event -> no signal", "frobnicate", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DeriveActivityState(tt.event, []byte(`{}`))
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("DeriveActivityState(%q) = (%q, %v), want (%q, %v)",
					tt.event, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestDeriveActivityStateSessionEndReasons(t *testing.T) {
	for _, reason := range []string{"clear", "resume"} {
		if _, ok := DeriveActivityState("session-end", []byte(`{"reason":"`+reason+`"}`)); ok {
			t.Fatalf("session-end reason %q must not exit the session", reason)
		}
	}
	if got, ok := DeriveActivityState("session-end", []byte(`{"reason":"logout"}`)); !ok || got != domain.ActivityExited {
		t.Fatalf("logout = (%q, %v)", got, ok)
	}
}
