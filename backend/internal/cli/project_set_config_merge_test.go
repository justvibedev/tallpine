package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const mergeBaseConfig = `{
	"canonicalRepoURL":"https://github.com/acme/repo",
	"sessionPrefix":"work",
	"preRemove":["./cleanup.sh"],
	"env":{"KEEP":"1","CHANGE":"old"},
	"symlinks":["a"],
	"agentConfig":{"model":"opus","permissions":"auto"},
	"trackerIntake":{"enabled":true,"provider":"github","repo":"acme/repo"},
	"reviewers":[{"harness":"codex","agentConfig":{"model":"gpt-5"}}],
	"containerReap":{"disabled":true}
}`

// mergeConfigServer serves GET /projects/{id} with a stored config and records
// the PUT /projects/{id}/config body.
func mergeConfigServer(t *testing.T, stored string) (*httptest.Server, *[]string, *map[string]any) {
	t.Helper()
	var calls []string
	var put map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","config":`+stored+`}}`)
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			var req struct {
				Config map[string]any `json:"config"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("decode PUT body %s: %v", body, err)
			}
			put = req.Config
			_, _ = io.WriteString(w, `{"project":{"id":"demo"}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &put
}

func runSetConfig(t *testing.T, stored string, args ...string) (map[string]any, []string, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	srv, calls, put := mergeConfigServer(t, stored)
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, aliveDeps(), append([]string{"project", "set-config", "demo"}, args...)...)
	return *put, *calls, err
}

func TestProjectSetConfigFocusedFlagMergesIntoExistingConfig(t *testing.T) {
	got, calls, err := runSetConfig(t, mergeBaseConfig, "--canonical-repo-url", "https://github.com/acme/new")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "GET /api/v1/projects/demo" || calls[1] != "PUT /api/v1/projects/demo/config" {
		t.Fatalf("calls = %v", calls)
	}
	if got["canonicalRepoURL"] != "https://github.com/acme/new" {
		t.Fatalf("canonicalRepoURL = %v", got["canonicalRepoURL"])
	}
	// Everything else, including a field the CLI mirror struct lacks (preRemove), survives.
	var want map[string]any
	_ = json.Unmarshal([]byte(mergeBaseConfig), &want)
	for k, v := range want {
		if k == "canonicalRepoURL" {
			continue
		}
		gotJSON, _ := json.Marshal(got[k])
		wantJSON, _ := json.Marshal(v)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("%s = %s, want preserved %s", k, gotJSON, wantJSON)
		}
	}
}

func TestProjectSetConfigMergeSemantics(t *testing.T) {
	got, _, err := runSetConfig(t, mergeBaseConfig,
		"--env", "CHANGE=new", "--env", "ADD=2",
		"--model", "sonnet",
		"--tracker-assignee", "alice",
		"--session-prefix", "",
		"--symlink", "b", "--symlink", "c",
		"--reviewer", "codex", "--reviewer", "claude-code",
	)
	if err != nil {
		t.Fatal(err)
	}
	env := got["env"].(map[string]any)
	if env["KEEP"] != "1" || env["CHANGE"] != "new" || env["ADD"] != "2" {
		t.Fatalf("env = %v, want merged by key", env)
	}
	agent := got["agentConfig"].(map[string]any)
	if agent["model"] != "sonnet" || agent["permissions"] != "auto" {
		t.Fatalf("agentConfig = %v", agent)
	}
	tracker := got["trackerIntake"].(map[string]any)
	if tracker["assignee"] != "alice" || tracker["enabled"] != true || tracker["repo"] != "acme/repo" {
		t.Fatalf("trackerIntake = %v", tracker)
	}
	if _, ok := got["sessionPrefix"]; ok {
		t.Fatalf("empty scalar flag should remove the field: %v", got)
	}
	if links := got["symlinks"].([]any); len(links) != 2 || links[0] != "b" {
		t.Fatalf("symlinks = %v, want list replaced", links)
	}
	reviewers := got["reviewers"].([]any)
	first := reviewers[0].(map[string]any)
	if len(reviewers) != 2 || first["harness"] != "codex" || first["agentConfig"] == nil {
		t.Fatalf("reviewers = %v, want codex keeps agentConfig + claude-code added", reviewers)
	}
	if got["containerReap"] == nil || got["preRemove"] == nil {
		t.Fatalf("untouched fields lost: %v", got)
	}
}

