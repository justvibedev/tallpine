package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const policyBody = `{"schemaVersion":2,"rules":[{"name":"permissionCap","titleKey":"t","fields":[{"name":"max","kind":"enum","value":"auto","source":"managed","locked":true},{"name":"other","kind":"bool","value":false,"source":"default","locked":false}]}],"managedPath":"/etc/x/policy.json","warnings":["something odd"]}`

func policyServer(t *testing.T, paths *[]string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*paths = append(*paths, r.URL.Path)
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/agent-policy") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, policyBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPolicyShowRendersEveryRuleTheDaemonReports(t *testing.T) {
	cfg := setConfigEnv(t)
	var paths []string
	writeRunFileFor(t, cfg, policyServer(t, &paths))
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "policy", "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`permissionCap.max: "auto" (managed, locked)`, "permissionCap.other: false (default)", "managed file: /etc/x/policy.json", "warning: something odd"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if len(paths) != 1 || paths[0] != "/api/v1/settings/agent-policy" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestPolicyShowProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	var paths []string
	writeRunFileFor(t, cfg, policyServer(t, &paths))
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "policy", "show", "--project", "my proj"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "/api/v1/projects/my proj/agent-policy" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestPolicyShowSessionPrintsTheDecisionRecord(t *testing.T) {
	cfg := setConfigEnv(t)
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sessionId":"p-1","recordedAt":"2026-10-28T12:00:00Z","record":{"v":2,"rules":{"permissionCap":{"requested":"bypass-permissions","effective":"auto"}}}}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "policy", "show", "--session", "p-1")
	if err != nil || !strings.Contains(out, `"effective": "auto"`) || len(paths) != 1 || paths[0] != "/api/v1/sessions/p-1/agent-policy" {
		t.Fatalf("%v %s %v", err, out, paths)
	}
}
