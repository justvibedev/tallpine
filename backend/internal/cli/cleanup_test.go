package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

const cleanupDryReport = `{"id":1,"startedAt":"2026-10-26T12:00:00Z","finishedAt":"2026-10-26T12:00:01Z","trigger":"manual","pass":"full","dryRun":true,"freedBytes":0,
"categories":[{"category":"worktrees","freedBytes":0,"wouldFreeBytes":2048,"pendingBytes":0,"freed":0,"wouldFree":1,"pending":0,"kept":1,"failed":0,
"items":[{"sessionId":"mer-1","what":"worktree","bytes":2048,"status":"would_free"},{"sessionId":"mer-2","what":"worktree","bytes":0,"status":"kept","reason":"uncommitted changes"}]},
{"category":"artifacts","freedBytes":0,"wouldFreeBytes":0,"pendingBytes":4096,"freed":0,"wouldFree":0,"pending":1,"kept":0,"failed":0,"items":[]}]}`

const cleanupRealReport = `{"id":2,"startedAt":"2026-10-26T12:01:00Z","finishedAt":"2026-10-26T12:01:01Z","trigger":"manual","pass":"full","dryRun":false,"freedBytes":2048,
"categories":[{"category":"worktrees","freedBytes":2048,"wouldFreeBytes":0,"pendingBytes":0,"freed":1,"wouldFree":0,"pending":0,"kept":1,"failed":0,"items":[]}]}`

type cleanupServer struct {
	mu      sync.Mutex
	runs    []map[string]any
	patches []map[string]any
	busy    bool
}

func (s *cleanupServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cleanup/runs":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.mu.Lock()
			s.runs = append(s.runs, body)
			busy := s.busy
			s.mu.Unlock()
			if busy {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"error":"conflict","code":"CLEANUP_IN_PROGRESS","message":"a cleanup run is already in progress"}`)
				return
			}
			if body["dryRun"] == true {
				_, _ = io.WriteString(w, cleanupDryReport)
				return
			}
			_, _ = io.WriteString(w, cleanupRealReport)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/cleanup/policy":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.mu.Lock()
			s.patches = append(s.patches, body)
			s.mu.Unlock()
			_, _ = io.WriteString(w, `{"automatic":true,"worktrees":{"enabled":true,"retentionDays":0},"artifacts":{"enabled":true,"retentionDays":14,"autoPrune":true,"deliverableAction":"archive","autoDeliverables":false}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cleanup":
			_, _ = io.WriteString(w, `{"policy":{"automatic":true,"worktrees":{"enabled":true,"retentionDays":1},"artifacts":{"enabled":true,"retentionDays":30,"autoPrune":true,"deliverableAction":"archive","autoDeliverables":false}},"running":false,"lastRun":`+cleanupRealReport+`,"nextLightRunAt":"2026-10-26T13:00:00Z","nextFullRunAt":"2026-10-27T12:00:00Z"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cleanup/runs":
			_, _ = io.WriteString(w, `{"runs":[`+cleanupRealReport+`]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *cleanupServer) runRequests() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.runs...)
}

func TestCleanup_DryRunNeverStartsARealRun(t *testing.T) {
	cfg := setConfigEnv(t)
	fake := &cleanupServer{}
	writeRunFileFor(t, cfg, fake.start(t))
	out, errOut, err := executeCLI(t, Deps{In: strings.NewReader(""), ProcessAlive: func(int) bool { return true }}, "cleanup", "--dry-run")
	if err != nil {
		t.Fatalf("cleanup --dry-run: %v\n%s", err, errOut)
	}
	runs := fake.runRequests()
	if len(runs) != 1 || runs[0]["dryRun"] != true {
		t.Fatalf("run requests = %v, want one dry run", runs)
	}
	for _, want := range []string{"would free 2.0 KiB", "4.0 KiB awaiting confirmation (4.0 KiB of artifacts: --confirm-artifacts)", "mer-2 worktree (uncommitted changes)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Type yes") {
		t.Fatalf("--dry-run prompted:\n%s", out)
	}
}

func TestCleanup_PromptDeclinedRemovesNothing(t *testing.T) {
	cfg := setConfigEnv(t)
	fake := &cleanupServer{}
	writeRunFileFor(t, cfg, fake.start(t))
	out, _, err := executeCLI(t, Deps{In: strings.NewReader("no\n"), ProcessAlive: func(int) bool { return true }}, "cleanup")
	if err != nil {
		t.Fatal(err)
	}
	if runs := fake.runRequests(); len(runs) != 1 || runs[0]["dryRun"] != true {
		t.Fatalf("run requests = %v, want only the preview", runs)
	}
	if !strings.Contains(out, "Remove 2.0 KiB now? Type yes to confirm") || !strings.Contains(out, "aborted") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestCleanup_ConfirmedRunsOnce(t *testing.T) {
	cfg := setConfigEnv(t)
	fake := &cleanupServer{}
	writeRunFileFor(t, cfg, fake.start(t))
	out, _, err := executeCLI(t, Deps{In: strings.NewReader("yes\n"), ProcessAlive: func(int) bool { return true }}, "cleanup", "--confirm-artifacts")
	if err != nil {
		t.Fatal(err)
	}
	runs := fake.runRequests()
	if len(runs) != 2 || runs[1]["dryRun"] != nil || runs[1]["confirmArtifacts"] != true || runs[1]["pass"] != "full" {
		t.Fatalf("run requests = %v, want preview then one confirmed full run", runs)
	}
	if !strings.Contains(out, "freed 2.0 KiB") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestCleanup_YesSkipsThePreview(t *testing.T) {
	cfg := setConfigEnv(t)
	fake := &cleanupServer{}
	writeRunFileFor(t, cfg, fake.start(t))
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cleanup", "--yes", "--light", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if runs := fake.runRequests(); len(runs) != 1 || runs[0]["pass"] != "light" || runs[0]["dryRun"] != nil {
		t.Fatalf("run requests = %v, want one light run", runs)
	}
	var report apitypes.CleanupRunReport
	if err := json.Unmarshal([]byte(out), &report); err != nil || report.FreedBytes != 2048 {
		t.Fatalf("json output = %q (%v)", out, err)
	}
}

func TestCleanup_JSONNeedsYesOrDryRun(t *testing.T) {
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cleanup", "--json")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err = %v (exit %d), want a usage error", err, ExitCode(err))
	}
}

