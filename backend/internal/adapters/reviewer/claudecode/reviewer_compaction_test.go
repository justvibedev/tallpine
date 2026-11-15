package claudecode

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// The compaction window the review engine puts in a reviewer's config reaches
// Claude Code's --settings through the real adapter (B734).
func TestReviewCommandCarriesTheCompactionWindow(t *testing.T) {
	stubClaudeOnPath(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	spec, err := New().ReviewCommand(context.Background(), ports.ReviewInvocation{
		ReviewerID: "review-w1", WorkspacePath: "/ws/w1", Prompt: "review it",
		Config: domain.AgentConfig{Model: "opus", Launch: domain.AgentLaunchOptions{AutoCompactWindow: "40%", ModelContextWindow: 1_000_000}},
	})
	if err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	if settings := flagValue(spec.Argv, "--settings"); !strings.Contains(settings, `"autoCompactWindow":400000`) {
		t.Fatalf("--settings %q: want autoCompactWindow 400000", settings)
	}
}
