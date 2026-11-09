//go:build darwin

package previewserver

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// TestManagerRunsPreviewInsideSessionFence starts a real server through
// sandbox-exec and has it try to read the install token, the exploit in B589.
func TestManagerRunsPreviewInsideSessionFence(t *testing.T) {
	if !statefence.Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	runFile := filepath.Join(root, "running.json")
	token := daemontoken.Path(runFile)
	if err := os.WriteFile(token, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := helperConfiguration("web", TargetApp)
	cfg.Env["TALLPINE_PREVIEW_FENCE_PROBE"] = token
	workspace := writeLaunchFile(t, []Configuration{cfg})
	fence := Fence{
		Decision:  policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}}),
		RunFile:   runFile,
		DataDir:   data,
		SessionID: "tp-1",
	}
	manager := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(manager.Close)

	status, err := manager.Start(context.Background(), "tp-1", workspace, "", nil, fence)
	if err != nil {
		t.Fatalf("Start: %v\nstatus=%+v", err, status)
	}
	logs := strings.Join(manager.Status("tp-1").Logs, "\n")
	if !strings.Contains(logs, "fence-probe denied") {
		t.Fatalf("preview server read the install token; logs:\n%s", logs)
	}
	stopped, err := manager.Stop(context.Background(), "tp-1")
	if err != nil || stopped.State != StateStopped {
		t.Fatalf("Stop = %+v, %v; a fenced server must stop like any other", stopped, err)
	}
}

func TestFenceResolveFailsClosedWithoutRunFile(t *testing.T) {
	if !statefence.Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	decision := policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}})
	if _, _, err := (Fence{Decision: decision, DataDir: t.TempDir(), SessionID: "tp-1"}).resolve(t.TempDir()); err == nil {
		t.Fatal("a fence with no run file must fail the start")
	}
}

func TestFenceResolveMovesTMPDIRIntoSessionScratch(t *testing.T) {
	if !statefence.Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	decision := policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}})
	_, env, err := (Fence{Decision: decision, RunFile: filepath.Join(root, "running.json"), DataDir: data, SessionID: "tp-1"}).resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := "TMPDIR=" + filepath.Join(data, "tmp", "tp-1")
	if !slices.Contains(env, want) {
		t.Fatalf("env = %v, want %s", env, want)
	}
}
