package deepseekharness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestHooksJSONRunsTallpineForEveryEvent(t *testing.T) {
	var cfg struct {
		Hooks map[string][]struct {
			Hooks []struct{ Type, Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(hooksJSON()), &cfg); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"SessionStart": "session-start", "UserPromptSubmit": "user-prompt-submit",
		"PreToolUse": "pre-tool-use", "PostToolUse": "post-tool-use", "Stop": "stop",
		"SubagentStart": "subagent-start", "SubagentStop": "subagent-stop",
	}
	if len(cfg.Hooks) != len(want) {
		t.Fatalf("hooks = %v, want %d events", cfg.Hooks, len(want))
	}
	for native, event := range want {
		g := cfg.Hooks[native]
		if len(g) != 1 || len(g[0].Hooks) != 1 || g[0].Hooks[0].Type != "command" ||
			g[0].Hooks[0].Command != "tallpine hooks deepseek-harness "+event {
			t.Errorf("%s = %+v, want command for %s", native, g, event)
		}
	}
}

// stubHooksInstall replaces the layout step with one that lays down the entry file.
func stubHooksInstall(t *testing.T, calls *int) {
	t.Helper()
	resetHooksInstallMemo()
	old := installHooksPlugin
	t.Cleanup(func() { installHooksPlugin = old })
	installHooksPlugin = func(_ context.Context, dir, _ string) error {
		*calls++
		entry := filepath.Join(dir, "node_modules", "@deepseek-ai", "dsh-hooks-claude-code", "lib", "index.js")
		if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
			return err
		}
		return os.WriteFile(entry, []byte("export {}\n"), 0o600)
	}
}

func TestTerminalLaunchInstallsHooksPluginOnceAndNamesItByPath(t *testing.T) {
	fakeDSH(t)
	dataDir := t.TempDir()
	calls := 0
	stubHooksInstall(t, &calls)
	for i := 0; i < 2; i++ {
		argv, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{Prompt: "x", DataDir: dataDir})
		if err != nil {
			t.Fatal(err)
		}
		if p := allPatches(t, argv); !strings.Contains(p, hooksPluginEntry(dataDir)) || strings.Contains(p, "'"+hooksPluginName+"'") {
			t.Fatalf("overlay must name the installed entry file, not the bare package:\n%s", p)
		}
	}
	if calls != 1 {
		t.Fatalf("install ran %d times, want 1", calls)
	}
}

func TestTerminalLaunchSkipsHooksOverlayWhenInstallFails(t *testing.T) {
	fakeDSH(t)
	old := installHooksPlugin
	t.Cleanup(func() { installHooksPlugin = old })
	resetHooksInstallMemo()
	installHooksPlugin = func(context.Context, string, string) error { return errors.New("offline") }
	argv, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{Prompt: "x", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(allPatches(t, argv), "hooks-claude-code") {
		t.Fatal("overlay mounted a plugin that is not installed")
	}
}

func TestTerminalLaunchFullAccessOnlyForBypass(t *testing.T) {
	fakeDSH(t)
	calls := 0
	stubHooksInstall(t, &calls)
	launch := func(mode ports.PermissionMode, d agentpolicy.Decision) string {
		argv, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{Prompt: "x", DataDir: t.TempDir(), Permissions: mode, Policy: d})
		if err != nil {
			t.Fatal(err)
		}
		return allPatches(t, argv)
	}
	if p := launch(ports.PermissionModeBypassPermissions, agentpolicy.Decision{}); !strings.Contains(p, "mode: danger-full-access") || !strings.Contains(p, "policy: never") {
		t.Fatalf("bypass must lift the sandbox so git reaches the shared .git:\n%s", p)
	}
	if p := launch(ports.PermissionModeAuto, agentpolicy.Decision{}); strings.Contains(p, "danger-full-access") {
		t.Fatalf("non-bypass launch must keep the sandbox:\n%s", p)
	}
	if p := launch(ports.PermissionModeBypassPermissions, policytest.Cap(t, permissioncap.AcceptEdits)); strings.Contains(p, "danger-full-access") {
		t.Fatalf("a cap that forbids bypass must keep the sandbox:\n%s", p)
	}
}

