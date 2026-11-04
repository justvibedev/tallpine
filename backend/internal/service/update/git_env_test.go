package update

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestUpdateGitIgnoresInheritedGitDir(t *testing.T) {
	repo := testenv.GitRepo(t)
	testenv.IgnoredRepoEnv(t)
	out, err := git(context.Background(), repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || out != "main" {
		t.Fatalf("git = %q, %v", out, err)
	}
}
