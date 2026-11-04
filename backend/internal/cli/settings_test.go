package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const settingsLogBody = `{"changes":[{"id":2,"changedAt":"2026-10-31T12:01:00Z","actor":"orchestrator:app-1","projectId":"app","key":"project.agentRules","oldValue":"null","newValue":"\"` + "0123456789012345678901234567890123456789012345678901234567890123456789" + `\""},{"id":1,"changedAt":"2026-10-31T12:00:00Z","actor":"user","projectId":"app","key":"project.noCI","oldValue":"null","newValue":"true"}]}`

func settingsLogServer(t *testing.T, requests *[]string, body string) *httptest.Server {
	return settingsAPIServer(t, requests, "/api/v1/settings/changes", body)
}

// settingsAPIServer answers GET path with body and records every request.
func settingsAPIServer(t *testing.T, requests *[]string, path, body string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests = append(*requests, r.URL.RequestURI())
		if r.Method != http.MethodGet || r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSettingsLogRendersChangesWithActor(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	writeRunFileFor(t, cfg, settingsLogServer(t, &requests, settingsLogBody))
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "settings", "log", "--project", "my app", "--limit", "5")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"2026-10-31T12:01:00Z  orchestrator:app-1  project.agentRules: null -> \"0123456789",
		"…\n",
		"2026-10-31T12:00:00Z  user  project.noCI: null -> true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if len(requests) != 1 || requests[0] != "/api/v1/settings/changes?limit=5&project=my+app" {
		t.Fatalf("requests = %v", requests)
	}
}

func TestSettingsLogAppWideJSONAndEmpty(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	writeRunFileFor(t, cfg, settingsLogServer(t, &requests, settingsLogBody))
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "settings", "log", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"key": "project.agentRules"`) || !strings.Contains(out, "0123456789012345678901234567890123456789012345678901234567890123456789") {
		t.Fatalf("json output lost data:\n%s", out)
	}
	if requests[0] != "/api/v1/settings/changes" {
		t.Fatalf("requests = %v", requests)
	}

	cfg = setConfigEnv(t)
	writeRunFileFor(t, cfg, settingsLogServer(t, &requests, `{"changes":[]}`))
	out, _, err = executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "settings", "log")
	if err != nil || !strings.Contains(out, "No settings changes recorded.") {
		t.Fatalf("empty log: %v\n%s", err, out)
	}
}
