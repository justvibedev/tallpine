package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// session get shows the measured baseline, the window and the trigger (B735).
func TestSessionGet_ShowsCompaction(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-2", "demo", "orchestrator", "idle", false)+
			`,"compaction":{"baseline":51000,"window":151000,"trigger":118000,"modelWindow":1000000,"roomTight":false}}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-2")
	if err != nil {
		t.Fatalf("session get failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "compaction: baseline 51k, window 151k, trigger 118k\n") {
		t.Fatalf("compaction not shown:\n%s", out)
	}
}

// Spawn prints the daemon's room warning and the compaction line.
func TestSpawn_ShowsCompactionAndRoomWarning(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","config":{}}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"session":{"id":"demo-3","status":"idle"},"promptBytes":0,"systemPromptBytes":0,`+
				`"warnings":["context baseline 150k leaves under 60k of room"],`+
				`"compaction":{"baseline":150000,"window":0,"trigger":0,"modelWindow":200000,"roomTight":true}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "codex", "--name", "worker")
	if err != nil {
		t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
	}
	for _, want := range []string{"warning: context baseline 150k leaves under 60k of room\n", "compaction: baseline 150k, window agent default, trigger agent default\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("spawn output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatCompaction(t *testing.T) {
	for _, tt := range []struct {
		view domain.AutoCompactView
		want string
	}{
		{domain.AutoCompactView{Baseline: 66_000, Window: 166_000, Trigger: 133_000}, "baseline 66k, window 166k, trigger 133k"},
		{domain.AutoCompactView{Window: 120_000, Trigger: 120_000}, "baseline not measured, window 120k, trigger 120k"},
		{domain.AutoCompactView{Baseline: 66_500}, "baseline 66.5k, window agent default, trigger agent default"},
	} {
		if got := formatCompaction(tt.view); got != tt.want {
			t.Errorf("formatCompaction(%+v) = %q, want %q", tt.view, got, tt.want)
		}
	}
}
