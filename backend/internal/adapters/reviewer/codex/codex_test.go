package codex

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type captureAgent struct {
	got        ports.LaunchConfig
	gotRestore ports.RestoreConfig
}

func (a *captureAgent) GetConfigSpec(context.Context) (ports.ConfigSpec, error) {
	return ports.ConfigSpec{}, nil
}
func (a *captureAgent) GetLaunchCommand(_ context.Context, cfg ports.LaunchConfig) ([]string, error) {
	a.got = cfg
	return []string{"agent", "--", cfg.Prompt}, nil
}
func (a *captureAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryInCommand, nil
}
func (a *captureAgent) GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error { return nil }
func (a *captureAgent) GetRestoreCommand(_ context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	a.gotRestore = cfg
	id := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]
	if id == "" {
		return nil, false, nil
	}
	return []string{"agent", "resume", id}, true, nil
}
func (a *captureAgent) SessionInfo(context.Context, ports.SessionRef) (ports.SessionInfo, bool, error) {
	return ports.SessionInfo{}, false, nil
}

func TestReviewCommandUsesReadOnlySandbox(t *testing.T) {
	t.Setenv("TALLPINE_PORT", "3103")
	t.Setenv("TALLPINE_DATA_DIR", "/wrong/data")
	t.Setenv("TALLPINE_RUN_FILE", "/wrong/running.json")
	agent := &captureAgent{}
	r := &Reviewer{agent: agent}

	got, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{
		ReviewerID:    "review-w1",
		WorkspacePath: "/ws/w1",
		DataDir:       "/tmp/tallpine data",
		RunFilePath:   "/tmp/tallpine data/running.json",
		Prompt:        "review it",
		SystemPrompt:  "review only",
	})
	if err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}

	want := []string{
		"agent",
		"--sandbox", "read-only",
		"-c", `shell_environment_policy.set.TALLPINE_PORT="3103"`,
		"-c", `shell_environment_policy.set.TALLPINE_DATA_DIR="/tmp/tallpine data"`,
		"-c", `shell_environment_policy.set.TALLPINE_RUN_FILE="/tmp/tallpine data/running.json"`,
		"--", "review it",
	}
	if !slices.Equal(got.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", got.Argv, want)
	}
	if agent.got.Permissions != ports.PermissionModeAuto {
		t.Fatalf("permissions = %q, want auto", agent.got.Permissions)
	}
	if agent.got.SystemPrompt != "review only" {
		t.Fatalf("system prompt = %q", agent.got.SystemPrompt)
	}
}

func TestReviewCommandEmitsConfiguredCodexEffort(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	got, err := New().ReviewCommand(context.Background(), ports.ReviewInvocation{
		Config: ports.AgentConfig{Effort: "high"},
		Prompt: "review it",
	})
	if err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	if !slices.Contains(got.Argv, "model_reasoning_effort='high'") {
		t.Fatalf("review command %#v missing configured Codex effort", got.Argv)
	}
}

func TestReviewMessageReturnsTaskPrompt(t *testing.T) {
	got, err := (&Reviewer{}).ReviewMessage(context.Background(), ports.ReviewInvocation{Prompt: "next review"})
	if err != nil {
		t.Fatalf("ReviewMessage: %v", err)
	}
	if got != "next review" {
		t.Fatalf("message = %q", got)
	}
}

func TestReviewCancelSendsSingleEscapeInput(t *testing.T) {
	spec, err := (&Reviewer{}).ReviewCancel(context.Background())
	if err != nil {
		t.Fatalf("ReviewCancel: %v", err)
	}
	if spec.Mode != ports.ReviewCancelInput {
		t.Fatalf("cancel mode = %q, want %q", spec.Mode, ports.ReviewCancelInput)
	}
	if spec.Input != "\x1b" || len(spec.Inputs) != 0 {
		t.Fatalf("input = %q inputs = %#v, want single escape input", spec.Input, spec.Inputs)
	}
}

func TestReviewCommandUsesHiddenSystemPromptFile(t *testing.T) {
	agent := &captureAgent{}
	r := &Reviewer{agent: agent}

	if _, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{
		Prompt:           "Start the Tallpine review task.",
		SystemPromptFile: "/tallpine/prompts/reviewer/system.md",
	}); err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	if agent.got.Prompt != "Start the Tallpine review task." || agent.got.SystemPrompt != "" || agent.got.SystemPromptFile != "/tallpine/prompts/reviewer/system.md" {
		t.Fatalf("launch config = %+v", agent.got)
	}
}

