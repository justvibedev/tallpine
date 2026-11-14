package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type fixedBrowserCapability string

func (f fixedBrowserCapability) Issue(_ domain.SessionID) (string, string, error) {
	return string(f), "verifier-1", nil
}

type browserCapabilityIssue struct {
	token    string
	verifier string
	err      error
}

type scriptedBrowserCapabilities struct {
	issues  []browserCapabilityIssue
	calls   int
	onIssue func(call int, id domain.SessionID)
}

func (s *scriptedBrowserCapabilities) Issue(id domain.SessionID) (string, string, error) {
	call := s.calls
	s.calls++
	if s.onIssue != nil {
		s.onIssue(call, id)
	}
	if call >= len(s.issues) {
		return "", "", errors.New("unexpected browser capability issuance")
	}
	issue := s.issues[call]
	return issue.token, issue.verifier, issue.err
}

func TestSpawnEnvProjectVarsCannotOverrideInternal(t *testing.T) {
	t.Parallel()

	env := spawnEnv("mer-1", "mer", "issue-9", "/data", map[string]string{
		"FOO":        "bar",
		EnvSessionID: "hacked", // a project must not override Tallpine-internal vars
		EnvProjectID: "hacked",
	})
	if env["FOO"] != "bar" {
		t.Fatalf("FOO = %q, want bar", env["FOO"])
	}
	if env[EnvSessionID] != "mer-1" {
		t.Fatalf("TALLPINE_SESSION_ID = %q, want mer-1 (internal wins)", env[EnvSessionID])
	}
	if env[EnvProjectID] != "mer" {
		t.Fatalf("TALLPINE_PROJECT_ID = %q, want mer (internal wins)", env[EnvProjectID])
	}
}

func TestSpawnEnvWindowsRemovesCaseVariantsOfProtectedVariables(t *testing.T) {
	t.Parallel()

	env := spawnEnvForOS("mer-1", "mer", "issue-9", `C:\tallpine`, map[string]string{
		"tallpine_session_id": "hacked",
		"buildMode":           "production",
	}, true)
	if _, ok := env["tallpine_session_id"]; ok {
		t.Fatal("case variant of protected TALLPINE_SESSION_ID survived")
	}
	if env[EnvSessionID] != "mer-1" || env["buildMode"] != "production" {
		t.Fatalf("environment = %v, want protected ID and untouched project variable spelling", env)
	}
}

