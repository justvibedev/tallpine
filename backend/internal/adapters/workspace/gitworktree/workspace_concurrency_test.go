package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestWorkspaceIntegrationConcurrentCreatesInOneRepo drives the orchestrator's
// fan-out shape: many fresh workers spawned in the same repository at once.
// Every spawn used to write the repository's shared .git/config (enabling
// extensions.worktreeConfig, and `worktree add -b` recording the new branch's
// upstream), and git refuses a second writer while config.lock exists, so a
// large share of simultaneous spawns failed with "could not lock config file".
func TestWorkspaceIntegrationConcurrentCreatesInOneRepo(t *testing.T) {
	t.Parallel()

	git, _, repo, _, ws := newOriginWorkspace(t)
	ctx := context.Background()
	freshConfig := func(id string) ports.WorkspaceConfig {
		return ports.WorkspaceConfig{
			ProjectID: "proj", SessionID: domain.SessionID(id), Kind: domain.KindWorker,
			Branch: "vo/" + id + "/root", FreshBranch: true, BaseBranch: "main",
		}
	}

	// A lone spawn is the reference for the upstream config every concurrent
	// spawn must also end up with.
	solo, err := ws.Create(ctx, freshConfig("solo"))
	if err != nil {
		t.Fatalf("solo create: %v", err)
	}
	wantUpstream := branchUpstream(t, git, repo, solo.Branch)

	const spawns = 20
	infos := make([]ports.WorkspaceInfo, spawns)
	errs := make([]error, spawns)
	start, wait := concurrently(spawns, func(i int) {
		infos[i], errs[i] = ws.Create(ctx, freshConfig(fmt.Sprintf("worker-%d", i)))
	})
	start()
	wait()

	var failed []string
	for i, err := range errs {
		if err != nil {
			failed = append(failed, fmt.Sprintf("worker-%d: %v", i, err))
		}
	}
	if len(failed) > 0 {
		t.Fatalf("%d of %d concurrent creates failed:\n%s", len(failed), spawns, strings.Join(failed, "\n"))
	}
	registered := string(outputGit(t, git, repo, "worktree", "list", "--porcelain"))
	for i, info := range infos {
		if !strings.Contains(registered, "worktree "+info.Path+"\n") {
			t.Errorf("worker-%d worktree %q is not registered", i, info.Path)
		}
		if got := branchUpstream(t, git, repo, info.Branch); got != wantUpstream {
			t.Errorf("worker-%d branch %q upstream = %q, want %q (same as a lone spawn)", i, info.Branch, got, wantUpstream)
		}
	}
}

// branchUpstream returns the branch's recorded upstream remote and merge ref,
// empty where git recorded none.
func branchUpstream(t *testing.T, git, repo, branch string) string {
	t.Helper()
	var parts []string
	for _, key := range []string{"remote", "merge"} {
		out, err := exec.Command(git, "-C", repo, "config", "--get", "branch."+branch+"."+key).Output()
		var exitErr *exec.ExitError
		unset := errors.As(err, &exitErr) && exitErr.ExitCode() == 1
		if err != nil && !unset {
			t.Fatalf("read branch.%s.%s: %v", branch, key, err)
		}
		parts = append(parts, strings.TrimSpace(string(out)))
	}
	return strings.Join(parts, " ")
}

