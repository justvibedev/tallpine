package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// runSetConfigRaw runs set-config against a server that records each call and
// the last PUT body verbatim.
func runSetConfigRaw(t *testing.T, args ...string) ([]string, string) {
	t.Helper()
	cfg := setConfigEnv(t)
	var calls []string
	var put string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			put = string(body)
		}
		_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","config":{}}}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	if _, _, err := executeCLI(t, aliveDeps(), append([]string{"project", "set-config", "demo"}, args...)...); err != nil {
		t.Fatal(err)
	}
	return calls, put
}

// B189: rules-only flags use the narrow rules endpoint (the one an
// orchestrator token may call) and send only the flags given.
func TestProjectSetConfigRulesOnlyUsesRulesEndpoint(t *testing.T) {
	calls, put := runSetConfigRaw(t, "--orchestrator-rules", "Delegate.")
	if len(calls) != 1 || calls[0] != "PUT /api/v1/projects/demo/rules" {
		t.Fatalf("calls = %v", calls)
	}
	if strings.TrimSpace(put) != `{"orchestratorRules":"Delegate."}` {
		t.Fatalf("body = %s", put)
	}
	calls, put = runSetConfigRaw(t, "--agent-rules", "", "--orchestrator-rules", "x")
	if len(calls) != 1 || !strings.Contains(put, `"agentRules":""`) {
		t.Fatalf("clear agent rules: calls=%v body=%s", calls, put)
	}
}

// Any non-rules flag keeps the general merge path.
func TestProjectSetConfigMixedFlagsUseConfigEndpoint(t *testing.T) {
	calls, _ := runSetConfigRaw(t, "--agent-rules", "x", "--model", "m")
	if len(calls) != 2 || calls[1] != "PUT /api/v1/projects/demo/config" {
		t.Fatalf("calls = %v", calls)
	}
	calls, _ = runSetConfigRaw(t, "--agent-rules-file", "docs/rules.md")
	if len(calls) != 2 || calls[1] != "PUT /api/v1/projects/demo/config" {
		t.Fatalf("calls = %v", calls)
	}
}