func TestRuntimeEnvInjectsBrowserCapability(t *testing.T) {
	t.Parallel()

	manager := &Manager{
		dataDir:             "/data",
		browserCapabilities: fixedBrowserCapability("capability-1"),
		executable:          func() (string, error) { return filepath.Join("/opt", "aod", "tallpine"), nil },
		logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	env, verifier, err := manager.launchRuntimeEnv("mer-1", "mer", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if env[EnvBrowserCapability] != "capability-1" {
		t.Fatalf("%s = %q", EnvBrowserCapability, env[EnvBrowserCapability])
	}
	if verifier != "verifier-1" {
		t.Fatalf("verifier = %q", verifier)
	}
}

func TestRuntimeEnvClearsDaemonBrowserRuntimeSecrets(t *testing.T) {
	t.Parallel()

	manager := &Manager{
		dataDir:    "/data",
		executable: func() (string, error) { return filepath.Join("/opt", "aod", "tallpine"), nil },
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	env := manager.runtimeEnv("mer-1", "mer", "", map[string]string{
		EnvBrowserRuntimeToken:      "runtime-secret",
		EnvBrowserRuntimeTokenStdin: "1",
	})
	if env[EnvBrowserRuntimeToken] != "" || env[EnvBrowserRuntimeTokenStdin] != "" {
		t.Fatalf("daemon browser runtime credentials leaked to worker: token=%q stdin=%q", env[EnvBrowserRuntimeToken], env[EnvBrowserRuntimeTokenStdin])
	}
}

func TestRuntimeEnvWindowsRemovesCaseVariantsOfProtectedVariables(t *testing.T) {
	daemonRunFile := filepath.Join(t.TempDir(), "daemon-running.json")
	previous := envKeysCaseInsensitive
	envKeysCaseInsensitive = true
	t.Cleanup(func() { envKeysCaseInsensitive = previous })

	manager := &Manager{
		dataDir:     `C:\tallpine`,
		runFilePath: daemonRunFile,
		executable:  func() (string, error) { return fakeDaemonExe(t), nil },
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	env := manager.runtimeEnv("mer-1", "mer", "issue-9", map[string]string{
		"Path":                                 `C:\project\bin`,
		"tallpine_session_id":                  "hacked",
		"tallpine_Project_Id":                  "hacked",
		"tallpine_Issue_ID":                    "hacked",
		"tallpine_Data_Dir":                    "hacked",
		"tallpine_run_file":                    "hacked",
		"tallpine_browser_runtime_token":       "runtime-secret",
		"tallpine_browser_runtime_token_stdin": "1",
		"buildMode":                            "production",
	})

	for _, key := range []string{
		"Path",
		"tallpine_session_id",
		"tallpine_Project_Id",
		"tallpine_Issue_ID",
		"tallpine_Data_Dir",
		"tallpine_run_file",
		"tallpine_browser_runtime_token",
		"tallpine_browser_runtime_token_stdin",
	} {
		if _, ok := env[key]; ok {
			t.Fatalf("case variant %s survived in runtime env: %v", key, env)
		}
	}
	if env["PATH"] == "" {
		t.Fatalf("PATH was not pinned: %v", env)
	}
	if env[EnvSessionID] != "mer-1" || env[EnvProjectID] != "mer" || env[EnvIssueID] != "issue-9" || env[EnvDataDir] != `C:\tallpine` {
		t.Fatalf("protected Tallpine env = %v", env)
	}
	if env[EnvRunFile] != daemonRunFile || env[EnvBrowserRuntimeToken] != "" || env[EnvBrowserRuntimeTokenStdin] != "" {
		t.Fatalf("runtime protected env = %v", env)
	}
	if env["buildMode"] != "production" {
		t.Fatalf("project env spelling was not preserved: %v", env)
	}
}

func TestRuntimeEnvPinsHooksToDaemonRunFile(t *testing.T) {
	daemonRunFile := filepath.Join(t.TempDir(), "daemon-running.json")
	t.Setenv("TALLPINE_RUN_FILE", filepath.Join(t.TempDir(), "inherited-wrong-daemon.json"))
	manager := &Manager{
		dataDir:     "/data",
		runFilePath: daemonRunFile,
		executable:  func() (string, error) { return filepath.Join("/opt", "aod", "tallpine"), nil },
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	env := manager.runtimeEnv("mer-1", "mer", "", map[string]string{
		"TALLPINE_RUN_FILE": "/project/cannot-redirect-hooks.json",
	})
	if got, want := env["TALLPINE_RUN_FILE"], daemonRunFile; got != want {
		t.Fatalf("TALLPINE_RUN_FILE = %q, want daemon run-file %q", got, want)
	}
}

func TestHookPATH(t *testing.T) {
	t.Parallel()

	sep := string(os.PathListSeparator)
	daemonExe := filepath.Join("/opt", "aod", "tallpine")
	daemonDir := filepath.Dir(daemonExe)
	exeOK := func() (string, error) { return daemonExe, nil }

	cases := []struct {
		name       string
		executable func() (string, error)
		daemonPATH string
		projectEnv map[string]string
		want       string
		wantErr    bool
	}{
		{
			name:       "prepends daemon dir to inherited PATH",
			executable: exeOK,
			daemonPATH: "/usr/bin" + sep + "/bin",
			want:       daemonDir + sep + "/usr/bin" + sep + "/bin",
		},
		{
			name:       "project PATH override is the base",
			executable: exeOK,
			daemonPATH: "/usr/bin",
			projectEnv: map[string]string{"PATH": "/proj/bin"},
			want:       daemonDir + sep + "/proj/bin",
		},
		{
			name:       "empty base PATH yields the daemon dir alone",
			executable: exeOK,
			want:       daemonDir,
		},
		{
			name:       "unresolvable executable fails",
			executable: func() (string, error) { return "", errors.New("no exe") },
			daemonPATH: "/usr/bin",
			wantErr:    true,
		},
		{
			// A daemon binary not named "tp" cannot anchor `tallpine` resolution by
			// having its directory prepended, so the pin must be refused.
			name:       "executable not named tallpine fails",
			executable: func() (string, error) { return filepath.Join("/opt", "aod", "tallpine-daemon"), nil },
			daemonPATH: "/usr/bin",
			wantErr:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string {
				if key == "PATH" {
					return tc.daemonPATH
				}
				return ""
			}
			got, err := HookPATH(tc.executable, getenv, tc.projectEnv, "/data")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("HookPATH = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("HookPATH: %v", err)
			}
			if got != tc.want {
				t.Fatalf("HookPATH = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffectiveHarnessAndAgentConfig(t *testing.T) {
	t.Parallel()

	cfg := domain.ProjectConfig{
		AgentConfig:  domain.AgentConfig{Model: "base", Effort: "medium", Mode: "low", Permissions: domain.PermissionModeAuto},
		Worker:       domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "worker", Effort: "high", Mode: "high"}},
		Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode},
	}

	// Explicit harness always wins.
	if h := effectiveHarness(domain.AgentHarness("aider"), domain.KindWorker, cfg); h != domain.AgentHarness("aider") {
		t.Fatalf("explicit harness = %q, want aider", h)
	}
	// Empty harness falls back to the role override per kind.
	if h := effectiveHarness("", domain.KindWorker, cfg); h != domain.HarnessCodex {
		t.Fatalf("worker harness = %q, want codex", h)
	}
	if h := effectiveHarness("", domain.KindOrchestrator, cfg); h != domain.HarnessClaudeCode {
		t.Fatalf("orchestrator harness = %q, want claude-code", h)
	}

	// Role override merges over the base agent config (set fields win; unset keep base).
	got := effectiveAgentConfig(domain.HarnessCodex, domain.KindWorker, cfg)
	if got.Model != "worker" || got.Effort != "high" || got.Mode != "high" || got.Permissions != domain.PermissionModeAuto {
		t.Fatalf("merged worker config = %#v, want model=worker mode=high permissions=auto", got)
	}
	// Orchestrator has no agent-config override, so the base config is used as-is.
	if got := effectiveAgentConfig(domain.HarnessClaudeCode, domain.KindOrchestrator, cfg); got.Model != "base" {
		t.Fatalf("orchestrator config = %#v, want base", got)
	}
	// A role pinned to a harness this build no longer ships also drops the
	// project-level model/effort/mode that were tuned for it, so the fallback
	// harness is not launched with e.g. an opencode model id. Permissions stay.
	removed := domain.ProjectConfig{
		AgentConfig: domain.AgentConfig{Model: "opencode/model", Effort: "high", Mode: "low", Permissions: domain.PermissionModeAuto},
		Worker:      domain.RoleOverride{Harness: "opencode", AgentConfig: domain.AgentConfig{Model: "worker"}},
	}
	if got := effectiveAgentConfig(domain.FallbackHarness, domain.KindWorker, removed); got.Model != "" || got.Effort != "" || got.Mode != "" || got.Permissions != domain.PermissionModeAuto {
		t.Fatalf("removed-harness worker config = %#v, want model/effort/mode dropped and permissions kept", got)
	}
	// A launch harness that differs from the role's configured harness drops the
	// role's model/mode — they were tuned for the other agent — but keeps the
	// harness-neutral permissions.
	if got := effectiveAgentConfig(domain.AgentHarness("aider"), domain.KindWorker, cfg); got.Model != "" || got.Mode != "" || got.Permissions != domain.PermissionModeAuto {
		t.Fatalf("mismatched-harness worker config = %#v, want model/mode dropped, permissions=auto", got)
	}
	// A role override with no harness pinned is deliberately treated as
	// "applies to any harness", so its model/mode are inherited whichever
	// harness the session launches with. This is a behavior decision, not a
	// side effect: assert it across two unrelated harnesses so it cannot
	// silently flip back to the old drop-on-every-switch behavior.
	unpinned := domain.ProjectConfig{
		AgentConfig: domain.AgentConfig{Model: "base", Mode: "low"},
		Worker:      domain.RoleOverride{AgentConfig: domain.AgentConfig{Model: "worker", Mode: "high"}},
	}
	for _, harness := range []domain.AgentHarness{domain.AgentHarness("aider"), domain.HarnessCodex} {
		got := effectiveAgentConfig(harness, domain.KindWorker, unpinned)
		if got.Model != "worker" || got.Mode != "high" {
			t.Fatalf("unpinned worker config for %q = %#v, want model=worker mode=high", harness, got)
		}
	}
}

type tuningCatalog struct {
	catalog ports.AgentModelCatalog
	err     error
	calls   *int
}

func (c tuningCatalog) Models(context.Context, string, string, bool) (ports.AgentModelCatalog, error) {
	if c.calls != nil {
		*c.calls++
	}
	return c.catalog, c.err
}

func TestResolveChatAgentConfigValidatesAndResetsDependentTuning(t *testing.T) {
	t.Parallel()

	m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "old", Efforts: []string{"high"}},
		{ID: "new", Efforts: []string{"low"}},
	}}}}
	project := domain.ProjectConfig{Worker: domain.RoleOverride{AgentConfig: domain.AgentConfig{Model: "old", Effort: "high"}}}
	resolved, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: ports.AgentConfig{Model: "new"},
	}, project)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "new" || resolved.Effort != "" {
		t.Fatalf("resolved = %#v, want new model with provider defaults", resolved)
	}
	resolved, err = m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: ports.AgentConfig{Model: "old"}, EffortOverride: true,
	}, project)
	if err != nil || resolved.Effort != "" {
		t.Fatalf("explicit provider defaults did not clear role tuning: %#v, %v", resolved, err)
	}

	_, err = m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: ports.AgentConfig{Model: "new", Effort: "high"},
	}, project)
	if !errors.Is(err, ports.ErrUnsupportedEffort) {
		t.Fatalf("error = %v, want ErrUnsupportedEffort", err)
	}

	resolved, err = m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: ports.AgentConfig{Model: "custom"},
	}, project)
	if err != nil || resolved.Model != "custom" || resolved.Effort != "" {
		t.Fatalf("custom model with provider defaults = %#v, %v", resolved, err)
	}

	m.modelCatalog = tuningCatalog{err: errors.New("discovery failed")}
	resolved, err = m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: ports.AgentConfig{Model: "new"},
	}, domain.ProjectConfig{})
	if err != nil || resolved.Model != "new" {
		t.Fatalf("provider defaults should survive discovery failure: %#v, %v", resolved, err)
	}
	_, err = m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: ports.AgentConfig{Model: "new", Effort: "high"},
	}, domain.ProjectConfig{})
	if !errors.Is(err, ports.ErrModelCapabilitiesUnavailable) {
		t.Fatalf("error = %v, want ErrModelCapabilitiesUnavailable", err)
	}
}

