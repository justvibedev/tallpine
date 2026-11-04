package importer

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// A session environment that carries GIT_DIR must not redirect the importer's
// git calls away from the folder being inspected.
func TestImporterGitIgnoresInheritedGitDir(t *testing.T) {
	repo := testenv.GitRepo(t)
	testenv.IgnoredRepoEnv(t)
	if !isImportGitRepo(repo) {
		t.Fatal("isImportGitRepo = false with GIT_DIR set")
	}
	if !importRemoteExists(repo, "origin") {
		t.Fatal("importRemoteExists(origin) = false with GIT_DIR set")
	}
	if got := resolveImportOriginURL(repo); got != "https://example.com/o/r.git" {
		t.Fatalf("resolveImportOriginURL = %q", got)
	}
	if out, err := importGitOutput(context.Background(), repo, "rev-parse", "--abbrev-ref", "HEAD"); err != nil || out != "main\n" {
		t.Fatalf("importGitOutput = %q, %v", out, err)
	}
}
