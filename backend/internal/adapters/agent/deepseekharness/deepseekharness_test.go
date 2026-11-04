package deepseekharness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// fakeDSH puts an executable named dsh on PATH so binary resolution succeeds.
func fakeDSH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "dsh")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test stub
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

func patchContent(t *testing.T, argv []string) string {
	t.Helper()
	for i, a := range argv {
		if a == "--patch" && i+1 < len(argv) {
			data, err := os.ReadFile(argv[i+1])
			if err != nil {
				t.Fatal(err)
			}
			return string(data)
		}
	}
	t.Fatalf("no --patch overlay in %v", argv)
	return ""
}

func TestLaunchDisablesSessionLogUpload(t *testing.T) {
	bin := fakeDSH(t)
	dataDir := t.TempDir()
	cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt:  "fix the bug",
		DataDir: dataDir, // production shape: manager sets DataDir, never Env["TALLPINE_DATA_DIR"]
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd[0] != bin || cmd[1] != "--profile" || cmd[2] != "headless" {
		t.Fatalf("argv = %v", cmd)
	}
	patch := patchContent(t, cmd)
	if !strings.Contains(patch, "id: session-log-deepseek") || !strings.Contains(patch, "enabled: false") {
		t.Fatalf("overlay does not disable the session-log plugin:\n%s", patch)
	}
	if !strings.HasPrefix(cmd[4], dataDir) {
		t.Fatalf("overlay %q is not under the Tallpine data dir %q", cmd[4], dataDir)
	}
	if cmd[len(cmd)-1] != "fix the bug" {
		t.Fatalf("task is not the last argument: %v", cmd)
	}
}

func TestLaunchWithoutDataDirFailsClosed(t *testing.T) {
	fakeDSH(t)
	if _, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{Prompt: "x"}); err == nil {
		t.Fatal("launch without a data dir succeeded; the overlay would land outside Tallpine state")
	}
	if _, err := PrivacyArgs(""); err == nil {
		t.Fatal("PrivacyArgs accepted an empty data dir")
	}
}

func TestRestoreKeepsPrivacyOverlayAndSessionID(t *testing.T) {
	fakeDSH(t)
	cmd, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: t.TempDir(),
		Prompt:  "continue",
		Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-1"}},
	})
	if err != nil || !ok {
		t.Fatalf("restore ok=%v err=%v", ok, err)
	}
	if !strings.Contains(patchContent(t, cmd), "enabled: false") {
		t.Fatal("restore launch lost the privacy overlay")
	}
	if !strings.Contains(strings.Join(cmd, " "), "--session-id native-1") {
		t.Fatalf("restore argv = %v", cmd)
	}
	// No captured native id: fall back to a fresh launch.
	if _, ok, _ := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{DataDir: t.TempDir()}); ok {
		t.Fatal("restore claimed a session without a native id")
	}
}

// The role prompt leads the task, and nothing dynamic precedes it: DeepSeek's
// prefix cache keys on the start of the request.
func TestTerminalTaskPutsRolePromptFirstAndStable(t *testing.T) {
	fakeDSH(t)
	launch := func(prompt string) string {
		cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{
			SystemPrompt: "You are a Tallpine worker.", Prompt: prompt, DataDir: t.TempDir(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return cmd[len(cmd)-1]
	}
	a, b := launch("task one"), launch("task two")
	const prefix = "Standing instructions from Tallpine."
	if !strings.HasPrefix(a, prefix) || !strings.Contains(a, "You are a Tallpine worker.") {
		t.Fatalf("role prompt is not delivered first:\n%s", a)
	}
	idx := strings.Index(a, "Task:\n")
	if idx < 0 || a[:idx] != b[:strings.Index(b, "Task:\n")] {
		t.Fatal("prefix before the task differs between launches; it must be byte-stable")
	}
}

func TestTerminalRefusesStdinSentinelAndEmptyTask(t *testing.T) {
	fakeDSH(t)
	for _, prompt := range []string{"", "  ", "-"} {
		if _, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{Prompt: prompt}); err == nil {
			t.Fatalf("prompt %q accepted; dsh would block reading stdin", prompt)
		}
	}
}

func TestAugmentRuntimeEnvDisablesTelemetryUnlessChosen(t *testing.T) {
	env := map[string]string{}
	New().AugmentRuntimeEnv(env, "")
	if env["DSH_TELEMETRY_MODE"] != "DISABLED" {
		t.Fatalf("env = %v", env)
	}
	env = map[string]string{"DSH_TELEMETRY_MODE": "FEEDBACK_ONLY"}
	New().AugmentRuntimeEnv(env, "")
	if env["DSH_TELEMETRY_MODE"] != "FEEDBACK_ONLY" {
		t.Fatal("an explicit project choice was overwritten")
	}
}

func TestAuthStatusReadsOnlyTheDeepSeekRef(t *testing.T) {
	fakeDSH(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	cases := map[string]struct {
		body string
		want ports.AgentAuthStatus
	}{
		"deepseek ref":     {"version: 1\nrefs:\n  DEEPSEEK_API_KEY: sk-x\n", ports.AgentAuthStatusAuthorized},
		"nested payload":   {"version: 1\nrefs:\n  DEEPSEEK_API_KEY:\n    payload:\n      secret: sk-x\n", ports.AgentAuthStatusAuthorized},
		"other provider":   {"version: 1\nrefs:\n  OPENCODE_GO_KEY: sk-x\n", ports.AgentAuthStatusUnknown},
		"empty secret":     {"version: 1\nrefs:\n  DEEPSEEK_API_KEY: \"\"\n", ports.AgentAuthStatusUnknown},
		"pre-release flat": {"DEEPSEEK_API_KEY: sk-x\n", ports.AgentAuthStatusUnknown},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "creds.yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("DSH_CREDENTIALS_PATH", path)
			got, err := New().AuthStatus(context.Background())
			if err != nil || got != tc.want {
				t.Fatalf("status = %q err = %v, want %q", got, err, tc.want)
			}
		})
	}
}

// allPatches reads every --patch overlay in argv.
func allPatches(t *testing.T, argv []string) string {
	t.Helper()
	var out strings.Builder
	for i, a := range argv {
		if a == "--patch" && i+1 < len(argv) {
			data, err := os.ReadFile(argv[i+1])
			if err != nil {
				t.Fatal(err)
			}
			out.Write(data)
		}
	}
	return out.String()
}

// dsh's headless profile composes the credentials plugin disabled, so a saved
// key is never read; the terminal launch (and its restore) must turn it back on.
func TestTerminalLaunchReenablesCredentials(t *testing.T) {
	fakeDSH(t)
	launch, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{Prompt: "x", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	restore, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: t.TempDir(), Prompt: "x",
		Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "n"}},
	})
	if err != nil || !ok {
		t.Fatalf("restore ok=%v err=%v", ok, err)
	}
	for name, argv := range map[string][]string{"launch": launch, "restore": restore} {
		patches := allPatches(t, argv)
		if !strings.Contains(patches, "id: credentials\n  disabled: false") {
			t.Fatalf("%s does not re-enable credentials:\n%s", name, patches)
		}
		if !strings.Contains(patches, "enabled: false") {
			t.Fatalf("%s lost the session-log privacy overlay:\n%s", name, patches)
		}
	}
}

// Chat's acp profile already has credentials on; it only gets the privacy overlay.
func TestACPLaunchLeavesCredentialsToTheProfile(t *testing.T) {
	args, err := ACPArgs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(allPatches(t, args), "id: credentials") {
		t.Fatal("the acp launch overrides credentials; its profile already enables them")
	}
}