// refreshRecordingCatalog serves a saved catalog and a rediscovered one, and
// records which a launch asked for.
type refreshRecordingCatalog struct {
	saved, fresh ports.AgentModelCatalog
	refreshes    *[]bool
}

func (c refreshRecordingCatalog) Models(_ context.Context, _, _ string, refresh bool) (ports.AgentModelCatalog, error) {
	*c.refreshes = append(*c.refreshes, refresh)
	if refresh {
		return c.fresh, nil
	}
	return c.saved, nil
}

func TestResolveAgentConfigValidatesAgainstSavedCatalogFirst(t *testing.T) {
	t.Parallel()

	spawn := ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		AgentConfig: ports.AgentConfig{Model: "sonnet", Effort: "high"}, EffortOverride: true,
	}
	var refreshes []bool
	m := &Manager{modelCatalog: refreshRecordingCatalog{refreshes: &refreshes, saved: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "sonnet", Efforts: []string{"high"}},
	}}}}
	if _, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, spawn, domain.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	if len(refreshes) != 1 || refreshes[0] {
		t.Fatalf("catalog reads = %v, want one saved read and no rediscovery", refreshes)
	}

	// A model the saved catalog does not list yet is checked against a live
	// rediscovery before the launch is rejected.
	refreshes = nil
	m = &Manager{modelCatalog: refreshRecordingCatalog{refreshes: &refreshes,
		saved: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "opus", Efforts: []string{"high"}}}},
		fresh: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "sonnet", Efforts: []string{"high"}}}},
	}}
	resolved, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, spawn, domain.ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Effort != "high" || len(refreshes) != 2 || refreshes[0] || !refreshes[1] {
		t.Fatalf("resolved=%#v reads=%v, want a saved read then a rediscovery", resolved, refreshes)
	}
}

