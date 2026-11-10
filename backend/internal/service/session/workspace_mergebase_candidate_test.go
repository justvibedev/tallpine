package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testgit"
)

// mergeBaseCandidate must resolve a candidate with a single merge-base spawn:
// the comparison is resolved on every workspace call, so an extra existence
// probe per candidate multiplies git processes (B321: the commit-review test
// ran 110 sequential spawns and blew its deadline under load).
func TestMergeBaseCandidateSpawnsNoExistenceProbe(t *testing.T) {
	repo := newWorkspaceRepo(t)
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	tree := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD^{tree}"))

	realGit, err := testgit.Real()
	if err != nil {
		t.Skip("git unavailable")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls.log")
	script := "#!/bin/sh\necho \"$@\" >> '" + log + "'\nexec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx := context.Background()
	if sha, ok := mergeBaseCandidate(ctx, repo, head); !ok || sha != head {
		t.Fatalf("valid commit = %q, %v; want %q", sha, ok, head)
	}
	for _, bad := range []string{"", "--octopus", "-h", "no-such-ref", strings.Repeat("a", 40), tree} {
		if sha, ok := mergeBaseCandidate(ctx, repo, bad); ok {
			t.Fatalf("candidate %q resolved to %q; want rejection", bad, sha)
		}
	}
	calls, _ := os.ReadFile(log)
	if strings.Contains(string(calls), "cat-file") {
		t.Fatalf("mergeBaseCandidate spawned an existence probe:\n%s", calls)
	}
}