func TestReviewRestoreCommandUsesNativeSessionIDAndReadOnlySandbox(t *testing.T) {
	t.Setenv("TALLPINE_PORT", "3103")
	t.Setenv("TALLPINE_DATA_DIR", "/wrong/data")
	t.Setenv("TALLPINE_RUN_FILE", "/wrong/running.json")
	agent := &captureAgent{}
	r := &Reviewer{agent: agent}

	got, ok, err := r.ReviewRestoreCommand(context.Background(), ports.ReviewInvocation{
		ReviewerID:       "review-w1",
		AgentSessionID:   "codex-native-1",
		WorkspacePath:    "/ws/w1",
		DataDir:          "/tmp/tallpine data",
		RunFilePath:      "/tmp/tallpine data/running.json",
		SystemPromptFile: "/tallpine/prompts/reviewer/system.md",
	})
	if err != nil {
		t.Fatalf("ReviewRestoreCommand: %v", err)
	}
	if !ok {
		t.Fatal("ReviewRestoreCommand ok = false, want true")
	}
	if !got.NativeResumed {
		t.Fatal("ReviewRestoreCommand did not report native resume")
	}
	want := []string{
		"agent", "resume",
		"--sandbox", "read-only",
		"-c", `shell_environment_policy.set.TALLPINE_PORT="3103"`,
		"-c", `shell_environment_policy.set.TALLPINE_DATA_DIR="/tmp/tallpine data"`,
		"-c", `shell_environment_policy.set.TALLPINE_RUN_FILE="/tmp/tallpine data/running.json"`,
		"codex-native-1",
	}
	if !slices.Equal(got.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", got.Argv, want)
	}
	if agent.gotRestore.Session.ID != "review-w1" || agent.gotRestore.Session.WorkspacePath != "/ws/w1" {
		t.Fatalf("restore session = %+v", agent.gotRestore.Session)
	}
	if agent.gotRestore.Session.Metadata[ports.MetadataKeyAgentSessionID] != "codex-native-1" {
		t.Fatalf("restore metadata = %#v", agent.gotRestore.Session.Metadata)
	}
	if agent.gotRestore.Permissions != ports.PermissionModeAuto {
		t.Fatalf("restore permissions = %q, want auto", agent.gotRestore.Permissions)
	}
}

func TestReviewCommandAndRestoreCarryThePolicyDecision(t *testing.T) {
	agent := &captureAgent{}
	r := &Reviewer{agent: agent}
	decision := policytest.Cap(t, permissioncap.AcceptEdits)
	if _, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{Policy: decision, Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if !permissioncap.Of(agent.got.Policy).ForbidsBypass() {
		t.Fatal("the terminal reviewer's launch dropped the policy decision")
	}
	if _, _, err := r.ReviewRestoreCommand(context.Background(), ports.ReviewInvocation{Policy: decision, AgentSessionID: "native-1", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if !permissioncap.Of(agent.gotRestore.Policy).ForbidsBypass() {
		t.Fatal("the terminal reviewer's restore dropped the policy decision")
	}
}

// With the real Codex adapter underneath, a capped reviewer launch is lowered:
// `auto` becomes accept-edits, so the built-in approvals reviewer is not used.
func TestCappedCodexReviewerLaunchIsLowered(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := New()
	launch := func(d agentpolicy.Decision) []string {
		got, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{Policy: d, Prompt: "p", WorkspacePath: t.TempDir(), DataDir: t.TempDir()})
		if err != nil {
			t.Fatalf("real codex adapter launch failed: %v", err)
		}
		return got.Argv
	}
	free := strings.Join(launch(agentpolicy.Decision{}), " ")
	capped := strings.Join(launch(policytest.Cap(t, permissioncap.AcceptEdits)), " ")
	if !strings.Contains(free, "auto_review") {
		t.Fatalf("uncapped reviewer no longer uses the auto approvals reviewer: %s", free)
	}
	if strings.Contains(capped, "auto_review") {
		t.Fatalf("a capped reviewer must not run in auto: %s", capped)
	}
}
