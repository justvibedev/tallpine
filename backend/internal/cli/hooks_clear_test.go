package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// clearNoteServer answers the activity report and serves note as the
// session's cleared-context note; reads counts the note reads.
func clearNoteServer(t *testing.T, note string) (srv *httptest.Server, reads func() int) {
	t.Helper()
	srv, log := slowClearNoteServer(t, note, 0)
	return srv, func() int { return strings.Count(strings.Join(log(), ","), "note") }
}

// slowClearNoteServer is clearNoteServer whose note takes delay to build. It
// records the order the hook's requests arrived in.
func slowClearNoteServer(t *testing.T, note string, delay time.Duration) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var log []string
	record := func(what string) {
		mu.Lock()
		defer mu.Unlock()
		log = append(log, what)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/qa-1/cleared-context-note":
			record("note")
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
			_ = json.NewEncoder(w).Encode(apitypes.ClearedContextNoteResponse{Note: note})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/activity"):
			_, _ = io.Copy(io.Discard, r.Body)
			record("activity")
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), log...)
	}
}

// A /clear typed in a Terminal UI Claude session drops the spawn brief and
// everything else Tallpine sent as messages. The SessionStart hook Claude runs
// for the clear hands the fresh context the cleared-context note.
func TestClaudeClearHookAddsTheClearedContextNote(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "qa-1")
	cfg := setConfigEnv(t)
	const note = "<ao-handoff-request>\nBRIEF-RULE: publishing is authorized\n</ao-handoff-request>"
	srv, reads := clearNoteServer(t, note)
	writeRunFileFor(t, cfg, srv)

	stdout, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"native-2","source":"clear"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "session-start")
	if err != nil {
		t.Fatal(err)
	}
	var output sessionStartHookOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("hook output %q: %v", stdout, err)
	}
	if reads() != 1 || output.HookSpecificOutput.HookEventName != "SessionStart" || output.HookSpecificOutput.AdditionalContext != note {
		t.Fatalf("reads=%d output=%+v, want the note as SessionStart context", reads(), output)
	}
}

// Only a clear re-briefs: a startup or resume keeps its context, and other
// agents never ask.
func TestSessionStartHookRebriefsOnlyAClaudeClear(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "qa-1")
	cfg := setConfigEnv(t)
	srv, reads := clearNoteServer(t, "note")
	writeRunFileFor(t, cfg, srv)
	for _, tc := range []struct{ agent, source string }{
		{"claude-code", "startup"}, {"claude-code", "resume"}, {"claude-code", "compact"}, {"codex", "clear"},
	} {
		stdout, _, err := executeCLI(t, Deps{
			In:           strings.NewReader(`{"session_id":"native-2","source":"` + tc.source + `"}`),
			ProcessAlive: func(int) bool { return true },
		}, "hooks", tc.agent, "session-start")
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(stdout) != "" || reads() != 0 {
			t.Fatalf("%s %s: stdout=%q reads=%d, want nothing", tc.agent, tc.source, stdout, reads())
		}
	}
}

// Claude Code shows hook text past its cap only as a preview of a saved file,
// so a long note asks the agent to read that file.
func TestLongClearedContextNoteAsksToReadTheSavedFile(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "qa-1")
	cfg := setConfigEnv(t)
	note := strings.Repeat("n", claudeHookContextMaxChars+1)
	srv, _ := clearNoteServer(t, note)
	writeRunFileFor(t, cfg, srv)
	stdout, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"native-2","source":"clear"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "session-start")
	if err != nil {
		t.Fatal(err)
	}
	var output sessionStartHookOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("hook output %q: %v", stdout, err)
	}
	if got := output.HookSpecificOutput.AdditionalContext; got != clearedNoteSpillLine+"\n\n"+note {
		t.Fatalf("long note context starts %q, want the read-the-file line first", got[:min(len(got), 120)])
	}
}

// The SessionStart report of a clear carries the new native session id, so a
// note that is slow to build must not cost it: the report goes first, and the
// note read has its own budget.
func TestSlowClearedContextNoteDoesNotCostTheActivityReport(t *testing.T) {
	prevReport, prevNote := hookReportBudget, clearedNoteBudget
	hookReportBudget, clearedNoteBudget = 200*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { hookReportBudget, clearedNoteBudget = prevReport, prevNote })
	t.Setenv("TALLPINE_SESSION_ID", "qa-1")
	cfg := setConfigEnv(t)
	srv, log := slowClearNoteServer(t, "note", time.Second)
	writeRunFileFor(t, cfg, srv)

	stdout, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"native-2","source":"clear"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "session-start")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(log(), ","); got != "activity,note" {
		t.Fatalf("requests = %q, want the activity report before the note read", got)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("a timed-out note still printed context: %q", stdout)
	}
}