func TestTerminalLaunchAndRestoreMountHooksOverlay(t *testing.T) {
	fakeDSH(t)
	dataDir := t.TempDir()
	stubHooksInstall(t, new(int))
	launch, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{Prompt: "x", DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	restore, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: dataDir, Prompt: "x",
		Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "n"}},
	})
	if err != nil || !ok {
		t.Fatalf("restore ok=%v err=%v", ok, err)
	}
	for name, argv := range map[string][]string{"launch": launch, "restore": restore} {
		patches := allPatches(t, argv)
		for _, want := range []string{"insert:", "id: hooks-claude-code", hooksPluginEntry(dataDir), dataDir} {
			if !strings.Contains(patches, want) {
				t.Errorf("%s overlay missing %q:\n%s", name, want, patches)
			}
		}
	}
	if _, err := os.Stat(dataDir + "/dsh/hooks.json"); err != nil {
		t.Fatal(err)
	}
}

// Chat (ACP) delivers events over the protocol; it must not mount command hooks.
func TestACPLaunchHasNoHooksOverlay(t *testing.T) {
	args, err := ACPArgs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(allPatches(t, args), "hooks-claude-code") {
		t.Fatal("ACP launch mounts the hooks plugin")
	}
}

func TestDeriveActivityStatePerEvent(t *testing.T) {
	tests := []struct {
		event string
		want  domain.ActivityState
		ok    bool
	}{
		{"user-prompt-submit", domain.ActivityActive, true},
		{"pre-tool-use", domain.ActivityActive, true},
		{"post-tool-use", domain.ActivityActive, true},
		{"stop", domain.ActivityIdle, true},
		{"session-start", "", false},
		{"subagent-start", "", false},
		{"subagent-stop", "", false},
	}
	for _, tt := range tests {
		got, ok := DeriveActivityState(tt.event, nil)
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s = (%q,%v), want (%q,%v)", tt.event, got, ok, tt.want, tt.ok)
		}
	}
}

func TestInstallHooksPluginIsOfflineAndSharesDshPackages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	root := t.TempDir()
	scope := filepath.Join(root, "pkg", "node_modules", "@deepseek-ai")
	if err := os.MkdirAll(filepath.Join(scope, "dsh-llm"), 0o700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "pkg", "dsh")
	if err := os.WriteFile(bin, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "hp")
	if err := installHooksPlugin(context.Background(), dir, bin); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"dsh-hooks-claude-code/lib/index.js", "dsh-hooks-claude-code/package.json", "dsh-hook-protocol/lib/index.js"} {
		if st, err := os.Stat(filepath.Join(dir, "node_modules", "@deepseek-ai", filepath.FromSlash(f))); err != nil || st.Size() == 0 {
			t.Errorf("%s not laid out from the embedded assets: %v", f, err)
		}
	}
	if _, err := os.Readlink(filepath.Join(dir, "node_modules", "@deepseek-ai", "dsh-llm")); err != nil {
		t.Errorf("dsh-llm must be a symlink into dsh's own tree: %v", err)
	}
}

var testIdentity = "scopeA\n1"

func resetHooksInstallMemo() {
	dshIdentity = func(string) (string, error) { return testIdentity, nil }
	hooksInstallMu.Lock()
	hooksInstallFailed = map[string]bool{}
	hooksInstallMu.Unlock()
}

func TestEnsureHooksPluginRebuildsWhenDshChanges(t *testing.T) {
	var calls int
	stubHooksInstall(t, &calls)
	oldID := dshIdentity
	t.Cleanup(func() { dshIdentity = oldID; resetHooksInstallMemo() })
	data := t.TempDir()
	EnsureHooksPlugin(context.Background(), data, "dsh")
	EnsureHooksPlugin(context.Background(), data, "dsh")
	if calls != 1 {
		t.Fatalf("same dsh: %d installs, want 1", calls)
	}
	dshIdentity = func(string) (string, error) { return "scopeB\n2", nil }
	EnsureHooksPlugin(context.Background(), data, "dsh")
	if calls != 2 {
		t.Fatalf("moved/upgraded dsh must rebuild the layout, installs=%d", calls)
	}
}

func TestEnsureHooksPluginRemembersFailureAndSerializes(t *testing.T) {
	resetHooksInstallMemo()
	old := installHooksPlugin
	t.Cleanup(func() { installHooksPlugin = old; resetHooksInstallMemo() })
	var calls atomic.Int32
	installHooksPlugin = func(_ context.Context, dir, _ string) error {
		calls.Add(1)
		_ = os.MkdirAll(dir, 0o700)
		return errors.New("boom")
	}
	data := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); EnsureHooksPlugin(context.Background(), data, "dsh") }()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("install ran %d times, want 1 (serialized + failure memo)", n)
	}
	if _, err := os.Stat(filepath.Join(data, "dsh", "hooks-plugin.tmp")); err == nil {
		t.Fatal("partial install left behind")
	}
}