func TestResolveAgentConfigRediscoversUnusableSavedCatalog(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"queued", "refreshing", "error"} {
		t.Run(state, func(t *testing.T) {
			var refreshes []bool
			m := &Manager{modelCatalog: refreshRecordingCatalog{refreshes: &refreshes,
				saved: ports.AgentModelCatalog{RefreshState: state, Models: []ports.AgentModelInfo{{ID: "sonnet"}}},
				fresh: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "bedrock-sonnet"}}},
			}}
			_, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
				Harness: domain.HarnessClaudeCode, AgentConfig: ports.AgentConfig{Model: "sonnet"},
			}, domain.ProjectConfig{})
			if !errors.Is(err, ErrUnsupportedModel) {
				t.Fatalf("error = %v, want provider catalog to reject old model", err)
			}
			if len(refreshes) != 2 || refreshes[0] || !refreshes[1] {
				t.Fatalf("catalog reads = %v, want saved then fresh", refreshes)
			}
		})
	}
	for _, model := range []string{"sonnet", ""} {
		t.Run("new effort/model="+model, func(t *testing.T) {
			var refreshes []bool
			m := &Manager{modelCatalog: refreshRecordingCatalog{refreshes: &refreshes,
				saved: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "sonnet", IsDefault: true, Efforts: []string{"high"}}}},
				fresh: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "sonnet", IsDefault: true, Efforts: []string{"high", "max"}}}},
			}}
			resolved, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
				Harness: domain.HarnessClaudeCode, AgentConfig: ports.AgentConfig{Model: model, Effort: "max"}, EffortOverride: true,
			}, domain.ProjectConfig{})
			if err != nil || resolved.Effort != "max" {
				t.Fatalf("new effort = %#v, %v", resolved, err)
			}
			if len(refreshes) != 2 || refreshes[0] || !refreshes[1] {
				t.Fatalf("catalog reads = %v, want saved then fresh", refreshes)
			}
		})
	}
}

func TestResolveChatAgentConfigValidatesClaudeEffort(t *testing.T) {
	t.Parallel()

	catalogCalls := 0
	m := &Manager{modelCatalog: tuningCatalog{calls: &catalogCalls, catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "sonnet", IsDefault: true, Efforts: []string{"high"}},
	}}}}
	resolved, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		AgentConfig: ports.AgentConfig{Model: "sonnet", Effort: "high"}, EffortOverride: true,
	}, domain.ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Effort != "high" {
		t.Fatalf("Claude Code effort = %q, want high", resolved.Effort)
	}
	if catalogCalls != 1 {
		t.Fatalf("Claude Code model catalog calls = %d, want 1", catalogCalls)
	}

	resolved, err = m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		AgentConfig: ports.AgentConfig{Model: "sonnet"}, EffortOverride: true,
	}, domain.ProjectConfig{Worker: domain.RoleOverride{AgentConfig: domain.AgentConfig{Effort: "high"}}})
	if err != nil || resolved.Effort != "" {
		t.Fatalf("explicit Claude provider default did not clear role tuning: %#v, %v", resolved, err)
	}
	if catalogCalls != 2 {
		t.Fatalf("provider default did not validate the selected model: %d catalog calls", catalogCalls)
	}
}

func TestResolveClaudeTUIAgentConfigRejectsUnsupportedEffort(t *testing.T) {
	t.Parallel()

	m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "sonnet", IsDefault: true, Efforts: []string{"low", "high"}},
	}}}}

	_, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		RequestedMode:  domain.SessionModeTUI,
		AgentConfig:    ports.AgentConfig{Model: "sonnet", Effort: "max"},
		EffortOverride: true,
	}, domain.ProjectConfig{})
	if !errors.Is(err, ports.ErrUnsupportedEffort) {
		t.Fatalf("error = %v, want ErrUnsupportedEffort", err)
	}
}

func TestResolveAgentConfigDeepSeekEffort(t *testing.T) {
	t.Parallel()

	m := &Manager{}
	resolve := func(effort string) (ports.AgentConfig, error) {
		return m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
			ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessDeepSeek,
			AgentConfig: ports.AgentConfig{Effort: effort}, EffortOverride: true,
		}, domain.ProjectConfig{})
	}
	got, err := resolve("high")
	if err != nil || got.Effort != "high" {
		t.Fatalf("high = %q, %v; want accepted", got.Effort, err)
	}
	if _, err := resolve("bogus"); !errors.Is(err, ports.ErrUnsupportedEffort) {
		t.Fatalf("bogus error = %v, want ErrUnsupportedEffort", err)
	}
	for _, v := range []string{"medium", "xhigh", "none", "minimal"} {
		if got, err := resolve(v); err != nil || got.Effort != v {
			t.Fatalf("%s = %q, %v; want accepted", v, got.Effort, err)
		}
	}
	for _, v := range []string{"medium", "xhigh", "bogus"} {
		got, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
			ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessDeepSeek,
			AgentConfig: ports.AgentConfig{Effort: v},
		}, domain.ProjectConfig{})
		if err != nil {
			t.Fatalf("inherited %s failed the spawn: %v", v, err)
		}
		if v == "bogus" && got.Effort != "" {
			t.Fatalf("inherited bogus effort = %q, want cleared", got.Effort)
		}
	}
}

func TestResolveAgentConfigRejectsExplicitEffortForUnsupportedHarness(t *testing.T) {
	t.Parallel()

	m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "gemini-2.5-pro", IsDefault: true, Efforts: []string{"low", "high"}},
	}}}}
	for _, harness := range []domain.AgentHarness{domain.HarnessKiro} {
		// A flagged explicit effort override must fail loudly instead of being
		// silently discarded: the caller asked for something the harness cannot
		// honor.
		_, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
			ProjectID: "p", Kind: domain.KindWorker, Harness: harness,
			AgentConfig: ports.AgentConfig{Model: "gemini-2.5-pro", Effort: "high"}, EffortOverride: true,
		}, domain.ProjectConfig{})
		if !errors.Is(err, ports.ErrUnsupportedEffort) {
			t.Fatalf("harness %q override error = %v, want ErrUnsupportedEffort", harness, err)
		}
		if !strings.Contains(err.Error(), "only supported for codex, claude-code and deepseek-harness") {
			t.Fatalf("harness %q error = %v, want support hint", harness, err)
		}
		// Unflagged request-carried effort still clears silently, matching the
		// long-standing contract for harnesses without effort support.
		resolved, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
			ProjectID: "p", Kind: domain.KindWorker, Harness: harness,
			AgentConfig: ports.AgentConfig{Model: "gemini-2.5-pro", Effort: "high"},
		}, domain.ProjectConfig{})
		if err != nil {
			t.Fatalf("harness %q unflagged error = %v, want silent clear", harness, err)
		}
		if resolved.Effort != "" {
			t.Fatalf("harness %q unflagged effort = %q, want cleared", harness, resolved.Effort)
		}
	}

	// Inherited project/role effort still clears silently: nothing explicit was
	// requested, so there is nothing to reject.
	resolved, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessKiro,
		AgentConfig: ports.AgentConfig{Model: "gemini-2.5-pro"},
	}, domain.ProjectConfig{Worker: domain.RoleOverride{AgentConfig: domain.AgentConfig{Effort: "high"}}})
	if err != nil {
		t.Fatalf("inherited effort error = %v, want silent clear", err)
	}
	if resolved.Effort != "" {
		t.Fatalf("inherited effort = %q, want provider default", resolved.Effort)
	}
}

