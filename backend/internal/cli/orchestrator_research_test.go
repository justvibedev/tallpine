package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// researchServer records each research call and answers with a research status.
func researchServer(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path+" "+strings.TrimSpace(string(body)))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"active":true,"available":true,"windowTokens":250000,"researchWindow":"250k","since":"2026-11-03T10:00:00Z"}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, setConfigEnv(t), srv)
	return &calls
}

func TestOrchestratorResearchOnAndOffPostTheSwitch(t *testing.T) {
	calls := researchServer(t)
	out, _, err := executeCLI(t, aliveDeps(), "orchestrator", "research", "on", "--session", "orch-1")
	if err != nil || !strings.Contains(out, "Research mode on") || !strings.Contains(out, "250k tokens") {
		t.Fatalf("on: out=%q err=%v", out, err)
	}
	if _, _, err := executeCLI(t, aliveDeps(), "orchestrator", "research", "off", "--session", "orch-1"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`POST /api/v1/orchestrators/orch-1/research {"enabled":true}`,
		`POST /api/v1/orchestrators/orch-1/research {"enabled":false}`,
	}
	if strings.Join(*calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %v, want %v", *calls, want)
	}
}

func TestOrchestratorResearchStatusReads(t *testing.T) {
	calls := researchServer(t)
	if _, _, err := executeCLI(t, aliveDeps(), "orchestrator", "research", "status", "--session", "orch-1"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !strings.HasPrefix((*calls)[0], "GET /api/v1/orchestrators/orch-1/research") {
		t.Fatalf("calls = %v", *calls)
	}
}

func TestOrchestratorResearchNeedsASession(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "")
	researchServer(t)
	if _, _, err := executeCLI(t, aliveDeps(), "orchestrator", "research", "on"); err == nil {
		t.Fatal("want a usage error without a session id")
	}
}

// --orchestrator-research-window alone uses the narrow rules endpoint.
func TestProjectSetConfigResearchWindowUsesRulesEndpoint(t *testing.T) {
	calls, put := runSetConfigRaw(t, "--orchestrator-research-window", "off")
	if len(calls) != 1 || calls[0] != "PUT /api/v1/projects/demo/rules" || strings.TrimSpace(put) != `{"orchestratorResearchCompactWindow":"off"}` {
		t.Fatalf("calls=%v body=%s", calls, put)
	}
}
