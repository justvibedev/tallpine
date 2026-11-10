package cli

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

const effortBaseConfig = `{
	"agentConfig":{"model":"opus"},
	"worker":{"agent":"codex","agentConfig":{"effort":"low"}},
	"orchestrator":{"agentConfig":{"effort":"low","model":"m"}},
	"reviewers":[{"harness":"codex","agentConfig":{"model":"gpt-5","effort":"low"}},{"harness":"claude-code"}]
}`

func TestProjectSetConfigRoleEffortFlags(t *testing.T) {
	got, _, err := runSetConfig(t, effortBaseConfig, "--worker-effort", "high", "--orchestrator-effort", "high", "--reviewer-effort", "high")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"worker":       map[string]any{"agent": "codex", "agentConfig": map[string]any{"effort": "high"}},
		"orchestrator": map[string]any{"agentConfig": map[string]any{"effort": "high", "model": "m"}},
		"reviewers": []any{
			map[string]any{"harness": "codex", "agentConfig": map[string]any{"model": "gpt-5", "effort": "high"}},
			map[string]any{"harness": "claude-code", "agentConfig": map[string]any{"effort": "high"}},
		},
	}
	for k, v := range want {
		if !reflect.DeepEqual(got[k], v) {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestProjectSetConfigRoleEffortClear(t *testing.T) {
	got, _, err := runSetConfig(t, effortBaseConfig, "--worker-effort", "", "--orchestrator-effort", "", "--reviewer-effort", "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	var want map[string]any
	_ = json.Unmarshal([]byte(`{"agentConfig":{"model":"opus"},"worker":{"agent":"codex"},"orchestrator":{"agentConfig":{"model":"m"}},
	"reviewers":[{"harness":"codex","agentConfig":{"model":"gpt-5"}},{"harness":"claude-code"}]}`), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %s", b)
	}
}

func TestProjectSetConfigReviewerEffortNeedsReviewer(t *testing.T) {
	if _, _, err := runSetConfig(t, `{}`, "--reviewer-effort", "high"); err == nil {
		t.Fatal("want error without reviewers")
	}
}

func TestProjectSetConfigJSONKeepsEffort(t *testing.T) {
	in := `{"worker":{"agentConfig":{"effort":"high"}},"orchestrator":{"agentConfig":{"effort":"high"}},"agentConfig":{"effort":"high"},"reviewers":[{"harness":"codex","agentConfig":{"effort":"high"}}]}`
	got, _, err := runSetConfig(t, `{}`, "--config-json", in)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	_ = json.Unmarshal([]byte(in), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestProjectGetJSONShowsEffort(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := projectServer(t, http.StatusOK, `{"status":"ok","project":{"id":"demo","config":{"worker":{"agentConfig":{"effort":"high"}},"reviewers":[{"harness":"codex","agentConfig":{"effort":"high"}}]}}}`)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, aliveDeps(), "project", "get", "demo", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got projectGetResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	c := got.Project.Config
	if c == nil || c.Worker.AgentConfig.Effort != "high" || c.Reviewers[0].AgentConfig.Effort != "high" {
		t.Fatalf("effort lost in get --json: %s", out)
	}
}

func TestProjectSetConfigEffortRejectsUnknownLevel(t *testing.T) {
	for _, flag := range []string{"--worker-effort", "--orchestrator-effort", "--reviewer-effort"} {
		if _, calls, err := runSetConfig(t, effortBaseConfig, flag, "hgih"); err == nil || len(calls) > 1 {
			t.Fatalf("%s: want usage error before any PUT, err=%v calls=%v", flag, err, calls)
		}
	}
}

func TestProjectSetConfigReviewerAndEffortTogether(t *testing.T) {
	got, _, err := runSetConfig(t, `{}`, "--reviewer", "codex", "--reviewer-effort", "high")
	if err != nil {
		t.Fatal(err)
	}
	want := []any{map[string]any{"harness": "codex", "agentConfig": map[string]any{"effort": "high"}}}
	if !reflect.DeepEqual(got["reviewers"], want) {
		t.Fatalf("reviewers = %v", got["reviewers"])
	}
}

func TestProjectSetConfigEffortIsLowercased(t *testing.T) {
	got, _, err := runSetConfig(t, `{}`, "--worker-effort", " HIGH ")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"agentConfig": map[string]any{"effort": "high"}}
	if !reflect.DeepEqual(got["worker"], want) {
		t.Fatalf("worker = %v", got["worker"])
	}
}