func TestProjectSetConfigTrackerIntakeFalseDisables(t *testing.T) {
	got, _, err := runSetConfig(t, mergeBaseConfig, "--tracker-intake=false")
	if err != nil {
		t.Fatal(err)
	}
	tracker := got["trackerIntake"].(map[string]any)
	if _, ok := tracker["enabled"]; ok || tracker["repo"] != "acme/repo" {
		t.Fatalf("trackerIntake = %v", tracker)
	}
}

func TestProjectSetConfigFlagOnEmptyConfig(t *testing.T) {
	got, _, err := runSetConfig(t, `null`, "--default-branch", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["defaultBranch"] != "main" {
		t.Fatalf("config = %v", got)
	}
}

func TestProjectSetConfigConfigJSONAndClearStillReplace(t *testing.T) {
	got, calls, err := runSetConfig(t, mergeBaseConfig, "--config-json", `{"sessionPrefix":"only"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || got["sessionPrefix"] != "only" || got["env"] != nil || got["preRemove"] != nil || got["canonicalRepoURL"] != nil {
		t.Fatalf("config-json: calls = %v, config = %v", calls, got)
	}
	got, calls, err = runSetConfig(t, mergeBaseConfig, "--clear")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || got["sessionPrefix"] != nil || got["env"] != nil {
		t.Fatalf("clear: calls = %v, config = %v", calls, got)
	}
}

func TestProjectSetConfigRequiresAFlag(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, aliveDeps(), "project", "set-config", "demo")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit = %d, err = %v", got, err)
	}
}

func TestProjectSetConfigNoCIFlag(t *testing.T) {
	got, _, err := runSetConfig(t, mergeBaseConfig, "--no-ci")
	if err != nil {
		t.Fatal(err)
	}
	if got["noCI"] != true || got["sessionPrefix"] != "work" {
		t.Fatalf("config = %v, want noCI set and other fields kept", got)
	}

	got, _, err = runSetConfig(t, `{"noCI":true,"sessionPrefix":"work"}`, "--no-ci=false")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["noCI"]; ok || got["sessionPrefix"] != "work" {
		t.Fatalf("config = %v, want noCI removed and other fields kept", got)
	}
}

func TestProjectSetConfigOtherFlagKeepsNoCI(t *testing.T) {
	got, _, err := runSetConfig(t, `{"noCI":true}`, "--session-prefix", "work")
	if err != nil {
		t.Fatal(err)
	}
	if got["noCI"] != true {
		t.Fatalf("config = %v, want noCI preserved", got)
	}
}

// planAutoApprove is tri-state: true and an explicit false are both stored,
// an unrelated flag keeps either, and only --plan-auto-approve-inherit clears it.
func TestProjectSetConfigPlanAutoApproveTriState(t *testing.T) {
	got, _, err := runSetConfig(t, mergeBaseConfig, "--plan-auto-approve")
	if err != nil || got["planAutoApprove"] != true {
		t.Fatalf("on: config = %v, err = %v", got, err)
	}
	got, _, err = runSetConfig(t, `{"planAutoApprove":true,"sessionPrefix":"work"}`, "--plan-auto-approve=false")
	if err != nil || got["planAutoApprove"] != false || got["sessionPrefix"] != "work" {
		t.Fatalf("explicit off: config = %v, err = %v", got, err)
	}
	got, _, err = runSetConfig(t, `{"planAutoApprove":false}`, "--session-prefix", "work")
	if err != nil || got["planAutoApprove"] != false {
		t.Fatalf("round-trip: config = %v, err = %v, want the explicit false kept", got, err)
	}
	got, _, err = runSetConfig(t, `{"planAutoApprove":false,"sessionPrefix":"work"}`, "--plan-auto-approve-inherit")
	if _, ok := got["planAutoApprove"]; err != nil || ok || got["sessionPrefix"] != "work" {
		t.Fatalf("inherit: config = %v, err = %v, want planAutoApprove cleared", got, err)
	}
}
