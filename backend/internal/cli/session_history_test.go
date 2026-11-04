package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/activitydispatch"
)

func TestSessionHistory_PrintsTurnsForTheCurrentSession(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("TALLPINE_SESSION_ID", "demo-1")
	var requested string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.Method + " " + r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"turns":[
			{"userRequest":"second ask","agentResponse":"second answer","at":"2026-10-11T09:02:00Z","harness":"codex"},
			{"userRequest":"first ask","agentResponse":"","at":"2026-10-11T09:01:00Z"}],"total":5,"offset":2}`))
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "history", "--offset", "2", "--limit", "2")
	if err != nil {
		t.Fatalf("session history failed: %v\nstderr=%s", err, errOut)
	}
	if requested != "GET /api/v1/sessions/demo-1/history?limit=2&offset=2" {
		t.Fatalf("request = %q", requested)
	}
	for _, want := range []string{
		"## Turn 3 of 5", "codex", "second ask", "second answer",
		"## Turn 2 of 5", "first ask", "(no final response recorded)",
		"1 older turns remain. Run `tallpine session history demo-1 --offset 4 --limit 2`",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestSessionHistory_RequiresASessionOutsideTallpine(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("TALLPINE_SESSION_ID", "")
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "history")
	if err == nil || !strings.Contains(err.Error(), "session id is required") {
		t.Fatalf("err = %v, want a usage error naming the missing session", err)
	}
}

func TestHooks_StopFinalMessageFeedsTurnHistoryForEveryAgent(t *testing.T) {
	const payload = `{"turn_id":"turn-1","last_assistant_message":"all done"}`
	for _, tt := range []struct {
		agent, payload               string
		wantFinal, wantReplayAnswers bool
	}{
		// Codex reports its final message without making it a replay checkpoint.
		{"codex", payload, true, false},
		{"claude-code", payload, true, true},
		{"codex", `{"agent_id":"child","last_assistant_message":"all done"}`, false, false},
		{"kiro", `{"session_id":"ses-1","model":"m","last_assistant_message":"all done"}`, true, false},
	} {
		t.Run(tt.agent, func(t *testing.T) {
			t.Setenv("TALLPINE_SESSION_ID", "tp-7")
			cfg := setConfigEnv(t)
			srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)

			_, _, err := executeCLI(t, Deps{
				In:           strings.NewReader(tt.payload),
				ProcessAlive: func(int) bool { return true },
			}, "hooks", tt.agent, "stop")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := strings.Contains(capture.body, `"finalAssistantMessage":"all done"`); got != tt.wantFinal {
				t.Fatalf("final message in turn history = %v, want %v: %s", got, tt.wantFinal, capture.body)
			}
			if got := strings.Contains(capture.body, `"latestAssistantUpdate":"all done"`); got != tt.wantReplayAnswers {
				t.Fatalf("final message as replay checkpoint = %v, want %v: %s", got, tt.wantReplayAnswers, capture.body)
			}
		})
	}
	if other := activitydispatch.Facts("codex", "post-tool-use", []byte(payload)).Conversation; other.FinalAssistantMessage != "" {
		t.Fatalf("a non-stop event reported a final message: %+v", other)
	}
}

func TestHooks_KiroPromptsReachTurnHistoryOnly(t *testing.T) {
	for _, agent := range []string{"kiro"} {
		t.Run(agent, func(t *testing.T) {
			t.Setenv("TALLPINE_SESSION_ID", "tp-7")
			cfg := setConfigEnv(t)
			srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)

			_, _, err := executeCLI(t, Deps{
				In:           strings.NewReader(`{"session_id":"ses-1","prompt":"fix this"}`),
				ProcessAlive: func(int) bool { return true },
			}, "hooks", agent, "user-prompt-submit")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(capture.body, `"turnUserRequest":"fix this"`) {
				t.Fatalf("the prompt did not reach the turn history field: %s", capture.body)
			}
			// The daemon drops a signal whose agent is not the one the session
			// runs, so a previous agent's leftover hooks cannot speak for it.
			if !strings.Contains(capture.body, `"harness":"`+agent+`"`) {
				t.Fatalf("the hook did not name its agent: %s", capture.body)
			}
			// The replay-checkpoint field stays empty for these agents.
			if strings.Contains(capture.body, `"latestUserPrompt"`) {
				t.Fatalf("the prompt leaked into the replay checkpoint: %s", capture.body)
			}
		})
	}
}
