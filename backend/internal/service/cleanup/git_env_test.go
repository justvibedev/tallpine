package cleanup

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestCleanupGitIgnoresInheritedGitDir(t *testing.T) {
	repo := testenv.GitRepo(t)
	testenv.IgnoredRepoEnv(t)
	ctx := context.Background()
	if out, err := runGitIn(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD"); err != nil || out != "main" {
		t.Fatalf("runGitIn = %q, %v", out, err)
	}
	if out, err := RunGit(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD"); err != nil || out != "main" {
		t.Fatalf("RunGit = %q, %v", out, err)
	}
	if out, err := gitOutput(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD"); err != nil || string(out) != "main\n" {
		t.Fatalf("gitOutput = %q, %v", out, err)
	}
}
