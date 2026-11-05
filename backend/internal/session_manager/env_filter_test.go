package sessionmanager

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func envFilterManager(t *testing.T, settings map[string]map[string]any, tokens GitHubAccountTokens) *Manager {
	t.Helper()
	m := githubEnvManager(t, tokens)
	m.dataDir = t.TempDir()
	m.executable = func() (string, error) { return "/opt/tallpine/tallpine", nil }
	engine, _ := policytest.Engine(t, settings)
	m.SetAgentPolicy(engine)
	return m
}

func allowlist(passthrough ...string) map[string]map[string]any {
	fields := map[string]any{"mode": "allowlist"}
	if passthrough != nil {
		fields["passthrough"] = passthrough
	}
	return map[string]map[string]any{"envFilter": fields}
}

func TestAllowlistSessionEnvIsTheAllowedDaemonVariablesPlusTheSessionsOwn(t *testing.T) {
	t.Setenv("B3_DB_PASSWORD", "leak")
	t.Setenv("ANTHROPIC_API_KEY", "needed")
	t.Setenv("B3_ALLOWED", "yes")
	t.Setenv("LANG", "daemon-lang")
	m := envFilterManager(t, allowlist("B3_ALLOWED"), &fakeGitHubTokens{})
	env := m.runtimeEnv("mer-1", "mer", "", map[string]string{"LANG": "project-lang", "B3_PROJECT": "kept"})
	if _, leaked := env["B3_DB_PASSWORD"]; leaked {
		t.Fatalf("a daemon secret reached the agent environment: %v", env)
	}
	for key, want := range map[string]string{"ANTHROPIC_API_KEY": "needed", "B3_ALLOWED": "yes", "LANG": "project-lang", "B3_PROJECT": "kept", EnvSessionID: "mer-1", agentlaunch.EnvScrub: "1"} {
		if env[key] != want {
			t.Errorf("env[%s] = %q, want %q", key, env[key], want)
		}
	}
	if env["PATH"] == "" {
		t.Fatal("PATH must survive")
	}
}

func TestInheritModeSessionEnvIsUntouched(t *testing.T) {
	t.Setenv("B3_DB_PASSWORD", "leak")
	for name, settings := range map[string]map[string]map[string]any{"no policy": nil, "explicit inherit": {"envFilter": {"mode": "inherit"}}} {
		m := envFilterManager(t, settings, &fakeGitHubTokens{})
		env := m.runtimeEnv("mer-1", "mer", "", nil)
		if _, ok := env["B3_DB_PASSWORD"]; ok || env[agentlaunch.EnvScrub] != "" {
			t.Fatalf("%s must behave as before: the runtime inherits, the map holds only the session's own: %v", name, env)
		}
	}
}

func TestAllowlistKeepsGitHubTokensOutWhenTheCredentialRuleDoes(t *testing.T) {
	settings := allowlist("GH_TOKEN", "GITHUB_*")
	settings["githubCreds"] = map[string]any{"mode": "helper"}
	m := envFilterManager(t, settings, &fakeGitHubTokens{token: "picked"})
	t.Setenv("GH_TOKEN", "daemon-token")
	env := m.runtimeEnv("mer-1", "mer", "", nil)
	for k, v := range env {
		if v == "daemon-token" || v == "picked" || k == "GH_TOKEN" {
			t.Fatalf("helper mode keeps tokens out even when the allowlist would pass them: %s", k)
		}
	}
}

// Under the allowlist the daemon's own token never reaches the agent, so it must
// not count as "already configured" and suppress the account pick (which would
// leave the agent with no token at all).
func TestAllowlistDaemonTokenThatIsNotPassedDoesNotBlockTheAccountPick(t *testing.T) {
	f := &fakeGitHubTokens{token: "picked-token"}
	m := envFilterManager(t, allowlist(), f)
	t.Setenv("GITHUB_TOKEN", "from-daemon") // after the manager: its fixture clears the token variables
	if env := m.runtimeEnv("mer-1", "mer", "", nil); env["GH_TOKEN"] != "picked-token" || env["GITHUB_TOKEN"] != "" {
		t.Fatalf("%v", env)
	}
	f.calls = 0
	m = envFilterManager(t, allowlist("TALLPINE_GITHUB_TOKEN"), f)
	t.Setenv("TALLPINE_GITHUB_TOKEN", "from-daemon")
	if env := m.runtimeEnv("mer-1", "mer", "", nil); env["TALLPINE_GITHUB_TOKEN"] != "from-daemon" || f.calls != 0 {
		t.Fatalf("a passed-through daemon token is explicit and wins: %v (calls=%d)", env, f.calls)
	}
}

