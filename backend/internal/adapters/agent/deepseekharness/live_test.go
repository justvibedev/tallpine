package deepseekharness

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestLiveHeadlessAnswersWithASavedKey runs the exact terminal argv against the
// installed dsh and expects an answer, which proves the credentials overlay: the
// headless profile ships with credentials disabled. It costs a real (tiny) model
// call and needs a key saved through dsh's web Models page, so it is opt-in:
//
//	TALLPINE_LIVE_DEEPSEEK=1 go test ./internal/adapters/agent/deepseekharness -run Live -v
func TestLiveHeadlessAnswersWithASavedKey(t *testing.T) {
	if os.Getenv("TALLPINE_LIVE_DEEPSEEK") != "1" {
		t.Skip("set TALLPINE_LIVE_DEEPSEEK=1 to run dsh against the model API")
	}
	if status, _ := New().AuthStatus(context.Background()); status != ports.AgentAuthStatusAuthorized {
		t.Skip("no DeepSeek key saved in dsh's credential store or DEEPSEEK_API_KEY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := map[string]string{}
	New().AugmentRuntimeEnv(env, "")
	argv, err := New().GetLaunchCommand(ctx, ports.LaunchConfig{Prompt: "Reply with exactly: OK", DataDir: t.TempDir(), Env: env})
	if err != nil {
		t.Fatal(err)
	}
	env2 := os.Environ()
	for k, v := range env {
		env2 = append(env2, k+"="+v)
	}
	// Own process group, killed on exit: CommandContext kills only the direct
	// child and would orphan dsh's node processes (B151).
	out, _, err := combinedOutputGrouped(ctx, argv[0], argv[1:], env2)
	t.Logf("dsh output: %s", strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("headless run failed: %v", err)
	}
	if strings.Contains(string(out), "MISSING_CREDENTIAL") || !strings.Contains(string(out), "OK") {
		t.Fatalf("headless did not answer with the saved key")
	}
}
