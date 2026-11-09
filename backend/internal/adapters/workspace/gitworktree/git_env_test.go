package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// traceEnv makes git record what it ran in a file, proving env reached the
// real process.
func traceEnv(t *testing.T) ([]string, func() string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "git-trace")
	return []string{"GIT_TRACE=" + file}, func() string {
		b, err := os.ReadFile(file)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return string(b)
	}
}

func TestFetchDefaultBranchPassesCredentialEnvToGit(t *testing.T) {
	t.Parallel()
	_, _, repo, _, ws := newOriginWorkspace(t)
	target, err := ws.ResolveDefaultBranch(context.Background(), repo, "main")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	env, trace := traceEnv(t)
	if err := ws.FetchDefaultBranch(context.Background(), repo, target, env); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.Contains(trace(), "fetch") {
		t.Fatalf("git fetch did not get the env; trace: %q", trace())
	}
}

// A fetch must not be shared with a caller that holds another credential: its
// failure would be reported to a caller who could have succeeded.
func TestFetchDefaultBranchDoesNotShareFetchAcrossCredentials(t *testing.T) {
	t.Parallel()
	_, _, repo, _, ws := newOriginWorkspace(t)
	target, err := ws.ResolveDefaultBranch(context.Background(), repo, "main")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var fetches atomic.Int32
	firstIn := make(chan struct{})
	release := make(chan struct{})
	realRun := ws.run
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		if len(args) > 2 && args[2] == "fetch" && fetches.Add(1) == 1 {
			close(firstIn)
			<-release
		}
		return realRun(ctx, binary, args...)
	}
	first := make(chan error, 1)
	go func() { first <- ws.FetchDefaultBranch(context.Background(), repo, target, []string{"GIT_TRACE=0"}) }()
	<-firstIn
	second := make(chan error, 1)
	go func() { second <- ws.FetchDefaultBranch(context.Background(), repo, target, nil) }()
	deadline := time.After(10 * time.Second)
	for fetches.Load() < 2 {
		select {
		case <-deadline:
			t.Fatal("second fetch joined the first instead of running its own")
		case <-time.After(time.Millisecond):
		}
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}

func TestOrchestratorRefreshFetchesWithCredentialEnv(t *testing.T) {
	t.Parallel()
	f := newRefreshFixture(t, domain.KindOrchestrator)
	env, trace := traceEnv(t)
	f.cfg.RefreshGitEnv = env
	fresh := advanceOriginMain(t, f.git, f.tmp, f.repo)
	if _, err := f.ws.Restore(context.Background(), f.cfg); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !strings.Contains(trace(), "fetch") {
		t.Fatalf("orchestrator refresh fetched without the env; trace: %q", trace())
	}
	if got := f.head(t); got != fresh {
		t.Fatalf("HEAD = %s, want %s", got, fresh)
	}
}
