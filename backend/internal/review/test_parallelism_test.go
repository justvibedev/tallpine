package review

import (
	"context"
	"strings"
	"testing"
)

func TestReviewerRuntimeEnvSetsTestParallelism(t *testing.T) {
	t.Setenv("TALLPINE_TEST_PARALLELISM", "")
	t.Setenv("GOMAXPROCS", "")
	l := &agentLauncher{dataDir: t.TempDir(), executable: func() (string, error) { return "/opt/tallpine", nil }}
	env := l.runtimeEnv(context.Background(), LaunchSpec{WorkerID: "mer-1"}, nil, nil)
	if env["TALLPINE_TEST_PARALLELISM"] == "" || env["GOMAXPROCS"] != env["TALLPINE_TEST_PARALLELISM"] {
		t.Fatalf("defaults missing: %q / %q", env["TALLPINE_TEST_PARALLELISM"], env["GOMAXPROCS"])
	}
	env = l.runtimeEnv(context.Background(), LaunchSpec{WorkerID: "mer-1", ProjectEnv: map[string]string{"TALLPINE_TEST_PARALLELISM": "9"}}, nil, nil)
	if env["TALLPINE_TEST_PARALLELISM"] != "9" {
		t.Fatalf("project value lost: %v", env)
	}
}

func TestReviewerPromptPointsAtTestParallelism(t *testing.T) {
	if !strings.Contains(reviewSystemPrompt(), "$TALLPINE_TEST_PARALLELISM") {
		t.Fatal("reviewer prompt lacks the parallelism line")
	}
}
