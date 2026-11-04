package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSessionKill_SuccessWithProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "kill", "demo-1", "--project", "demo")
	if err != nil {
		t.Fatalf("session kill failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "session demo-1 killed") {
		t.Fatalf("unexpected kill output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1", "POST /api/v1/sessions/demo-1/kill"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

// TestSessionKill_PreservedWorkspaceNote: freed=false means the daemon
// terminated the session but kept the worktree (uncommitted changes are never
// force-deleted) — the CLI must say so instead of implying a full teardown.
func TestSessionKill_PreservedWorkspaceNote(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/kill" {
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","freed":false}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "kill", "demo-1")
	if err != nil {
		t.Fatalf("session kill failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "session demo-1 killed (workspace preserved)") {
		t.Fatalf("unexpected kill output:\n%s", out)
	}
}
func TestSessionKill_PendingCleanupNote(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/kill" {
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","freed":false,"cleanupPending":true}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "kill", "demo-1")
	if err != nil {
		t.Fatalf("session kill failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "session demo-1 killed (workspace cleanup pending)") {
		t.Fatalf("unexpected kill output:\n%s", out)
	}
}

func TestSessionRestore_SuccessWithProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	log := &sessionRequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.append(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "terminated", true)+`}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/restore":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","session":`+sessionJSON("demo-1", "demo", "worker", "idle", false)+`}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restore", "demo-1", "-p", "demo")
	if err != nil {
		t.Fatalf("session restore failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "session demo-1 restored") || !strings.Contains(out, "project: demo") {
		t.Fatalf("unexpected restore output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1", "POST /api/v1/sessions/demo-1/restore"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionRestore_ExitedOrchestratorResumesAgentInPlace(t *testing.T) {
	cfg := setConfigEnv(t)
	log := &sessionRequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.append(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-orch":
			_, _ = io.WriteString(w, `{"session":{"id":"demo-orch","projectId":"demo","kind":"orchestrator","harness":"codex","activity":{"state":"exited","lastActivityAt":"2026-06-02T12:00:00Z"},"isTerminated":false,"status":"exited"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-orch/resume-agent":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-orch","resumeMode":"native","session":{"id":"demo-orch","projectId":"demo","kind":"orchestrator","activity":{"state":"idle"},"isTerminated":false,"status":"idle"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restore", "demo-orch", "--project", "demo")
	if err != nil {
		t.Fatalf("session restore failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "agent resumed for session demo-orch") || !strings.Contains(out, "mode: native") {
		t.Fatalf("unexpected restore output:\n%s", out)
	}
	want := []string{
		"GET /api/v1/sessions/demo-orch",
		"POST /api/v1/sessions/demo-orch/resume-agent",
	}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionExitAndResumeAgentUseProcessOnlyRoutes(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	out, errOut, err := executeCLI(t, deps, "session", "exit-agent", "demo-1", "--project", "demo")
	if err != nil {
		t.Fatalf("session exit-agent failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "agent exited for session demo-1") {
		t.Fatalf("unexpected exit-agent output:\n%s", out)
	}

	out, errOut, err = executeCLI(t, deps, "session", "resume-agent", "demo-1")
	if err != nil {
		t.Fatalf("session resume-agent failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "agent resumed for session demo-1") || !strings.Contains(out, "mode: native") {
		t.Fatalf("unexpected resume-agent output:\n%s", out)
	}

	want := []string{
		"GET /api/v1/sessions/demo-1",
		"POST /api/v1/sessions/demo-1/exit-agent",
		"POST /api/v1/sessions/demo-1/resume-agent",
	}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionCleanup_YesSkipsPrompt(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader("no\n"),
		ProcessAlive: func(int) bool { return true },
	}, "session", "cleanup", "--project", "demo", "--yes")
	if err != nil {
		t.Fatalf("session cleanup failed: %v\nstderr=%s", err, errOut)
	}
	if strings.Contains(out, "Type yes to confirm") {
		t.Fatalf("--yes should skip confirmation prompt:\n%s", out)
	}
	for _, want := range []string{"Checking for completed sessions", "Would clean demo-old", "Would clean demo-orch", "Cleaned: demo-old", "Cleaned: demo-orch", "Cleanup complete. 2 sessions cleaned."} {
		if !strings.Contains(out, want) {
			t.Fatalf("cleanup output missing %q:\n%s", want, out)
		}
	}
	want := []string{
		"GET /api/v1/sessions?active=false&project=demo",
		"POST /api/v1/sessions/cleanup?project=demo",
	}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

// TestSessionCleanup_ReportsAlreadyGoneWorkspacesSeparately: a workspace whose
// directory was already missing tears down without error but reclaims nothing.
// Folding it into the cleaned count is what lets a batch run report hundreds of
// sessions cleaned while disk usage does not move, so the summary has to keep
// the two apart.
func TestSessionCleanup_ReportsAlreadyGoneWorkspacesSeparately(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"sessions":[`+
				sessionJSON("demo-old", "demo", "worker", "terminated", true)+`,`+
				sessionJSON("demo-orch", "demo", "orchestrator", "terminated", true)+`]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/cleanup":
			_, _ = io.WriteString(w, `{"ok":true,"cleaned":["demo-old"],"alreadyGone":["demo-orch"],"skipped":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "cleanup", "--project", "demo", "--yes")
	if err != nil {
		t.Fatalf("session cleanup failed: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{
		"Cleaned: demo-old",
		"Already gone: demo-orch (workspace was missing; nothing reclaimed)",
		"Cleanup complete. 1 session cleaned, 1 already gone.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("cleanup output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Cleaned: demo-orch") {
		t.Fatalf("a workspace that was already gone must not be reported as cleaned:\n%s", out)
	}
}

// TestSessionCleanup_DryRunListsWithoutPromptingOrDeleting: --dry-run must
// preview the candidate sessions and stop there — no confirmation prompt and,
// critically, no POST to the cleanup endpoint, so nothing is removed.
func TestSessionCleanup_DryRunListsWithoutPromptingOrDeleting(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		// A prompt would block on empty input and fail; --dry-run must not read it.
		In:           strings.NewReader(""),
		ProcessAlive: func(int) bool { return true },
	}, "session", "cleanup", "--project", "demo", "--dry-run")
	if err != nil {
		t.Fatalf("session cleanup --dry-run failed: %v\nstderr=%s", err, errOut)
	}
	if strings.Contains(out, "Type yes to confirm") {
		t.Fatalf("--dry-run must not prompt for confirmation:\n%s", out)
	}
	for _, want := range []string{"Would clean demo-old", "Would clean demo-orch", "(dry-run: no sessions were removed)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Cleaned:") || strings.Contains(out, "sessions cleaned") {
		t.Fatalf("--dry-run must not report removals:\n%s", out)
	}
	// Only the candidate listing GET is allowed; no cleanup POST may be sent.
	want := []string{"GET /api/v1/sessions?active=false&project=demo"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

// TestSessionCleanup_ReportsSkippedWorkspaces: a session whose workspace was
// preserved must be listed with its reason and counted in the summary —
// previously the CLI printed "Would clean N" then "0 sessions cleaned" with no
// explanation, leaking workspaces invisibly.
func TestSessionCleanup_ReportsSkippedWorkspaces(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"sessions":[`+
				sessionJSON("demo-old", "demo", "worker", "terminated", true)+`,`+
				sessionJSON("demo-orch", "demo", "orchestrator", "terminated", true)+`]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/cleanup":
			_, _ = io.WriteString(w, `{"ok":true,"cleaned":["demo-old"],"skipped":[{"sessionId":"demo-orch","reason":"workspace has uncommitted changes"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "cleanup", "--project", "demo", "--yes")
	if err != nil {
		t.Fatalf("session cleanup failed: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{
		"Cleaned: demo-old",
		"Skipped: demo-orch (workspace has uncommitted changes)",
		"Cleanup complete. 1 session cleaned, 1 skipped.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("cleanup output missing %q:\n%s", want, out)
		}
	}
}

func TestSessionCleanup_PromptFailsWithoutInput(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(""),
		ProcessAlive: func(int) bool { return true },
	}, "session", "cleanup", "--project", "demo")
	if err == nil {
		t.Fatal("expected cleanup prompt without input to fail")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !strings.Contains(out, "Type yes to confirm") {
		t.Fatalf("output missing confirmation prompt:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions?active=false&project=demo"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}
