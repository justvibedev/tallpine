package claudecode

import (
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// claudeAbortedTurnScreen reproduces the live pane of a Claude Code session
// whose turn died on an expired OAuth login: the turn's Stop hook never fired,
// the composer holds an unsent user draft, and the CLI sits idle at the
// prompt. Captured plain (tmux capture-pane without -e), which is exactly what
// the activity observer's GetOutput provides.
func claudeAbortedTurnScreen(draft string) string {
	rule := strings.Repeat("─", 48)
	return "⏺ Stopped watching Artifact: \"scm-observer.md\" (connection lost)\n" +
		"\n" +
		"⏺ Login expired · Please run /login\n" +
		"\n" +
		"✻ Worked for 0s\n" +
		"\n" +
		rule + "\n" +
		draft +
		rule + "\n" +
		"\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · PR #4090\n" +
		"  ⧉  scm-observer"
}

func TestDetectTerminalActivityWaitsForLoginAfterAbortedTurn(t *testing.T) {
	plugin := &Plugin{}
	tests := []struct {
		name   string
		output string
	}{
		{
			name: "login expired with staged draft",
			output: claudeAbortedTurnScreen("❯ so btw, the status isn't permanently stuck at \"Checking merge readiness\". It's stuck at that for\n" +
				"  most of the time but keeps showing the merge status once in a while.\n"),
		},
		{
			name:   "login expired with empty composer",
			output: claudeAbortedTurnScreen("❯\n"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, ok := plugin.DetectTerminalActivity(tt.output)
			if !ok || state != domain.ActivityWaitingInput {
				t.Fatalf("DetectTerminalActivity = (%q, %v), want (waiting_input, true)", state, ok)
			}
		})
	}
}

func TestDetectTerminalActivityFailsClosedOffIdle(t *testing.T) {
	plugin := &Plugin{}
	rule := strings.Repeat("─", 48)
	tests := []struct {
		name   string
		output string
	}{
		{
			name:   "active spinner row",
			output: "✶ Generating… (esc to interrupt · 2s)\n" + rule + "\n❯\n" + rule,
		},
		{
			name: "active with interrupt hint in footer",
			output: "✻ Computing… (24s · ↓ 114 tokens)\n" + rule + "\n❯\n" + rule +
				"\n⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt · ← for agents",
		},
		{
			name:   "permission dialog",
			output: "Do you want to proceed?\n❯ 1. Yes\n  2. No\nPress enter to confirm",
		},
		{
			name:   "no recognizable surface",
			output: "plain build output\nwithout any provider chrome\n",
		},
		{
			name:   "empty capture",
			output: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, ok := plugin.DetectTerminalActivity(tt.output)
			if ok {
				t.Fatalf("DetectTerminalActivity = (%q, true), want not authoritative", state)
			}
		})
	}
}

// The captured pane includes scrollback. A login-expired notice the user has
// since resolved, or the same words in tool output, must never read as
// waiting_input: tool hooks cannot clear that sticky state mid-turn, so a
// working session would show as waiting on the user until its turn ended.
func TestDetectTerminalActivityIgnoresStaleOrQuotedLoginExpiry(t *testing.T) {
	plugin := &Plugin{}
	rule := strings.Repeat("─", 48)
	staleLogin := "⏺ Login expired · Please run /login\n\n❯ /login\n  ⎿  Login successful\n\n"
	tests := []struct {
		name      string
		output    string
		wantState domain.ActivityState
		wantOK    bool
	}{
		{
			name: "busy after a resolved login expiry",
			output: staleLogin + "⏺ Running the build.\n\n⏺ Bash(npm run package)\n  ⎿  Running…\n\n" +
				"✶ Building… (esc to interrupt · 3m 2s)\n" + rule + "\n❯\n" + rule,
		},
		{
			name: "busy with the interrupt hint in the footer",
			output: staleLogin + "✻ Computing… (24s · ↓ 114 tokens)\n" + rule + "\n❯\n" + rule +
				"\n⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt",
		},
		{
			name:      "idle after a later response",
			output:    staleLogin + "⏺ Done: the tests pass.\n\n✻ Worked for 2m\n\n" + rule + "\n❯\n" + rule,
			wantState: domain.ActivityIdle, wantOK: true,
		},
		{
			name: "login text quoted in tool output",
			output: "⏺ Search(pattern: \"login expired\")\n  ⎿  terminal_activity.go: \"⏺ Login expired · Please run /login\"\n\n" +
				"⏺ Found the probe.\n\n" + rule + "\n❯\n" + rule,
			wantState: domain.ActivityIdle, wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, ok := plugin.DetectTerminalActivity(tt.output)
			if ok != tt.wantOK || state != tt.wantState {
				t.Fatalf("DetectTerminalActivity = (%q, %v), want (%q, %v)", state, ok, tt.wantState, tt.wantOK)
			}
		})
	}
}
