package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func TestSessionUsage_JSONAndTable(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("TALLPINE_SESSION_ID", "demo-1")
	var requested string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.Method + " " + r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"model":"claude-sonnet-5-5","inputTokens":10,"cacheCreationTokens":200,"cacheReadTokens":3000,"outputTokens":40,"turns":2}]}`))
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	out, errOut, err := executeCLI(t, deps, "session", "usage", "--json")
	if err != nil {
		t.Fatalf("session usage --json: %v\nstderr=%s", err, errOut)
	}
	if requested != "GET /api/v1/sessions/demo-1/usage" {
		t.Fatalf("request = %q", requested)
	}
	var got sessionUsageResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(got.Models) != 1 || got.Models[0].CacheReadTokens != 3000 || got.Models[0].CacheCreationTokens != 200 || got.Models[0].Turns != 2 {
		t.Fatalf("models = %+v", got.Models)
	}

	out, _, err = executeCLI(t, deps, "session", "usage", "other-2")
	if err != nil {
		t.Fatalf("session usage: %v", err)
	}
	if !strings.Contains(out, "claude-sonnet-5-5") || !strings.Contains(out, "CACHE-READ") || requested != "GET /api/v1/sessions/other-2/usage" {
		t.Fatalf("table output = %q (request %q)", out, requested)
	}
}

func TestWriteSessionDetailsShowsModel(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := writeSessionDetails(cmd, apitypes.SessionView{ID: "s1", Harness: "codex", Model: "gpt-5.6-luna"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "model: gpt-5.6-luna\n") {
		t.Fatalf("model line missing: %q", buf.String())
	}
}