func TestResolveClaudeModelWithoutEffortStillRequiresCatalog(t *testing.T) {
	t.Parallel()

	m := &Manager{modelCatalog: tuningCatalog{err: errors.New("gateway does not list models")}}

	_, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		AgentConfig: ports.AgentConfig{Model: "provider/model-vNext"},
	}, domain.ProjectConfig{Worker: domain.RoleOverride{AgentConfig: domain.AgentConfig{
		Model: "sonnet", Effort: "high",
	}}})
	if !errors.Is(err, ports.ErrModelCapabilitiesUnavailable) {
		t.Fatalf("error = %v, want ErrModelCapabilitiesUnavailable", err)
	}
}

func TestResolveClaudeModelWithoutEffortRejectsUnknownAndAcceptsCoveredStaleCatalog(t *testing.T) {
	t.Parallel()

	cfg := ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		RequestedMode: domain.SessionModeTUI, AgentConfig: ports.AgentConfig{Model: "provider/old"},
	}
	m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{
		CustomModelEntry: ports.CustomModelEntryDirect,
		Models:           []ports.AgentModelInfo{{ID: "provider/current"}},
	}}}
	if _, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, cfg, domain.ProjectConfig{}); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("unknown model error = %v, want ErrUnsupportedModel", err)
	}

	var refreshes []bool
	m.modelCatalog = refreshRecordingCatalog{refreshes: &refreshes, saved: ports.AgentModelCatalog{
		CustomModelEntry: ports.CustomModelEntryDirect,
		Models:           []ports.AgentModelInfo{{ID: "provider/old", Efforts: []string{"high"}}},
		Stale:            true,
		RefreshState:     "error",
	}, fresh: ports.AgentModelCatalog{}}
	got, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, cfg, domain.ProjectConfig{})
	if err != nil || got.Model != "provider/old" {
		t.Fatalf("stale catalog resolution = %+v, %v; want covered saved model", got, err)
	}
	if !reflect.DeepEqual(refreshes, []bool{false}) {
		t.Fatalf("refreshes = %v, want only the saved catalog read", refreshes)
	}
}

func TestResolveClaudeModelMissingFromStaleRefreshedCatalogIsUnavailable(t *testing.T) {
	t.Parallel()

	cfg := ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		RequestedMode: domain.SessionModeTUI, AgentConfig: ports.AgentConfig{Model: "provider/new"},
	}
	stale := ports.AgentModelCatalog{
		CustomModelEntry: ports.CustomModelEntryDirect,
		Models:           []ports.AgentModelInfo{{ID: "provider/old"}},
		Stale:            true, RefreshState: "error",
	}
	var refreshes []bool
	m := &Manager{modelCatalog: refreshRecordingCatalog{refreshes: &refreshes, saved: stale, fresh: stale}}
	_, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, cfg, domain.ProjectConfig{})
	if !errors.Is(err, ports.ErrModelCapabilitiesUnavailable) || errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("error = %v, want ErrModelCapabilitiesUnavailable only", err)
	}
}

func TestResolveCodexTUIAgentConfigFallsBackWhenCatalogCannotValidate(t *testing.T) {
	t.Parallel()

	cfg := ports.SpawnConfig{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: domain.AgentConfig{Model: "gpt-custom", Effort: "xhigh"}}
	unvalidated := domain.AgentConfig{Model: "gpt-custom", Effort: "xhigh"}
	for name, catalog := range map[string]tuningCatalog{
		"lookup failed":   {err: errors.New("not logged in")},
		"stale":           {catalog: ports.AgentModelCatalog{Stale: true, Models: []ports.AgentModelInfo{{ID: "gpt-custom", Efforts: []string{"xhigh"}}}}},
		"custom model":    {catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "gpt-5", Efforts: []string{"low"}}}}},
		"unlisted effort": {catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "gpt-custom", Efforts: []string{"low"}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			m := &Manager{modelCatalog: catalog, logger: slog.New(slog.DiscardHandler)}
			got, err := m.resolveTUIAgentConfig(context.Background(), agentpolicy.Decision{}, cfg, domain.ProjectConfig{}, unvalidated)
			if err != nil || got != unvalidated {
				t.Fatalf("got %+v, %v; want unvalidated launch", got, err)
			}
		})
	}
}

func TestResolveClaudeTUIAgentConfigStillRequiresCatalog(t *testing.T) {
	t.Parallel()

	m := &Manager{modelCatalog: tuningCatalog{err: errors.New("not logged in")}, logger: slog.New(slog.DiscardHandler)}
	cfg := ports.SpawnConfig{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		AgentConfig: domain.AgentConfig{Model: "opus", Effort: "high"}}
	if _, err := m.resolveTUIAgentConfig(context.Background(), agentpolicy.Decision{}, cfg, domain.ProjectConfig{}, cfg.AgentConfig); !errors.Is(err, ports.ErrModelCapabilitiesUnavailable) {
		t.Fatalf("err = %v, want ErrModelCapabilitiesUnavailable", err)
	}
}

