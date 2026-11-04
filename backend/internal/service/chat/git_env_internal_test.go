package chat

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestHandoffChangedFilesIgnoreInheritedGitDir(t *testing.T) {
	repo := testenv.GitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	testenv.IgnoredRepoEnv(t)
	if got := handoffChangedFiles(context.Background(), repo, ""); !slices.Contains(got, "new.txt") {
		t.Fatalf("handoffChangedFiles = %v, want new.txt", got)
	}
}
