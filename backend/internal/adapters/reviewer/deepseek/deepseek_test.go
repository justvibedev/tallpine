package deepseek

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// fakeDSH puts a dsh executable on PATH so the real worker plugin resolves it.
func fakeDSH(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dsh"), []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test stub
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func patchFiles(t *testing.T, argv []string) []string {
	t.Helper()
	var out []string
	for i, a := range argv {
		if a == "--patch" && i+1 < len(argv) {
			out = append(out, argv[i+1])
		}
	}
	return out
}

func TestReviewCommandIsReadOnlyHeadlessLaunch(t *testing.T) {
	fakeDSH(t)
	dataDir := t.TempDir()
	got, err := New().ReviewCommand(context.Background(), ports.ReviewInvocation{
		ReviewerID: "review-w1", WorkspacePath: "/ws/w1", DataDir: dataDir, Prompt: "review it",
	})
	if err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	if !slices.Contains(got.Argv, "headless") || got.Argv[len(got.Argv)-1] != "review it" {
		t.Fatalf("argv = %#v, want headless profile with the task last", got.Argv)
	}
	patches := patchFiles(t, got.Argv)
	last := patches[len(patches)-1]
	if filepath.Base(last) != "readonly.patch.yml" {
		t.Fatalf("last patch = %s, want the read-only pin applied after the other overlays", last)
	}
	body, err := os.ReadFile(last) //nolint:gosec // test temp path
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "id: sandbox-policy") || !strings.Contains(string(body), "mode: read-only") {
		t.Fatalf("read-only patch = %q", body)
	}
	if got.Env["DSH_PERMISSION_MODE"] != "read-only" {
		t.Fatalf("env = %#v, want DSH_PERMISSION_MODE=read-only", got.Env)
	}
}

func TestReviewerIsTerminalOnly(t *testing.T) {
	var r any = New()
	if _, ok := r.(ports.ReviewerChatProfile); ok {
		t.Fatal("DeepSeek reviewer must not opt into chat: the ACP driver has no read-only launch")
	}
}

func TestReviewCommandPinsSessionPermissionMode(t *testing.T) {
	fakeDSH(t)
	got, err := New().ReviewCommand(context.Background(), ports.ReviewInvocation{DataDir: t.TempDir(), Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(got.Argv, "--permission-mode")
	if i < 0 || got.Argv[i+1] != "read-only" {
		t.Fatalf("argv = %#v, want --permission-mode read-only", got.Argv)
	}
}

func TestReviewRestoreCommandIsReadOnly(t *testing.T) {
	fakeDSH(t)
	got, ok, err := New().ReviewRestoreCommand(context.Background(), ports.ReviewInvocation{
		AgentSessionID: "ds-1", DataDir: t.TempDir(), RunID: "run-1", Prompt: "again",
	})
	if err != nil || !ok {
		t.Fatalf("restore ok=%v err=%v", ok, err)
	}
	if !slices.Contains(got.Argv, "--session-id") || got.Argv[len(got.Argv)-1] != "again" {
		t.Fatalf("argv = %#v", got.Argv)
	}
	patches := patchFiles(t, got.Argv)
	if filepath.Base(patches[len(patches)-1]) != "readonly.patch.yml" || got.Env["DSH_PERMISSION_MODE"] != "read-only" {
		t.Fatalf("restore not read-only: argv=%#v env=%#v", got.Argv, got.Env)
	}
	if _, ok, _ := New().ReviewRestoreCommand(context.Background(), ports.ReviewInvocation{DataDir: t.TempDir()}); ok {
		t.Fatal("restore without a native session id must report ok=false")
	}
}

// The review submission path is the Codex reviewer's: the task prompt is
// delivered unchanged, in the launch argv and for later passes.
func TestReviewSubmissionPathMatchesCodex(t *testing.T) {
	fakeDSH(t)
	r := New()
	if r.Harness() != domain.ReviewerDeepSeek {
		t.Fatal("harness identity")
	}
	msg, err := r.ReviewMessage(context.Background(), ports.ReviewInvocation{Prompt: "next review"})
	if err != nil || msg != "next review" {
		t.Fatalf("message = %q err=%v", msg, err)
	}
	cmd, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{DataDir: t.TempDir(), Prompt: "submit via tallpine review submit", SystemPrompt: "review only"})
	if err != nil {
		t.Fatal(err)
	}
	last := cmd.Argv[len(cmd.Argv)-1]
	if !strings.Contains(last, "submit via tallpine review submit") || !strings.Contains(last, "review only") {
		t.Fatalf("task = %q, want prompt and system prompt delivered", last)
	}
	spec, err := r.ReviewCancel(context.Background())
	if err != nil || spec.Mode != ports.ReviewCancelInterrupt || spec.Interrupts != 2 {
		t.Fatalf("cancel = %+v err=%v", spec, err)
	}
	if r.ReviewProcessReusable() {
		t.Fatal("one-shot headless reviewer must not be reused")
	}
}