func TestResolveCodexTUIAgentConfigFallsBackWhenCatalogLookupTimesOut(t *testing.T) {
	t.Parallel()

	cfg := ports.SpawnConfig{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: domain.AgentConfig{Model: "gpt-custom", Effort: "xhigh"}}
	m := &Manager{modelCatalog: tuningCatalog{err: fmt.Errorf("codex model/list: %w", context.DeadlineExceeded)}, logger: slog.New(slog.DiscardHandler)}
	got, err := m.resolveTUIAgentConfig(context.Background(), agentpolicy.Decision{}, cfg, domain.ProjectConfig{}, cfg.AgentConfig)
	if err != nil || got != cfg.AgentConfig {
		t.Fatalf("got %+v, %v; want unvalidated launch", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.modelCatalog = tuningCatalog{err: context.Canceled}
	if _, err := m.resolveTUIAgentConfig(ctx, agentpolicy.Decision{}, cfg, domain.ProjectConfig{}, cfg.AgentConfig); err == nil {
		t.Fatal("cancelled spawn launched")
	}
}

func TestApplySymlinks(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink creation requires a host privilege outside this unit test")
	}
	project := t.TempDir()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("X=1"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A present source is linked; a missing source is skipped, not an error.
	if err := applySymlinks(project, workspace, []string{".env", "missing.txt"}); err != nil {
		t.Fatalf("applySymlinks: %v", err)
	}
	target := filepath.Join(workspace, ".env")
	if data, err := os.ReadFile(target); err != nil || string(data) != "X=1" {
		t.Fatalf("symlinked .env = %q err=%v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(workspace, "missing.txt")); !os.IsNotExist(err) {
		t.Fatal("missing source should not have been linked")
	}
}

func TestApplySymlinksRejectsParentTraversal(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	workspace := t.TempDir()
	// A "..", "/" or "../" segment escapes the project tree and must be refused
	// before any stat/link runs, so a project config cannot link in arbitrary
	// host files.
	for _, bad := range []string{"../escape", "/etc/passwd", "a/../../b", ".."} {
		if err := applySymlinks(project, workspace, []string{bad}); err == nil {
			t.Fatalf("applySymlinks(%q) accepted an unsafe path", bad)
		}
	}
}

func TestRunPostCreate(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	source := t.TempDir()
	command := `printf '%s|%s|%s' "$PROJECT_TOKEN" "$TALLPINE_SOURCE_TREE_PATH" "$TALLPINE_WORKTREE_PATH" > out.txt`
	if runtime.GOOS == "windows" {
		command = `echo %PROJECT_TOKEN%^|%TALLPINE_SOURCE_TREE_PATH%^|%TALLPINE_WORKTREE_PATH%> out.txt`
	}
	if err := runPostCreate(context.Background(), workspace, source, []string{command}, map[string]string{"PROJECT_TOKEN": "setup-value"}); err != nil {
		t.Fatalf("runPostCreate: %v", err)
	}
	output, err := os.ReadFile(filepath.Join(workspace, "out.txt"))
	if err != nil {
		t.Fatalf("post-create command did not run in workspace: %v", err)
	}
	if !strings.Contains(string(output), "setup-value|"+source+"|"+workspace) {
		t.Fatalf("post-create environment = %q", output)
	}
	// A failing command surfaces an error.
	if err := runPostCreate(context.Background(), workspace, source, []string{"exit 3"}, nil); err == nil {
		t.Fatal("expected error from failing post-create command")
	}
}

func TestRunPostCreateReceivesAndRedactsProjectEnv(t *testing.T) {
	t.Parallel()

	command := `echo "$PROJECT_TOKEN" && exit 3`
	if runtime.GOOS == "windows" {
		command = `echo %PROJECT_TOKEN% && exit /b 3`
	}
	secret := "project-secret-123"
	err := runPostCreate(context.Background(), t.TempDir(), t.TempDir(), []string{command}, map[string]string{"PROJECT_TOKEN": secret})
	if err == nil || !strings.Contains(err.Error(), "[REDACTED]") || strings.Contains(err.Error(), secret) {
		t.Fatalf("postCreate error did not redact project value: %v", err)
	}
}

func TestSpawnPermissionPrecedence(t *testing.T) {
	t.Parallel()

	for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
		for _, tc := range []struct {
			name                    string
			base, role, spawn, want domain.PermissionMode
		}{
			{"unset", "", "", "", domain.PermissionModeAuto},
			{"project", domain.PermissionModeDefault, "", "", domain.PermissionModeDefault},
			{"role", domain.PermissionModeAuto, domain.PermissionModeAcceptEdits, "", domain.PermissionModeAcceptEdits},
			{"spawn", domain.PermissionModeAuto, domain.PermissionModeAcceptEdits, domain.PermissionModeDefault, domain.PermissionModeDefault},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				cfg := domain.ProjectConfig{AgentConfig: domain.AgentConfig{Permissions: tc.base}, Worker: domain.RoleOverride{AgentConfig: domain.AgentConfig{Permissions: tc.role}}, Orchestrator: domain.RoleOverride{AgentConfig: domain.AgentConfig{Permissions: tc.role}}}
				got := applySpawnAgentConfig(effectiveAgentConfig(domain.HarnessCodex, kind, cfg), domain.AgentConfig{Permissions: tc.spawn})
				if got.Permissions != tc.want {
					t.Fatalf("got %q want %q", got.Permissions, tc.want)
				}
			})
		}
	}
	if got := effectiveAgentConfig(domain.HarnessCodex, domain.KindWorker, domain.ProjectConfig{}); got.Permissions != "" {
		t.Fatalf("non-spawn resolution changed: %q", got.Permissions)
	}
}

