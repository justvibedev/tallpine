package sessionmanager

import (
	"log/slog"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestRuntimeEnvBaseSetsTestParallelismDefaults(t *testing.T) {
	t.Setenv("TALLPINE_TEST_PARALLELISM", "")
	t.Setenv("GOMAXPROCS", "")
	m := &Manager{dataDir: t.TempDir(), logger: slog.New(slog.DiscardHandler), executable: func() (string, error) { return "/opt/tallpine", nil }}
	env, _ := m.runtimeEnvBase("mer-1", "mer", "", nil, "", false, domain.AgentHarness(""))
	if env["TALLPINE_TEST_PARALLELISM"] == "" || env["GOMAXPROCS"] != env["TALLPINE_TEST_PARALLELISM"] {
		t.Fatalf("defaults missing: %q / %q", env["TALLPINE_TEST_PARALLELISM"], env["GOMAXPROCS"])
	}
	env, _ = m.runtimeEnvBase("mer-1", "mer", "", map[string]string{"TALLPINE_TEST_PARALLELISM": "11"}, "", false, domain.AgentHarness(""))
	if env["TALLPINE_TEST_PARALLELISM"] != "11" || env["GOMAXPROCS"] != "11" {
		t.Fatalf("project value lost: %v", env)
	}
}

func TestSystemPromptsCarryTestParallelismSection(t *testing.T) {
	for _, role := range []sessionPromptRole{sessionPromptRoleWorker, sessionPromptRoleOrchestrator} {
		got := buildSystemPromptText(systemPromptConfig{Role: role, Project: promptProject{ID: "mer", Name: "Mercury"}})
		for _, want := range []string{"## Test Parallelism", "go test -p $TALLPINE_TEST_PARALLELISM", "pytest -n $TALLPINE_TEST_PARALLELISM", "you already have that command running", "one waiter per run", "Stop a superseded run", "under `$TMPDIR`", "is not idling", "Only a run you started in this worktree", "If your harness notifies you"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s prompt missing %q", role, want)
			}
		}
	}
}
