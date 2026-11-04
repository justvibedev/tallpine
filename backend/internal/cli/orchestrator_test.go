package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func orchestratorCommandServer(t *testing.T) (*httptest.Server, *sessionRequestLog) {
	t.Helper()
	log := &sessionRequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.append(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/orchestrators":
			_, _ = io.WriteString(w, `{"sessions":[`+
				sessionJSON("other-orch", "other", "orchestrator", "idle", false)+`,`+
				sessionJSON("demo-worker", "demo", "worker", "working", false)+`,`+
				sessionJSON("demo-orch", "demo", "orchestrator", "working", false)+`]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func TestOrchestratorList_TableOutput(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := orchestratorCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "orchestrator", "ls")
	if err != nil {
		t.Fatalf("orchestrator ls failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "demo:") || !strings.Contains(out, "demo-orch") {
		t.Fatalf("output missing demo orchestrator:\n%s", out)
	}
	if !strings.Contains(out, "other:") || !strings.Contains(out, "other-orch") {
		t.Fatalf("output missing other orchestrator:\n%s", out)
	}
	if strings.Contains(out, "demo-worker") {
		t.Fatalf("worker session should not be shown in orchestrator ls:\n%s", out)
	}
	want := []string{"GET /api/v1/orchestrators"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestOrchestratorList_JSONOutputDecodes(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := orchestratorCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "orchestrator", "ls", "--json")
	if err != nil {
		t.Fatalf("orchestrator ls --json failed: %v\nstderr=%s", err, errOut)
	}
	var got orchestratorListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("orchestrator ls --json output is not decodable: %v\noutput=%s", err, out)
	}
	if len(got.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2; data=%#v", len(got.Data), got.Data)
	}
	if got.Data[0].ID != "demo-orch" || got.Data[0].ProjectID != "demo" || got.Data[0].Role != "orchestrator" {
		t.Fatalf("unexpected first JSON entry: %#v", got.Data[0])
	}
	if got.Data[1].ID != "other-orch" || got.Data[1].ProjectID != "other" || got.Data[1].Role != "orchestrator" {
		t.Fatalf("unexpected second JSON entry: %#v", got.Data[1])
	}
}

func TestOrchestratorHandoff_PostsOwnSessionAndNote(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("TALLPINE_SESSION_ID", "demo-orch")
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.Method+" "+r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"orchestrator":{"id":"demo-orch-2","projectId":"demo"}}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "orchestrator", "handoff", "--note", "/tmp/state.md")
	if err != nil {
		t.Fatalf("handoff failed: %v\nstderr=%s", err, errOut)
	}
	if gotPath != "POST /api/v1/orchestrators/demo-orch/handoff" || gotBody != `{"notePath":"/tmp/state.md"}` {
		t.Fatalf("request = %s %s", gotPath, gotBody)
	}
	if !strings.Contains(out, "demo-orch-2") {
		t.Fatalf("output missing the new id:\n%s", out)
	}
}

func TestOrchestratorHandoff_RequiresASession(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("TALLPINE_SESSION_ID", "")
	if _, _, err := executeCLI(t, Deps{}, "orchestrator", "handoff", "--note", "/tmp/state.md"); err == nil {
		t.Fatal("handoff without a session id succeeded")
	}
}

func TestOrchestratorEnd_PostsTheSessionToTheEndpoint(t *testing.T) {
	cfg := setConfigEnv(t)
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.Method+" "+r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"removed":"demo-orch-2","orchestrator":"demo-orch","workers":["w1","w2"],"notifyFailed":[]}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "orchestrator", "end", "--session", "demo-orch-2")
	if err != nil {
		t.Fatalf("end failed: %v\nstderr=%s", err, errOut)
	}
	if gotPath != "POST /api/v1/orchestrators/end" || gotBody != `{"sessionId":"demo-orch-2"}` {
		t.Fatalf("request = %s %s", gotPath, gotBody)
	}
	if !strings.Contains(out, "Removed demo-orch-2") || !strings.Contains(out, "2 live worker(s) now report to demo-orch") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestOrchestratorEnd_NeedsExactlyOneTarget(t *testing.T) {
	setConfigEnv(t)
	for _, args := range [][]string{{"orchestrator", "end"}, {"orchestrator", "end", "--lane", "B12", "--session", "demo-orch-2"}} {
		if _, _, err := executeCLI(t, Deps{}, args...); err == nil {
			t.Fatalf("%v succeeded without exactly one of --lane / --session", args)
		}
	}
}

// orchestratorNewServer answers orchestrator new, then serves the session
// states in order (the last one repeats).
func orchestratorNewServer(t *testing.T, states []string) *httptest.Server {
	t.Helper()
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"project":{"id":"demo","name":"demo"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/orchestrators":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"orchestrator":{"id":"demo-1","projectId":"demo"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			st := states[min(polls, len(states)-1)]
			polls++
			extra := ""
			if st == "failed" {
				extra = `,"provisionError":"workspace: default branch is unresolved"`
			}
			_, _ = io.WriteString(w, `{"session":{"id":"demo-1","projectId":"demo","kind":"orchestrator","provisionState":"`+st+`"`+extra+`}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runOrchestratorNew(t *testing.T, states []string) (string, error) {
	t.Helper()
	oldPoll := orchestratorStartPoll
	orchestratorStartPoll = time.Millisecond
	t.Cleanup(func() { orchestratorStartPoll = oldPoll })
	cfg := setConfigEnv(t)
	writeRunFileFor(t, cfg, orchestratorNewServer(t, states))
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "orchestrator", "new", "--project", "demo")
	return out, err
}

func TestOrchestratorNew_ReportsFailedAsyncStart(t *testing.T) {
	out, err := runOrchestratorNew(t, []string{"provisioning", "provisioning", "failed"})
	if err == nil || !strings.Contains(err.Error(), "default branch is unresolved") {
		t.Fatalf("err = %v, out = %q; want the start failure", err, out)
	}
	if strings.Contains(out, "is the default orchestrator") {
		t.Fatalf("printed success for a failed start: %q", out)
	}
}

func TestOrchestratorNew_WaitsForProvisioningToBecomeReady(t *testing.T) {
	out, err := runOrchestratorNew(t, []string{"provisioning", "provisioning", "ready"})
	if err != nil || !strings.Contains(out, "demo-1 is the default orchestrator") {
		t.Fatalf("err = %v, out = %q; want success", err, out)
	}
}

func TestOrchestratorNew_StillProvisioningAtDeadlineIsSuccess(t *testing.T) {
	old := orchestratorStartWait
	orchestratorStartWait = 20 * time.Millisecond
	t.Cleanup(func() { orchestratorStartWait = old })
	out, err := runOrchestratorNew(t, []string{"provisioning"})
	if err != nil || !strings.Contains(out, "is the default orchestrator") {
		t.Fatalf("err = %v, out = %q; want success at deadline", err, out)
	}
}