func TestRunPostCreateFailureSaysWhatFailedAndWhereToFixIt(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("sh-only step")
	}
	err := runPostCreate(t.Context(), t.TempDir(), t.TempDir(), []string{"true", "echo could not reach registry; exit 3"}, nil)
	want := "The project's Setup script failed at step 2 (exit status 3), so the agent did not start. " +
		"Fix the script in Project settings > Scripts, then try again. Output: could not reach registry"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v\nwant    %s", err, want)
	}
}

func TestRunPostCreateStopsLaterStepsAfterCancellation(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	command := "while :; do :; done"
	if runtime.GOOS == "windows" {
		command = "for /L %i in (1,0,2) do @rem waiting"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	err := runPostCreate(ctx, workspace, t.TempDir(), []string{"", command, "echo unexpected > later.txt"}, nil)
	var setupErr *SetupStepError
	if !errors.As(err, &setupErr) || setupErr.Step != 2 || setupErr.TimedOut {
		t.Fatalf("cancellation error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "later.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("later setup step ran after cancellation: %v", err)
	}
}

// A configured family alias has no capabilities of its own; it must accept
// exactly the efforts of the family's newest concrete model.
func TestResolveClaudeFamilyAliasUsesNewestModelEfforts(t *testing.T) {
	t.Parallel()

	models := []ports.AgentModelInfo{
		{ID: "opus", Label: "Opus", IsDefault: true},
		{ID: "sonnet", Label: "Sonnet"},
		{ID: "haiku", Label: "Haiku"},
		{ID: "fable", Label: "Fable"},
		{ID: "claude-opus-4-1", Label: "Claude Opus 4.1", Efforts: []string{"low", "medium"}},
		{ID: "claude-opus-5-5", Label: "Claude Opus 5.5", Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
		{ID: "claude-sonnet-5-5", Label: "Claude Sonnet 5.5", Efforts: []string{"low", "medium", "high"}},
		{ID: "claude-haiku-4-5-20251001", Label: "Claude Haiku 4.5", Efforts: []string{"low"}},
		{ID: "claude-fable-5-1", Label: "Claude Fable 5.1", Efforts: []string{"medium", "high", "max"}},
	}
	m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{Models: models}}}
	all := []string{"low", "medium", "high", "xhigh", "max"}
	cases := map[string][]string{
		"opus":             {"low", "medium", "high", "xhigh", "max"},
		"opus[1m]":         nil,
		"sonnet":           {"low", "medium", "high"},
		"haiku":            {"low"},
		"fable":            {"medium", "high", "max"},
		"claude-opus-5-5":  {"low", "medium", "high", "xhigh", "max"},
		"claude-opus-4-1":  {"low", "medium"},
		"claude-fable-5-1": {"medium", "high", "max"},
	}
	for model, allowed := range cases {
		for _, effort := range all {
			_, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
				ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
				AgentConfig:    ports.AgentConfig{Model: model, Effort: effort},
				EffortOverride: true,
			}, domain.ProjectConfig{})
			want := containsString(allowed, effort)
			if model == "opus[1m]" {
				continue // not in catalog: rejected before effort is checked
			}
			if want && err != nil {
				t.Errorf("model %q effort %q: unexpected error %v", model, effort, err)
			}
			if !want && !errors.Is(err, ports.ErrUnsupportedEffort) {
				t.Errorf("model %q effort %q: error = %v, want ErrUnsupportedEffort", model, effort, err)
			}
		}
	}
}

// A Setup step runs with the time limit its first line sets, which never
// reaches the shell, and a step over it fails saying so.
func TestRunPostCreateUsesEachStepsTimeLimit(t *testing.T) {
	previous := scriptStepTimeoutUnit
	scriptStepTimeoutUnit = 100 * time.Millisecond
	t.Cleanup(func() { scriptStepTimeoutUnit = previous })
	workspace := t.TempDir()
	write := "echo ran > out.txt"
	slow := "sleep 2"
	if runtime.GOOS == "windows" {
		write = "echo ran> out.txt"
		slow = "ping -n 3 127.0.0.1 > NUL"
	}
	if err := runPostCreate(context.Background(), workspace, t.TempDir(), []string{"# tallpine-timeout: 30m\n" + write}, nil); err != nil {
		t.Fatalf("a timed step failed: %v", err)
	}
	if output, err := os.ReadFile(filepath.Join(workspace, "out.txt")); err != nil || strings.TrimSpace(string(output)) != "ran" {
		t.Fatalf("the timed step did not run: %q %v", output, err)
	}
	// One "minute" is 100ms here: the step stops at its own limit.
	started := time.Now()
	err := runPostCreate(context.Background(), workspace, t.TempDir(), []string{"# tallpine-timeout: 1m\n" + slow}, nil)
	if err == nil || !strings.Contains(err.Error(), "The project's Setup script stopped at step 1 after 1 minute, so the agent did not start.") || time.Since(started) > 1500*time.Millisecond {
		t.Fatalf("err = %v after %s, want the step stopped at its limit", err, time.Since(started))
	}
	// A long limit leaves the step running past the default one.
	if err := runPostCreate(context.Background(), workspace, t.TempDir(), []string{"# tallpine-timeout: 30m\n" + slow}, nil); err != nil {
		t.Fatalf("a step under its own limit failed: %v", err)
	}
}

// A start that runs Setup also gets its steps' time limits.
func TestPostCreateBudgetCoversTheSetupSteps(t *testing.T) {
	t.Parallel()

	if got := postCreateBudget(10*time.Minute, nil); got != 10*time.Minute {
		t.Fatalf("no setup = %s", got)
	}
	// A step without its own limit adds Setup's 30-minute default.
	if got := postCreateBudget(10*time.Minute, []string{"npm ci", " ", "# tallpine-timeout: 90m\nmake"}); got != 130*time.Minute {
		t.Fatalf("setup budget = %s", got)
	}
	// A typed limit, shorter than the default too, is the step's own.
	if got := postCreateBudget(10*time.Minute, []string{"# tallpine-timeout: 5m\nnpm ci", "make"}); got != 45*time.Minute {
		t.Fatalf("setup budget with a short step = %s", got)
	}
}