func TestCleanup_BusySurfacesTheDaemonError(t *testing.T) {
	cfg := setConfigEnv(t)
	fake := &cleanupServer{busy: true}
	writeRunFileFor(t, cfg, fake.start(t))
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cleanup", "--yes")
	if err == nil || !strings.Contains(err.Error(), "already in progress") || ExitCode(err) != 1 {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
}

func TestCleanupPolicy_SetBuildsANestedPatch(t *testing.T) {
	cfg := setConfigEnv(t)
	fake := &cleanupServer{}
	writeRunFileFor(t, cfg, fake.start(t))
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cleanup", "policy",
		"--set", "artifacts.retentionDays=14", "--set", "worktrees.retentionDays=0", "--set", "automatic=true", "--set", "artifacts.deliverableAction=archive")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.patches) != 1 {
		t.Fatalf("patches = %v", fake.patches)
	}
	got, _ := json.Marshal(fake.patches[0])
	want := `{"artifacts":{"deliverableAction":"archive","retentionDays":14},"automatic":true,"worktrees":{"retentionDays":0}}`
	if string(got) != want {
		t.Fatalf("patch = %s, want %s", got, want)
	}
	if !strings.Contains(out, "after 14 day(s)") {
		t.Fatalf("output:\n%s", out)
	}
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cleanup", "policy", "--set", "novalue"); ExitCode(err) != 2 {
		t.Fatalf("malformed --set: err=%v exit=%d", err, ExitCode(err))
	}
}

func TestCleanupStatusAndHistory(t *testing.T) {
	cfg := setConfigEnv(t)
	fake := &cleanupServer{}
	writeRunFileFor(t, cfg, fake.start(t))
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cleanup", "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Automatic cleanup: on", "gitignored files in them (such as .env) go too", "Next light pass: 2026-10-26T13:00:00Z", "freed 2.0 KiB"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
	out, _, err = executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cleanup", "history", "--limit", "5")
	if err != nil || !strings.Contains(out, "freed 2.0 KiB") {
		t.Fatalf("history: %v\n%s", err, out)
	}
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cleanup", "history", "--limit", "0"); ExitCode(err) != 2 {
		t.Fatalf("--limit 0: exit %d", ExitCode(err))
	}
}