// TestWorkspaceIntegrationEnablesWorktreeConfigLocallyDespiteGlobalValue: the
// skip-if-enabled read must look at the repository's own config only, because
// git honors extensions.worktreeConfig from nowhere else. A user-level value
// must not leave the repository without it.
func TestWorkspaceIntegrationEnablesWorktreeConfigLocallyDespiteGlobalValue(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	global := filepath.Join(tmp, "global.gitconfig")
	if err := os.WriteFile(global, []byte("[extensions]\n\tworktreeConfig = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.enableWorktreeConfig(context.Background(), repo); err != nil {
		t.Fatalf("enable worktree config: %v", err)
	}
	if got := strings.TrimSpace(string(outputGit(t, git, repo, "config", "--local", "--get", "extensions.worktreeConfig"))); got != "true" {
		t.Fatalf("repository-local extensions.worktreeConfig = %q, want true", got)
	}
}

// TestWorkspaceIntegrationConcurrentDefaultBranchFetchesShareOne: spawns into
// one project run in parallel and each refreshes the same default branch
// first. Callers that arrive while a fetch is in flight share it instead of
// racing a second fetch on the same remote-tracking ref.
func TestWorkspaceIntegrationConcurrentDefaultBranchFetchesShareOne(t *testing.T) {
	t.Parallel()
	git, tmp, repo, _, ws := newOriginWorkspace(t)
	ctx := context.Background()
	target, err := ws.ResolveDefaultBranch(ctx, repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	// Advance origin so the shared fetch has something to bring in.
	seed := filepath.Join(tmp, "seed")
	if err := os.WriteFile(filepath.Join(seed, "next.txt"), []byte("next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, seed, "add", "next.txt")
	runGit(t, git, seed, "commit", "-m", "next")
	runGit(t, git, seed, "push", "origin", "main")
	want := strings.TrimSpace(string(outputGit(t, git, seed, "rev-parse", "HEAD")))

	var fetches atomic.Int32
	leaderIn := make(chan struct{})
	release := make(chan struct{})
	realRun := ws.run
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		if len(args) > 2 && args[2] == "fetch" {
			if fetches.Add(1) == 1 {
				close(leaderIn)
				<-release
			}
		}
		return realRun(ctx, binary, args...)
	}

	const callers = 8
	errs := make([]error, callers)
	start, wait := concurrently(callers, func(i int) {
		errs[i] = ws.FetchDefaultBranch(ctx, repo, target, nil)
	})
	start()
	<-leaderIn
	// Let every other caller reach the in-flight fetch before it finishes.
	time.Sleep(100 * time.Millisecond)
	close(release)
	wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("%d concurrent refreshes ran %d fetches, want 1", callers, got)
	}
	if got := strings.TrimSpace(string(outputGit(t, git, repo, "rev-parse", target.BaseRef))); got != want {
		t.Fatalf("%s = %s after the shared fetch, want %s", target.BaseRef, got, want)
	}
}

// TestWorkspaceIntegrationConcurrentAddExcludeWritesOnce: info/exclude is
// shared by every worktree of a repository, and parallel spawns that carry
// attachments all add the same pattern to it.
func TestWorkspaceIntegrationConcurrentAddExcludeWritesOnce(t *testing.T) {
	t.Parallel()
	_, _, repo, _, ws := newOriginWorkspace(t)
	ctx := context.Background()
	const spawns = 8
	infos := make([]ports.WorkspaceInfo, spawns)
	for i := range spawns {
		id := fmt.Sprintf("worker-%d", i)
		info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: domain.SessionID(id), Branch: "vo/" + id + "/root"})
		if err != nil {
			t.Fatal(err)
		}
		infos[i] = info
	}
	const pattern = "/.tallpine/attachments/"
	errs := make([]error, spawns)
	start, wait := concurrently(spawns, func(i int) {
		errs[i] = ws.AddExclude(ctx, infos[i], pattern)
	})
	start()
	wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker-%d: %v", i, err)
		}
	}
	exclude, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(exclude), pattern); got != 1 {
		t.Fatalf("info/exclude lists %q %d times, want once:\n%s", pattern, got, exclude)
	}
}

// A caller that gives up stops only its own wait. A speculative preparation
// that leads the shared fetch and is then cancelled must not kill the fetch a
// still-live spawn joined, or that spawn would branch from stale refs.
func TestWorkspaceIntegrationCancelledFetchLeaderDoesNotAbortFollower(t *testing.T) {
	git, tmp, repo, _, ws := newOriginWorkspace(t)
	target, err := ws.ResolveDefaultBranch(context.Background(), repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(tmp, "seed")
	if err := os.WriteFile(filepath.Join(seed, "later.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, seed, "add", "later.txt")
	runGit(t, git, seed, "commit", "-m", "later")
	runGit(t, git, seed, "push", "origin", "main")
	want := strings.TrimSpace(string(outputGit(t, git, seed, "rev-parse", "HEAD")))

	var fetches atomic.Int32
	leaderIn := make(chan struct{})
	release := make(chan struct{})
	realRun := ws.run
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		if len(args) > 2 && args[2] == "fetch" && fetches.Add(1) == 1 {
			close(leaderIn)
			<-release
		}
		return realRun(ctx, binary, args...)
	}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() { leaderDone <- ws.FetchDefaultBranch(leaderCtx, repo, target, nil) }()
	<-leaderIn
	followerDone := make(chan error, 1)
	go func() { followerDone <- ws.FetchDefaultBranch(context.Background(), repo, target, nil) }()
	// Let the follower join the in-flight fetch, then cancel only the leader.
	time.Sleep(100 * time.Millisecond)
	cancelLeader()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled leader = %v, want its own cancellation", err)
	}
	close(release)
	if err := <-followerDone; err != nil {
		t.Fatalf("live follower failed after the leader was cancelled: %v", err)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches = %d, want the follower to share the one in flight", got)
	}
	if got := strings.TrimSpace(string(outputGit(t, git, repo, "rev-parse", target.BaseRef))); got != want {
		t.Fatalf("%s = %s, want the fetched %s", target.BaseRef, got, want)
	}
}

// concurrently prepares n goroutines running fn(i) behind a start barrier.
// start releases them all at once; wait blocks until every one has returned.
func concurrently(n int, fn func(i int)) (start, wait func()) {
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			fn(i)
		}()
	}
	return func() { close(gate) }, wg.Wait
}