type deadlineProbeWorkspace struct {
	ports.Workspace
	budget time.Duration
}

func (w *deadlineProbeWorkspace) Create(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	if deadline, ok := ctx.Deadline(); ok {
		w.budget = time.Until(deadline)
	}
	return w.Workspace.Create(ctx, cfg)
}

// A REST spawn is not cut at the request's flat deadline: it runs under its
// budget plus its project's Setup step limits.
func TestRESTSpawnBudgetCoversItsSetupSteps(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	probe := &deadlineProbeWorkspace{Workspace: m.workspace}
	m.workspace = probe
	project := st.projects["mer"]
	project.Config.PostCreate = []string{"# tallpine-timeout: 90m\nmake", "npm ci"}
	st.projects["mer"] = project
	var extended time.Duration
	ctx := ports.WithSpawnRequestBudget(context.Background(), time.Minute, func(total time.Duration) { extended = total })
	_, _, _, _ = m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if want := 121 * time.Minute; probe.budget < want-time.Minute || probe.budget > want {
		t.Fatalf("spawn budget = %s, want about %s (1m + 90m + 30m)", probe.budget, want)
	}
	if extended != 121*time.Minute+time.Second {
		t.Fatalf("request deadline extended to %s, want just past the spawn's own", extended)
	}
}

type createConfigProbeWorkspace struct {
	ports.Workspace
	created []ports.WorkspaceConfig
}

func (w *createConfigProbeWorkspace) Create(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	w.created = append(w.created, cfg)
	return w.Workspace.Create(ctx, cfg)
}

func TestSpawnPassesCloneWorktreeIncludeToWorkspace(t *testing.T) {
	t.Parallel()

	for _, enabled := range []bool{false, true} {
		m, st, _, _ := newManager()
		probe := &createConfigProbeWorkspace{Workspace: m.workspace}
		m.workspace = probe
		project := st.projects["mer"]
		project.Config.CloneWorktreeInclude = enabled
		st.projects["mer"] = project
		_, _, _, _ = m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
		if len(probe.created) == 0 || probe.created[0].CloneWorktreeInclude != enabled {
			t.Fatalf("enabled=%v: workspace create configs = %+v", enabled, probe.created)
		}
	}
}

// A restore extends the request's budget by its Setup only when it recreates
// the worktree and so runs Setup; an ordinary restore that hangs keeps the
// request's ordinary budget.
func TestRESTRestoreExtendsItsBudgetOnlyWhenItRunsSetup(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		recreated bool
		want      time.Duration
	}{
		{"worktree kept", false, 0},
		{"worktree recreated", true, 91*time.Minute + time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _, _ := newManager()
			project := st.projects["mer"]
			project.Config.PostCreate = []string{"# tallpine-timeout: 90m\ntrue"}
			st.projects["mer"] = project
			workspace := t.TempDir()
			if tc.recreated {
				workspace = filepath.Join(workspace, "reclaimed")
			}
			seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: workspace, Branch: "tp/mer-1", AgentSessionID: "agent-x"})
			var extended time.Duration
			ctx := ports.WithSpawnRequestBudget(context.Background(), time.Minute, func(total time.Duration) { extended = total })
			_, _ = m.RestoreWithMode(ctx, "mer-1")
			if extended != tc.want {
				t.Fatalf("request budget extended to %s, want %s", extended, tc.want)
			}
		})
	}
}

func TestResolveClaudeFamilyAliasesAcceptedWhenCatalogListsConcreteModels(t *testing.T) {
	t.Parallel()

	efforts := []string{"low", "high"}
	m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{
		Models: []ports.AgentModelInfo{
			{ID: "claude-fable-5-1", Label: "Fable 5.1", Efforts: efforts},
			{ID: "claude-opus-5-5", Label: "Opus 5.5", Efforts: efforts},
			{ID: "claude-sonnet-5-5", Label: "Sonnet 5.5", Efforts: efforts},
			{ID: "claude-haiku-5-5", Label: "Haiku 5.5", Efforts: efforts},
		},
	}}}
	for _, alias := range domain.ClaudeFamilyAliases {
		resolved, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
			ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
			AgentConfig: ports.AgentConfig{Model: alias},
		}, domain.ProjectConfig{})
		if err != nil {
			t.Fatalf("alias %q error = %v", alias, err)
		}
		if resolved.Model != alias {
			t.Fatalf("alias %q resolved to %q", alias, resolved.Model)
		}
	}
}

func TestResolveClaudeFamilyAliasesAcceptedWhenFamilyModelsHaveNoEfforts(t *testing.T) {
	t.Parallel()

	m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{
		Models: []ports.AgentModelInfo{
			{ID: "claude-fable-5-1", Label: "Fable 5.1"},
			{ID: "claude-opus-5-5", Label: "Opus 5.5"},
			{ID: "claude-sonnet-5-5", Label: "Sonnet 5.5"},
			{ID: "claude-haiku-5-5", Label: "Haiku 5.5"},
		},
	}}}
	aliases := append(append([]string{}, domain.ClaudeFamilyAliases...), "sonnet[1m]")
	for _, alias := range aliases {
		resolved, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
			ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
			AgentConfig: ports.AgentConfig{Model: alias},
		}, domain.ProjectConfig{})
		if err != nil {
			t.Fatalf("alias %q error = %v", alias, err)
		}
		if resolved.Model != alias {
			t.Fatalf("alias %q resolved to %q", alias, resolved.Model)
		}
	}
}
