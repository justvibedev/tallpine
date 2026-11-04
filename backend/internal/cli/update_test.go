package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func updateServer(t *testing.T, posted *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/update"):
			_, _ = io.WriteString(w, `{"installedCommit":"1111111111111111","candidate":"2222222222222222","updateAvailable":true,"buildAvailable":false,"last":{"state":"rolled_back","commit":"3333333333333333","message":"unhealthy"}}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/update"):
			_ = json.NewDecoder(r.Body).Decode(posted)
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"state":"running","commit":"2222222222222222"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUpdateStatusShowsCandidateAndLastResult(t *testing.T) {
	cfg := setConfigEnv(t)
	var posted map[string]any
	writeRunFileFor(t, cfg, updateServer(t, &posted))
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "update", "status")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	for _, want := range []string{"Installed: 111111111111", "Update available: 222222222222 (CI passed, will be built)", "Last update: rolled_back"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestUpdateStartPostsTheCandidate(t *testing.T) {
	cfg := setConfigEnv(t)
	var posted map[string]any
	writeRunFileFor(t, cfg, updateServer(t, &posted))
	if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "update", "start"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if posted["commit"] != "2222222222222222" {
		t.Fatalf("posted %v", posted)
	}
}

func TestUpdateRefusedInsideAgentSession(t *testing.T) {
	cfg := setConfigEnv(t)
	var posted map[string]any
	writeRunFileFor(t, cfg, updateServer(t, &posted))
	t.Setenv("TALLPINE_SESSION_ID", "app-1")
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "update", "start", "abc1234")
	if err == nil || posted != nil {
		t.Fatalf("err %v posted %v", err, posted)
	}
}
