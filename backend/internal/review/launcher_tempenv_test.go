package review

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// A reviewer must not fall back to the shared /tmp/claude-<uid> (B428): every
// temp key, CLAUDE_CODE_TMPDIR included, points at its own directory.
func TestReviewerGetsOwnTempDir(t *testing.T) {
	dataDir := t.TempDir()
	rt := &fakeRuntime{}
	reviewer := &fakeReviewer{}
	l := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, rt, dataDir)
	spec := launchSpec()
	if _, err := l.Spawn(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(sessionscratch.TempDir(dataDir, spec.WorkerID), "review")
	for _, key := range reviewerTempKeys {
		if got := rt.createCfg.Env[key]; got != want || !strings.HasPrefix(got, dataDir) {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if got := rt.createCfg.Env[sessionscratch.TempPrefixEnv]; got != sessionscratch.TempPrefix(want) {
		t.Fatalf("TMPPREFIX = %q, want zsh temp files in %q (B521)", got, want)
	}
	// A fenced reviewer opens the same dir for writes (B450).
	if reviewer.gotInv.TempDir != want {
		t.Fatalf("invocation TempDir = %q, want %q", reviewer.gotInv.TempDir, want)
	}
}

func TestReviewerKeepsProjectTempEnv(t *testing.T) {
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, t.TempDir())
	spec := launchSpec()
	spec.ProjectEnv = map[string]string{"TMPDIR": "/project/tmp"}
	if _, err := l.Spawn(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if rt.createCfg.Env["TMPDIR"] != "/project/tmp" || rt.createCfg.Env["CLAUDE_CODE_TMPDIR"] != "" {
		t.Fatalf("project temp env not kept: %v", rt.createCfg.Env)
	}
}
