package project

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// A session environment that carries GIT_DIR must not redirect the project
// service's git calls away from the folder being registered.
func TestProjectGitIgnoresInheritedGitDir(t *testing.T) {
	repo := testenv.GitRepo(t)
	testenv.IgnoredRepoEnv(t)
	if !isGitRepo(repo) {
		t.Fatal("isGitRepo = false with GIT_DIR set")
	}
	if got := resolveGitOriginURL(repo); got != "https://example.com/o/r.git" {
		t.Fatalf("resolveGitOriginURL = %q", got)
	}
	if out, err := gitOutput(context.Background(), repo, "rev-parse", "--abbrev-ref", "HEAD"); err != nil || out != "main\n" {
		t.Fatalf("gitOutput = %q, %v", out, err)
	}
}
