// Package fencetest is the shared assertion that a code path building a seatbelt
// profile takes its git paths only from the repository the daemon has on record.
package fencetest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/brand"
)

// Build returns the profile a code path produces for this decision, env and
// workspace.
type Build func(t *testing.T, decision agentpolicy.Decision, env map[string]string, workspace string) string

// AssertGitPathsTrusted checks one profile-building path: a normal linked
// worktree gets its repo's common dir, and a .git file naming HOME gets nothing.
func AssertGitPathsTrusted(t *testing.T, build Build) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	common := filepath.Join(repo, ".git")
	private := filepath.Join(common, "worktrees", "w")
	ws := filepath.Join(root, "w")
	for _, d := range []string{home, private, ws} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(root, "state", "running.json"), brand.Env("DATA_DIR"): filepath.Join(root, "state", "data"), "HOME": home}
	decision := policytest.DecisionForRepo(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}}, repo)
	dotGit := filepath.Join(ws, ".git")
	writeFile(t, filepath.Join(private, "gitdir"), dotGit)

	writeClaim(t, dotGit, private)
	if p := build(t, decision, env, ws); !strings.Contains(p, below(common)) {
		t.Errorf("linked worktree lost its common dir:\n%s", p)
	}
	writeClaim(t, dotGit, home)
	if p := build(t, decision, env, ws); strings.Contains(p, subpath(home)) || strings.Contains(p, below(common)) {
		t.Errorf("a crafted .git widened the profile:\n%s", p)
	}
}

// RootsBuild returns what a Codex workspace-write path hands the sandbox as its
// writable roots, rendered as text, for this decision and workspace.
type RootsBuild func(t *testing.T, decision agentpolicy.Decision, workspace string) string

// AssertCodexRootsTrusted checks one Codex roots path: a normal linked worktree
// gets its repo's roots, and neither a crafted .git nor a crafted commondir
// names anything outside them.
func AssertCodexRootsTrusted(t *testing.T, build RootsBuild) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "foreign")
	repo := filepath.Join(root, "repo")
	common := filepath.Join(repo, ".git")
	private := filepath.Join(common, "worktrees", "w")
	ws := filepath.Join(root, "w")
	for _, d := range []string{foreign, private, ws} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	decision := policytest.DecisionForRepo(t, nil, repo)
	dotGit := filepath.Join(ws, ".git")
	writeFile(t, filepath.Join(private, "gitdir"), dotGit)
	writeClaim(t, dotGit, private)
	if got := build(t, decision, ws); !strings.Contains(got, filepath.Join(common, "objects")) || !strings.Contains(got, private) {
		t.Errorf("linked worktree lost its git roots: %s", got)
	}
	writeFile(t, filepath.Join(private, "commondir"), foreign)
	if got := build(t, decision, ws); strings.Contains(got, foreign) {
		t.Errorf("a crafted commondir named a root: %s", got)
	}
	writeClaim(t, dotGit, foreign)
	if got := build(t, decision, ws); strings.Contains(got, foreign) || strings.Contains(got, common) {
		t.Errorf("a crafted .git named a root: %s", got)
	}
}

func subpath(p string) string { return `(subpath "` + p + `")` }

func below(p string) string { return `"^` + strings.ReplaceAll(regexp.QuoteMeta(p), `\`, `\\`) + `/"` }

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeClaim(t *testing.T, dotGit, dir string) {
	t.Helper()
	if err := os.WriteFile(dotGit, []byte("gitdir: "+dir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
