package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type sessionRequestLog struct {
	mu       sync.Mutex
	requests []string
}

func requestLogEntry(r *http.Request) string {
	entry := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		entry += "?" + r.URL.RawQuery
	}
	return entry
}

func appendPrimaryRequest(dst *[]string, r *http.Request) {
	entry := requestLogEntry(r)
	*dst = append(*dst, entry)
}

func (l *sessionRequestLog) append(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	appendPrimaryRequest(&l.requests, r)
}

func (l *sessionRequestLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.requests...)
}

func sessionCommandServer(t *testing.T) (*httptest.Server, *sessionRequestLog) {
	t.Helper()
	log := &sessionRequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.append(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			active := r.URL.Query().Get("active")
			switch active {
			case "false":
				_, _ = io.WriteString(w, `{"sessions":[`+
					sessionJSON("demo-old", "demo", "worker", "terminated", true)+`,`+
					sessionJSON("demo-orch", "demo", "orchestrator", "terminated", true)+`]}`)
			default:
				_, _ = io.WriteString(w, `{"sessions":[`+
					sessionJSON("demo-2", "demo", "orchestrator", "idle", false)+`,`+
					sessionJSON("demo-1", "demo", "worker", "working", false)+`]}`)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "working", false)+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/justvibedev/tallpine-dev","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/pr/claim":
			var req apitypes.ClaimPRRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if req.AllowTakeover == nil || !*req.AllowTakeover {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"error":"conflict","code":"PR_CLAIMED_BY_ACTIVE_SESSION","message":"PR is already claimed by active session demo-2 (omit --no-takeover to steal)"}`)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","prs":[{"url":`+jsonQuote(req.PR)+`,"number":142,"state":"open","ci":"passing","review":"review_required","mergeability":"mergeable","reviewComments":false,"updatedAt":"2026-06-04T12:00:00Z"}],"branchChanged":true,"takenOverFrom":["demo-0"]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/cleanup":
			_, _ = io.WriteString(w, `{"ok":true,"cleaned":["demo-old","demo-orch"],"skipped":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/kill":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","freed":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/restore":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","session":`+sessionJSON("demo-1", "demo", "worker", "idle", false)+`}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/exit-agent":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","session":`+sessionJSON("demo-1", "demo", "worker", "exited", false)+`}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/resume-agent":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","resumeMode":"native","session":`+sessionJSON("demo-1", "demo", "worker", "idle", false)+`}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/sessions/demo-1":
			var req apitypes.RenameSessionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","displayName":`+jsonQuote(req.DisplayName)+`}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/sessions/demo-2":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"bad_request","code":"ORCHESTRATOR_RENAME_UNSUPPORTED","message":"The project orchestrator cannot be renamed"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func sessionJSON(id, project, kind, status string, terminated bool) string {
	b, _ := json.Marshal(map[string]any{
		"id":           id,
		"projectId":    project,
		"kind":         kind,
		"harness":      "codex",
		"displayName":  "Current Name",
		"activity":     map[string]any{"state": "idle", "lastActivityAt": "2026-06-02T12:00:00Z"},
		"isTerminated": terminated,
		"createdAt":    "2026-06-02T11:00:00Z",
		"updatedAt":    "2026-06-02T12:00:00Z",
		"status":       status,
	})
	return string(b)
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestSessionList_ProjectFilterAndDefaultFiltering(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "ls", "--project", "demo")
	if err != nil {
		t.Fatalf("session ls failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "demo:") || !strings.Contains(out, "demo-1") {
		t.Fatalf("output missing worker session:\n%s", out)
	}
	if strings.Contains(out, "demo-2") {
		t.Fatalf("orchestrator session should be hidden without --all:\n%s", out)
	}
	if !strings.Contains(out, "1 terminated session hidden") {
		t.Fatalf("hidden terminated hint missing:\n%s", out)
	}
	if !strings.Contains(out, "2 orchestrator sessions hidden. Use --all or `tallpine orchestrator ls` to show.") {
		t.Fatalf("hidden orchestrator hint missing:\n%s", out)
	}
	want := []string{
		"GET /api/v1/sessions?active=true&project=demo",
		"GET /api/v1/sessions?active=false&project=demo",
	}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionList_HintsWhenOnlyTerminatedOrchestratorIsHidden(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sessions" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("active") == "false" {
			_, _ = io.WriteString(w, `{"sessions":[`+sessionJSON("demo-orch-old", "demo", "orchestrator", "terminated", true)+`]}`)
			return
		}
		_, _ = io.WriteString(w, `{"sessions":[]}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "ls", "--project", "demo")
	if err != nil {
		t.Fatalf("session ls failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "1 orchestrator session hidden. Use --all or `tallpine orchestrator ls` to show.") {
		t.Fatalf("terminated orchestrator hint missing:\n%s", out)
	}
	if strings.Contains(out, "terminated session hidden") {
		t.Fatalf("terminated orchestrator should not be reported as visible via --include-terminated alone:\n%s", out)
	}
}

func TestSessionList_JSONOutputDecodes(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "ls", "--project", "demo", "--json")
	if err != nil {
		t.Fatalf("session ls --json failed: %v\nstderr=%s", err, errOut)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("session ls --json output is not decodable: %v\noutput=%s", err, out)
	}
	if got.Meta.HiddenTerminatedCount != 1 {
		t.Fatalf("hiddenTerminatedCount = %d, want 1", got.Meta.HiddenTerminatedCount)
	}
	if got.Meta.HiddenOrchestratorCount != 2 {
		t.Fatalf("hiddenOrchestratorCount = %d, want 2", got.Meta.HiddenOrchestratorCount)
	}
	if len(got.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1; data=%#v", len(got.Data), got.Data)
	}
	if got.Data[0].ID != "demo-1" || got.Data[0].ProjectID != "demo" || got.Data[0].Role != "worker" {
		t.Fatalf("unexpected JSON entry: %#v", got.Data[0])
	}
	// `session get --json` already carries the display name; `ls --json` must too so scripts can tell sessions apart.
	if got.Data[0].DisplayName != "Current Name" {
		t.Fatalf("displayName = %q, want %q", got.Data[0].DisplayName, "Current Name")
	}
}

func TestSessionList_EnrichesPRColumnsAndKeepsFallbackFacts(t *testing.T) {
	cfg := setConfigEnv(t)
	var detailsMu sync.Mutex
	detailRequests := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/sessions":
			if r.URL.Query().Get("active") == "false" {
				_, _ = io.WriteString(w, `{"sessions":[]}`)
				return
			}
			_, _ = io.WriteString(w, `{"sessions":[
				{"id":"demo-1","projectId":"demo","kind":"worker","branch":"feat/INT-1327","activity":{"state":"working","lastActivityAt":"2026-06-02T12:00:00Z"},"isTerminated":false,"createdAt":"2026-06-02T11:00:00Z","updatedAt":"2026-06-02T12:00:00Z","status":"working","prs":[{"number":3,"ci":"passing","review":"approved"}]},
				{"id":"demo-2","projectId":"demo","kind":"worker","branch":"feat/INT-1328","activity":{"state":"idle","lastActivityAt":"2026-06-02T11:55:00Z"},"isTerminated":false,"createdAt":"2026-06-02T11:00:00Z","updatedAt":"2026-06-02T12:00:00Z","status":"idle","prs":[{"number":2,"ci":"failing","review":"changes_requested"}]}
			]}`)
		case "/api/v1/sessions/demo-1/pr":
			detailsMu.Lock()
			detailRequests["demo-1"]++
			detailsMu.Unlock()
			_, _ = io.WriteString(w, `{"sessionId":"demo-1","prs":[{"number":3,"ci":{"state":"passing"},"review":{"decision":"approved","unresolvedThreadCount":2}}]}`)
		case "/api/v1/sessions/demo-2/pr":
			detailsMu.Lock()
			detailRequests["demo-2"]++
			detailsMu.Unlock()
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"message":"SCM temporarily unavailable"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	now := time.Date(2026, 6, 2, 12, 5, 0, 0, time.UTC)
	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
		Now:          func() time.Time { return now },
	}, "session", "ls", "--project", "demo")
	if err != nil {
		t.Fatalf("session ls failed: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{"SESSION", "ROLE", "BRANCH", "THREADS", "demo-1", "worker", "feat/INT-1327", "#3", "passing", "approved", "2", "working", "5m ago", "demo-2", "#2", "failing", "changes_requested"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	detailsMu.Lock()
	gotRequests := map[string]int{"demo-1": detailRequests["demo-1"], "demo-2": detailRequests["demo-2"]}
	detailsMu.Unlock()
	if !reflect.DeepEqual(gotRequests, map[string]int{"demo-1": 1, "demo-2": 1}) {
		t.Fatalf("PR summary requests = %#v, want one per session", gotRequests)
	}

	out, errOut, err = executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
		Now:          func() time.Time { return now },
	}, "session", "ls", "--project", "demo", "--json")
	if err != nil {
		t.Fatalf("session ls --json failed: %v\nstderr=%s", err, errOut)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode enriched JSON: %v\noutput=%s", err, out)
	}
	if len(got.Data) != 2 || got.Data[0].Branch != "feat/INT-1327" || got.Data[0].Activity != "working" || got.Data[0].PRs[0].Threads == nil || *got.Data[0].PRs[0].Threads != 2 {
		t.Fatalf("first enriched entry = %#v, want branch, activity, and 2 threads", got.Data)
	}
	if got.Data[1].PRs[0].Threads != nil || got.Data[1].PRs[0].CI != "failing" || got.Data[1].PRs[0].Review != "changes_requested" {
		t.Fatalf("fallback entry = %#v, want basic facts and unknown thread count", got.Data[1])
	}
}

func TestSessionList_ThreadCountUnknownVersusObservedZero(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/sessions":
			if r.URL.Query().Get("active") == "false" {
				_, _ = io.WriteString(w, `{"sessions":[]}`)
				return
			}
			_, _ = io.WriteString(w, `{"sessions":[
				{"id":"old-1","projectId":"demo","kind":"worker","activity":{"state":"idle","lastActivityAt":"2026-06-02T11:55:00Z"},"isTerminated":false,"createdAt":"2026-06-02T11:00:00Z","updatedAt":"2026-06-02T12:00:00Z","status":"idle","prs":[{"number":1,"ci":"passing","review":"approved"}]},
				{"id":"zero-1","projectId":"demo","kind":"worker","activity":{"state":"idle","lastActivityAt":"2026-06-02T11:55:00Z"},"isTerminated":false,"createdAt":"2026-06-02T11:00:00Z","updatedAt":"2026-06-02T12:00:00Z","status":"idle","prs":[{"number":2,"ci":"passing","review":"approved"}]}
			]}`)
		case "/api/v1/sessions/old-1/pr":
			// A pre-change daemon: the summary endpoint succeeds but does not
			// carry unresolvedThreadCount. That must read as unknown, not zero.
			_, _ = io.WriteString(w, `{"sessionId":"old-1","prs":[{"number":1,"ci":{"state":"passing"},"review":{"decision":"approved"}}]}`)
		case "/api/v1/sessions/zero-1/pr":
			_, _ = io.WriteString(w, `{"sessionId":"zero-1","prs":[{"number":2,"ci":{"state":"passing"},"review":{"decision":"approved","unresolvedThreadCount":0}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
		Now:          func() time.Time { return time.Date(2026, 6, 2, 12, 5, 0, 0, time.UTC) },
	}, "session", "ls", "--project", "demo", "--json")
	if err != nil {
		t.Fatalf("session ls --json failed: %v\nstderr=%s", err, errOut)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode JSON: %v\noutput=%s", err, out)
	}
	if len(got.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2; data=%#v", len(got.Data), got.Data)
	}
	if got.Data[0].PRs[0].Threads != nil {
		t.Fatalf("older-daemon entry threads = %#v, want unknown (nil)", got.Data[0].PRs[0].Threads)
	}
	if got.Data[1].PRs[0].Threads == nil || *got.Data[1].PRs[0].Threads != 0 {
		t.Fatalf("observed-zero entry threads = %#v, want explicit 0", got.Data[1].PRs[0].Threads)
	}

	out, errOut, err = executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
		Now:          func() time.Time { return time.Date(2026, 6, 2, 12, 5, 0, 0, time.UTC) },
	}, "session", "ls", "--project", "demo")
	if err != nil {
		t.Fatalf("session ls failed: %v\nstderr=%s", err, errOut)
	}
	for _, row := range []string{"old-1", "zero-1"} {
		if !strings.Contains(out, row) {
			t.Fatalf("output missing %q:\n%s", row, out)
		}
	}
	if !strings.Contains(out, "-") {
		t.Fatalf("table output missing unknown-count dash:\n%s", out)
	}
}

func TestSessionList_AllIncludesOrchestratorsWithoutHiddenHint(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "ls", "--project", "demo", "--all")
	if err != nil {
		t.Fatalf("session ls --all failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "demo-1") || !strings.Contains(out, "demo-2") {
		t.Fatalf("output missing worker or orchestrator session:\n%s", out)
	}
	fields := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(fields, "demo-1 Current Name worker") || !strings.Contains(fields, "demo-2 Current Name orchestrator") {
		t.Fatalf("output does not distinguish worker and orchestrator roles:\n%s", out)
	}
	if strings.Contains(out, "orchestrator session hidden") {
		t.Fatalf("output reports hidden orchestrators with --all:\n%s", out)
	}
}

func TestSessionGet_SuccessWithProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "get", "demo-1", "-p", "demo")
	if err != nil {
		t.Fatalf("session get failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "id: demo-1") || !strings.Contains(out, "project: demo") {
		t.Fatalf("unexpected get output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionGet_JSONOutputDecodes(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "get", "demo-1", "--project", "demo", "--json")
	if err != nil {
		t.Fatalf("session get --json failed: %v\nstderr=%s", err, errOut)
	}
	var got apitypes.SessionResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("session get --json output is not decodable: %v\noutput=%s", err, out)
	}
	if got.Session.ID != "demo-1" || got.Session.ProjectID != "demo" || got.Session.Status != "working" {
		t.Fatalf("unexpected session JSON: %#v", got.Session)
	}
}

func TestSessionGet_ShowsLastAssistantMessage(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-2", "demo", "orchestrator", "idle", false)+
			`,"lastAssistantMessage":{"text":"EVAL-COMPLETE\nall 3 merged","at":"2026-10-25T20:11:38Z"}}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-2")
	if err != nil {
		t.Fatalf("session get failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "last message (2026-10-25T20:11:38Z):\n  EVAL-COMPLETE\n  all 3 merged\n") {
		t.Fatalf("last message not shown:\n%s", out)
	}
	out, errOut, err = executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-2", "--json")
	if err != nil {
		t.Fatalf("session get --json failed: %v\nstderr=%s", err, errOut)
	}
	var got apitypes.SessionResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.LastAssistantMessage == nil ||
		got.LastAssistantMessage.Text != "EVAL-COMPLETE\nall 3 merged" {
		t.Fatalf("session get --json = %s (err %v)", out, err)
	}
}

func TestSessionRename_SuccessWithProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "rename", "demo-1", "New Name", "-p", "demo")
	if err != nil {
		t.Fatalf("session rename failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, `session demo-1 renamed to "New Name"`) {
		t.Fatalf("unexpected rename output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1", "PATCH /api/v1/sessions/demo-1"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionRename_OrchestratorRefusalIsRuntimeError(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "rename", "demo-2", "New Name")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "ORCHESTRATOR_RENAME_UNSUPPORTED") {
		t.Fatalf("err=%v exit=%d, want orchestrator rename refusal runtime error", err, ExitCode(err))
	}
	if strings.Contains(out, "renamed") {
		t.Fatalf("refused rename reported success:\n%s", out)
	}
}

func TestSessionCommands_MissingIDIsUsageError(t *testing.T) {
	setConfigEnv(t)
	for _, sub := range []string{"get", "kill", "restore", "exit-agent", "resume-agent"} {
		t.Run(sub, func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{}, "session", sub)
			if err == nil {
				t.Fatal("expected missing id to fail")
			}
			if got := ExitCode(err); got != 2 {
				t.Fatalf("exit code = %d, want 2 (err=%v)", got, err)
			}
		})
	}
}

func TestSessionRename_MissingNameIsUsageError(t *testing.T) {
	setConfigEnv(t)

	_, _, err := executeCLI(t, Deps{}, "session", "rename", "demo-1")
	if err == nil {
		t.Fatal("expected missing name to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2 (err=%v)", got, err)
	}
}

func TestSessionGet_ProjectMismatchDoesNotPassScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "get", "demo-1", "--project", "other")
	if err == nil {
		t.Fatal("expected project mismatch to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "not in project other") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSessionRename_ProjectMismatchDoesNotPatch(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "rename", "demo-1", "New Name", "--project", "other")
	if err == nil {
		t.Fatal("expected project mismatch to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "not in project other") {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"GET /api/v1/sessions/demo-1"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

// A spawn and a restore run the project's Setup, which the daemon bounds by
// the Setup steps' own limits; a client timeout would cut a long Setup short.
func TestSetupCallsHaveNoClientTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		call string
		args []string
	}{
		{"spawn", "POST /api/v1/sessions", []string{"spawn", "--project", "demo", "--agent", "codex", "--name", "worker"}},
		{"restore", "POST /api/v1/sessions/demo-1/restore", []string{"session", "restore", "demo-1", "-p", "demo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
					_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","defaultBranch":"main"}}`)
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
					_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
					_, _ = io.WriteString(w, `{"session":{"id":"demo-9","status":"idle"}}`)
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
			var seen bool
			var deadline time.Time
			var hasDeadline bool
			client := &http.Client{Timeout: 2 * time.Second, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method+" "+req.URL.Path == tc.call {
					seen = true
					deadline, hasDeadline = req.Context().Deadline()
				}
				return http.DefaultTransport.RoundTrip(req)
			})}
			_, errOut, err := executeCLI(t, Deps{HTTPClient: client, ProcessAlive: func(int) bool { return true }}, tc.args...)
			if err != nil {
				t.Fatalf("%s failed: %v\nstderr=%s", tc.name, err, errOut)
			}
			if !seen {
				t.Fatalf("%s never called %s", tc.name, tc.call)
			}
			if hasDeadline {
				t.Fatalf("%s request deadline in %s, want no client timeout", tc.call, time.Until(deadline).Round(time.Minute))
			}
		})
	}
}

func TestSessionPausedUntilShownInListAndDetails(t *testing.T) {
	until := time.Date(2026, 11, 1, 20, 20, 0, 0, time.UTC)
	if got := activityWithPause("idle", &until); !strings.HasPrefix(got, "idle (paused until ") {
		t.Fatalf("activityWithPause = %q", got)
	}
	if got := activityWithPause("idle", nil); got != "idle" {
		t.Fatalf("unpaused activity = %q", got)
	}
	cmd := &cobra.Command{}
	var buf strings.Builder
	cmd.SetOut(&buf)
	if err := writeSessionDetails(cmd, apitypes.SessionView{ID: "s-1", PausedUntil: &until}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "paused until: 2026-11-01T20:20:00Z (provider usage limit)") {
		t.Fatalf("details missing the pause:\n%s", buf.String())
	}
}