func TestDecisionRecordNamesTheDroppedVariablesNeverTheirValues(t *testing.T) {
	t.Setenv("B3_DB_PASSWORD", "super-secret-value")
	t.Setenv("ANTHROPIC_API_KEY", "kept-value")
	rec, st := spawnWithPolicy(t, allowlist())
	raw := st.policyRecord(rec.ID)
	var got struct {
		Rules map[string]struct {
			Mode         string   `json:"-"`
			KeptCount    int      `json:"keptCount"`
			DroppedCount int      `json:"droppedCount"`
			DroppedNames []string `json:"droppedNames"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	f := got.Rules["envFilter"]
	if f.KeptCount == 0 || f.DroppedCount == 0 || !contains(f.DroppedNames, "B3_DB_PASSWORD") || contains(f.DroppedNames, "ANTHROPIC_API_KEY") {
		t.Fatalf("record = %s", raw)
	}
	if strings.Contains(string(raw), "super-secret-value") || strings.Contains(string(raw), "kept-value") {
		t.Fatalf("the record must carry variable names only, never values: %s", raw)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestAllowlistGivesEachHarnessOnlyItsOwnCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "o")
	t.Setenv("ANTHROPIC_API_KEY", "a")
	t.Setenv("AWS_ACCESS_KEY_ID", "aws")
	t.Setenv("HTTPS_PROXY", "http://proxy")
	cases := []struct {
		harness    domain.AgentHarness
		has, lacks []string
	}{
		{domain.HarnessClaudeCode, []string{"ANTHROPIC_API_KEY", "AWS_ACCESS_KEY_ID", "HTTPS_PROXY", "PATH"}, []string{"OPENAI_API_KEY"}},
		{domain.HarnessCodex, []string{"OPENAI_API_KEY", "HTTPS_PROXY", "PATH"}, []string{"ANTHROPIC_API_KEY", "AWS_ACCESS_KEY_ID"}},
	}
	for _, c := range cases {
		m := envFilterManager(t, allowlist(), &fakeGitHubTokens{})
		m.store.(*fakeStore).sessions["w-1"] = domain.SessionRecord{ID: "w-1", ProjectID: "mer", Harness: c.harness}
		env := m.runtimeEnv("w-1", "mer", "", nil)
		for _, k := range c.has {
			if env[k] == "" {
				t.Errorf("%s lost %s", c.harness, k)
			}
		}
		for _, k := range c.lacks {
			if _, ok := env[k]; ok {
				t.Errorf("%s kept %s", c.harness, k)
			}
		}
	}
}

// A chat provider switch prepares the target's env while the stored row still
// names the source harness: the launch's harness decides, not the store's.
func TestAllowlistChatSwitchUsesTargetHarnessNotStoredOne(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "o")
	t.Setenv("ANTHROPIC_API_KEY", "a")
	m := envFilterManager(t, allowlist(), &fakeGitHubTokens{})
	rec := domain.SessionRecord{ID: "w-1", ProjectID: "mer", Harness: domain.HarnessClaudeCode}
	m.store.(*fakeStore).sessions["w-1"] = rec
	_, env, err := m.prepareChatControllerEnv(context.Background(), rec, domain.ProjectConfig{}, rec.ControllerOwner(), domain.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	if env["OPENAI_API_KEY"] != "o" {
		t.Error("codex target lost OPENAI_API_KEY")
	}
	if _, ok := env["ANTHROPIC_API_KEY"]; ok {
		t.Error("codex target kept the source harness's ANTHROPIC_API_KEY")
	}
}
