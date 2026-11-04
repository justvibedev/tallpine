package releaseinstall

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/process"
)

// keepScriptTrees is how many origin/main script worktrees stay on disk.
const keepScriptTrees = 3

// BuildRequester runs `scripts/release-local request-build` as the daemon. A
// fenced agent cannot write StateDir/release-builds, and must not: the daemon
// installs whatever lives there, so write access would let a worker swap the
// app. The script comes from a daemon-owned detached worktree of the project's
// origin/main (outside the agent's write fence, never the agent's worktree, and
// never the owner's checkout, which is not kept current: release-local refuses
// to build from a script that is behind origin/main).
type BuildRequester struct {
	StateDir string
	// ProjectPath resolves a project id to its registered checkout path.
	ProjectPath func(ctx context.Context, projectID string) (string, error)
	// Git runs git in dir and returns its trimmed stdout (test seam).
	Git func(ctx context.Context, dir string, args ...string) (string, error)
	// Start runs argv detached with env and returns its pid (test seam).
	Start func(argv, env []string, logPath string) (int, error)
}

// NewBuildRequester returns a requester that runs the real script.
func NewBuildRequester(stateDir string, projectPath func(context.Context, string) (string, error)) *BuildRequester {
	return &BuildRequester{StateDir: stateDir, ProjectPath: projectPath, Git: runGit, Start: startDetached}
}

// Request starts the detached request-build for the project and returns its
// log path and pid.
func (b *BuildRequester) Request(ctx context.Context, projectID string) (Result, error) {
	if projectID == "" {
		return Result{}, refuse("no project for this request")
	}
	root, err := b.ProjectPath(ctx, projectID)
	if err != nil || root == "" {
		return Result{}, refuse("project %s has no checkout path: %v", projectID, err)
	}
	tree, err := b.scriptTree(ctx, root)
	if err != nil {
		return Result{}, err
	}
	script := filepath.Join(tree, "scripts", "release-local")
	if _, err := os.Stat(script); err != nil {
		return Result{}, refuse("origin/main has no scripts/release-local: %v", err)
	}
	logDir := filepath.Join(b.StateDir, "release-builds")
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return Result{}, fmt.Errorf("create release-builds: %w", err)
	}
	logPath := filepath.Join(logDir, "request-build.log")
	argv := []string{script, "--state-dir", b.StateDir, "request-build"}
	pid, err := b.Start(argv, cleanEnv(), logPath)
	if err != nil {
		return Result{}, fmt.Errorf("start request-build: %w", err)
	}
	return Result{Log: logPath, PID: pid}, nil
}

// scriptTree fetches origin/main in the project repo and returns a detached
// worktree of that commit under StateDir/release-scripts/<sha12>, creating it
// when missing. Each commit gets its own tree so a running build never sees its
// scripts change; older trees beyond keepScriptTrees are pruned best-effort.
func (b *BuildRequester) scriptTree(ctx context.Context, repo string) (string, error) {
	if _, err := b.Git(ctx, repo, "fetch", "--quiet", "origin", "main"); err != nil {
		return "", fmt.Errorf("fetch origin main: %w", err)
	}
	sha, err := b.Git(ctx, repo, "rev-parse", "--verify", "origin/main^{commit}")
	if err != nil || !shaPattern.MatchString(sha) {
		return "", refuse("cannot resolve origin/main in %s: %v", repo, err)
	}
	base := filepath.Join(b.StateDir, "release-scripts")
	tree := filepath.Join(base, sha[:12])
	if _, err := os.Stat(tree); err != nil {
		if err := os.MkdirAll(base, 0o750); err != nil {
			return "", fmt.Errorf("create release-scripts: %w", err)
		}
		if _, err := b.Git(ctx, repo, "worktree", "add", "--detach", "--force", tree, sha); err != nil {
			return "", fmt.Errorf("add origin/main worktree: %w", err)
		}
	}
	b.pruneScriptTrees(ctx, repo, base, tree)
	return tree, nil
}

func (b *BuildRequester) pruneScriptTrees(ctx context.Context, repo, base, keep string) {
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) <= keepScriptTrees {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		a, _ := entries[i].Info()
		c, _ := entries[j].Info()
		return a != nil && c != nil && a.ModTime().After(c.ModTime())
	})
	for _, e := range entries[keepScriptTrees:] {
		if dir := filepath.Join(base, e.Name()); dir != keep {
			_, _ = b.Git(ctx, repo, "worktree", "remove", "--force", dir)
		}
	}
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := process.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
