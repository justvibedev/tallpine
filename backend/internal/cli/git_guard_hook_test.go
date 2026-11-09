package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
)

func gitHookPayload(t *testing.T, cwd, tool string, input map[string]any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"cwd": cwd, "tool_name": tool, "tool_input": input})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func gitHookShellPayload(t *testing.T, cwd, command string) []byte {
	t.Helper()
	return []byte(gitHookPayload(t, cwd, "Bash", map[string]any{"command": command}))
}

func gitHookScratchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitT(t, dir, "init", "-q")
	return dir
}

// B600: the forms a shared write takes in a real shell command. Each one reached
// the real git when an rc file had moved it ahead of the shim.
func TestGitHookRefusesSharedWrites(t *testing.T) {
	f := newGitGuardFixture(t)
	scratch := gitHookScratchRepo(t)
	commands := map[string]string{
		"remote set-url":    "git remote set-url origin https://example.com/evil.git",
		"remote add":        "git remote add other https://example.com/o.git",
		"remote remove":     "git remote remove origin",
		"plain config":      "git config user.name AGENT",
		"local config":      "git config --local core.pager cat",
		"chained":           "git status && git remote add a b",
		"sequenced":         "git status; git config a.b c",
		"piped":             "git remote add a b | cat",
		"cd into worktree":  "cd " + f.worktree + " && git remote add a b",
		"dash C":            "git -C " + f.worktree + " config a.b c",
		"absolute git":      "/usr/bin/git remote rename origin other",
		"quoted words":      `git "remote" 'add' a b`,
		"escaped word":      `git rem\ote add a b`,
		"variable operand":  `git remote set-url origin "$URL"`,
		"variable key":      `git config "$KEY" "$VALUE"`,
		"bash -c":           "bash -c 'git remote add a b'",
		"bash -lc":          `bash -lc "git config a.b c"`,
		"zsh -c":            "zsh -c 'git remote add a b'",
		"bash long options": "bash --noprofile --norc -c 'git remote add a b'",
		"bash set options":  "bash -euo pipefail -c 'git remote add a b'",
		"bash single long":  "bash --norc -c 'git remote add a b'",
		"bash noprofile":    "bash --noprofile -c 'git remote add a b'",
		"bash rcfile":       "bash --rcfile /dev/null -c 'git remote add a b'",
		"bash c then flag":  "bash -c -e 'git remote add a b'",
		"eval":              `eval "git remote add a b"`,
		"command wrapper":   "command git remote add a b",
		"env wrapper":       "env FOO=1 git remote add a b",
		"nice wrapper":      "nice -n 19 git remote add a b",
		"timeout wrapper":   "timeout 5 git remote add a b",
		"subshell":          "(git remote add a b)",
		"command subst":     "echo $(git remote add a b)",
		"backticks":         "echo `git remote add a b`",
		"if body":           "if true; then git remote add a b; fi",
		"loop body":         "for r in a; do git remote add $r b; done",
		"xargs":             "echo origin | xargs git remote remove",
		"heredoc":           "bash <<'EOF'\ngit remote add a b\nEOF",
		"subshell cd stays": "(cd " + scratch + "); git remote add a b",
		"zsh-only syntax":   "echo ${(f)x}; git remote add a b",
		"zsh-only assign":   "echo ${(f)x}; FOO=1 git remote add a b",
		"zsh-only wrapped":  "echo ${(f)x}; env git remote add a b",
	}
	before := f.ownerConfig()
	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			reason := gitHookRefusal(context.Background(), gitHookShellPayload(t, f.worktree, command))
			if !strings.Contains(reason, "refused") {
				t.Fatalf("%q was not refused; reason=%q", command, reason)
			}
		})
	}
	if f.ownerConfig() != before {
		t.Fatal("classifying a command changed the owner config")
	}
}

// Legitimate git use must be untouched, including writes to other repositories.
func TestGitHookAllowsLegitimateCommands(t *testing.T) {
	f := newGitGuardFixture(t)
	scratch := gitHookScratchRepo(t)
	allow := agentlaunch.EnvAllowSharedGitConfig
	commands := map[string]string{
		"remote list":          "git remote -v",
		"remote get-url":       "git remote get-url origin",
		"remote update":        "git remote update",
		"config read":          "git config --get remote.origin.url",
		"config new read":      "git config get user.name",
		"config list":          "git config --list",
		"worktree config":      "git config --worktree a.b c",
		"global config":        "git config --global a.b c",
		"branch tracking key":  "git config branch.work.remote origin",
		"push upstream":        "git push -u origin work",
		"commit":               "git commit -m 'remote config'",
		"log grep":             "git log --grep config",
		"only text":            "echo 'git remote add a b'",
		"grep for it":          `grep -rn "git config" .`,
		"command lookup":       "command -v git",
		"other repo cd":        "cd " + scratch + " && git remote add a b",
		"other repo dash C":    "git -C " + scratch + " remote add a b",
		"other repo subshell":  "(cd " + scratch + " && git remote add a b)",
		"other repo GIT_DIR":   "GIT_DIR=" + filepath.Join(scratch, ".git") + " git remote add a b",
		"other repo bash -c":   "bash -c 'cd " + scratch + " && git remote add a b'",
		"new repo":             "mkdir fresh && cd fresh && git init && git remote add a b",
		"unknown dir":          `cd "$(mktemp -d)" && git init && git remote add a b`,
		"unknown dash C":       `git -C "$REPO" remote add a b`,
		"override prefix":      allow + "=1 git remote add a b",
		"override through env": "env " + allow + "=1 git remote add a b",
		"override exported":    "export " + allow + "=1; git remote add a b",
		"no git at all":        "ls -la",
		"zsh-only echoed":      `echo ${(f)x}; echo "never git config a.b c"`,
		"zsh-only separator":   `echo ${(f)x}; echo "a; git config a.b c"`,
		"zsh-only argument":    "echo ${(f)x}; echo git config a.b c",
		"patch body":           "*** Begin Patch\n*** Update File: a.md\n@@\n git remote add upstream URL\n*** End Patch",
		"bash script operand":  "bash --norc setup.sh git remote add a b",
		"bash odd script path": "bash 'git remote add a b'",
	}
	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			if reason := gitHookRefusal(context.Background(), gitHookShellPayload(t, f.worktree, command)); reason != "" {
				t.Fatalf("%q was refused: %s", command, reason)
			}
		})
	}
}

// Only a session's own project is guarded, and a tool without a shell command
// has nothing to scan.
func TestGitHookIgnoresWhatItDoesNotGuard(t *testing.T) {
	f := newGitGuardFixture(t)
	refused := "git remote add a b"
	cases := map[string][]byte{
		"not json":       []byte("nope"),
		"no command":     []byte(gitHookPayload(t, f.worktree, "read", map[string]any{"path": "a"})),
		"non-string":     []byte(gitHookPayload(t, f.worktree, "x", map[string]any{"command": []string{"git", "remote", "add", "a", "b"}})),
		"todo-style":     []byte(gitHookPayload(t, f.worktree, "todo", map[string]any{"command": "create"})),
		"apply_patch":    []byte(gitHookPayload(t, f.worktree, "apply_patch", map[string]any{"command": "git remote add upstream URL"})),
		"no cwd":         gitHookShellPayload(t, "", refused),
		"other repo cwd": gitHookShellPayload(t, gitHookScratchRepo(t), refused),
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if reason := gitHookRefusal(context.Background(), payload); reason != "" {
				t.Fatalf("refused: %s", reason)
			}
		})
	}
	t.Run("session without guard root", func(t *testing.T) {
		t.Setenv(agentlaunch.EnvGitGuardRoot, "")
		if reason := gitHookRefusal(context.Background(), gitHookShellPayload(t, f.worktree, refused)); reason != "" {
			t.Fatalf("refused: %s", reason)
		}
	})
}

// The whole hook: the harness sees exit 2 with the reason on stderr, which is
// what Codex and Kiro treat as "do not run this tool".
func TestHooksBlockSharedGitWritesForShellHarnesses(t *testing.T) {
	for _, agent := range []string{"codex", "kiro", "deepseek-harness"} {
		t.Run(agent, func(t *testing.T) {
			f := newGitGuardFixture(t)
			t.Setenv("TALLPINE_SESSION_ID", "worker-1")
			cfg := setConfigEnv(t)
			srv, _ := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)
			before := f.ownerConfig()
			_, errOut, err := executeCLI(t, Deps{
				In:           strings.NewReader(gitHookPayload(t, f.worktree, "Bash", map[string]any{"command": "git remote set-url origin x"})),
				ProcessAlive: func(int) bool { return true },
			}, "hooks", agent, "pre-tool-use")
			if ExitCode(err) != 2 {
				t.Fatalf("exit = %d, want 2 (err=%v)", ExitCode(err), err)
			}
			if !strings.Contains(errOut, "tallpine git: refused") {
				t.Fatalf("stderr = %q", errOut)
			}
			if f.ownerConfig() != before {
				t.Fatal("owner config changed")
			}
		})
	}
}

// Reviewers share a worktree with their worker and take their own hook path.
func TestHooksBlockSharedGitWritesForReviewers(t *testing.T) {
	for _, event := range []string{hookEventPreTool, hookEventGitGuard} {
		t.Run(event, func(t *testing.T) {
			f := newGitGuardFixture(t)
			t.Setenv("TALLPINE_REVIEW_SESSION_ID", "review-7")
			cfg := setConfigEnv(t)
			srv, _ := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)
			_, errOut, err := executeCLI(t, Deps{
				In:           strings.NewReader(gitHookPayload(t, f.worktree, "Bash", map[string]any{"command": "git remote set-url origin x"})),
				ProcessAlive: func(int) bool { return true },
			}, "hooks", "codex", event)
			if ExitCode(err) != 2 || !strings.Contains(errOut, "tallpine git: refused") {
				t.Fatalf("exit = %d (err=%v), stderr = %q", ExitCode(err), err, errOut)
			}
		})
	}
}

// An allowed tool call must still report activity and exit 0.
func TestHooksStillReportActivityForAllowedTools(t *testing.T) {
	f := newGitGuardFixture(t)
	t.Setenv("TALLPINE_SESSION_ID", "worker-1")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(gitHookPayload(t, f.worktree, "Bash", map[string]any{"command": "git status"})),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "pre-tool-use")
	if err != nil {
		t.Fatalf("hook failed: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/sessions/worker-1/activity" {
		t.Fatalf("activity path = %q", capture.path)
	}
}

// Claude Code keeps the launch PATH, so its shim does the refusing and its hook
// never adds a second, different decision.
func TestHooksLeaveClaudeCodeToTheShim(t *testing.T) {
	f := newGitGuardFixture(t)
	t.Setenv("TALLPINE_SESSION_ID", "worker-1")
	cfg := setConfigEnv(t)
	srv, _ := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(gitHookPayload(t, f.worktree, "Bash", map[string]any{"command": "git remote set-url origin x"})),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "pre-tool-use")
	if err != nil {
		t.Fatalf("claude-code hook was blocked: %v\nstderr=%s", err, errOut)
	}
}

// The chat app-server's guard-only event refuses without reporting activity.
func TestHooksGitGuardEventRefusesAndReportsNothing(t *testing.T) {
	f := newGitGuardFixture(t)
	t.Setenv("TALLPINE_SESSION_ID", "worker-1")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	deps := func(command string) Deps {
		return Deps{
			In:           strings.NewReader(gitHookPayload(t, f.worktree, "Bash", map[string]any{"command": command})),
			ProcessAlive: func(int) bool { return true },
		}
	}
	if _, _, err := executeCLI(t, deps("git remote add a b"), "hooks", "codex", hookEventGitGuard); ExitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2 (err=%v)", ExitCode(err), err)
	}
	if _, errOut, err := executeCLI(t, deps("git status"), "hooks", "codex", hookEventGitGuard); err != nil {
		t.Fatalf("allowed command failed: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "" {
		t.Fatalf("guard-only event reported activity to %q", capture.path)
	}
}

// A refusal must not depend on the daemon being up.
func TestHooksBlockWithoutADaemon(t *testing.T) {
	f := newGitGuardFixture(t)
	t.Setenv("TALLPINE_SESSION_ID", "worker-1")
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(gitHookPayload(t, f.worktree, "Bash", map[string]any{"command": "git remote add a b"})),
		ProcessAlive: func(int) bool { return false },
	}, "hooks", "kiro", "pre-tool-use")
	if ExitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2 (err=%v)", ExitCode(err), err)
	}
}

// Once a `cd` cannot be followed the target is unknown, so the hook must not fall
// back to the directory the hook itself runs in.
func TestGitHookDoesNotGuessAfterAnUnknownCd(t *testing.T) {
	f := newGitGuardFixture(t)
	t.Chdir(f.worktree)
	command := `cd "$(mktemp -d)" && git init && git remote add a b`
	if reason := gitHookRefusal(context.Background(), gitHookShellPayload(t, f.worktree, command)); reason != "" {
		t.Fatalf("refused: %s", reason)
	}
}

func TestGitHookWorkingDirectoryIsResolvedRelativeToTheCall(t *testing.T) {
	f := newGitGuardFixture(t)
	nested := filepath.Join(f.worktree, "sub")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if reason := gitHookRefusal(context.Background(), gitHookShellPayload(t, f.worktree, "cd sub && git remote add a b")); reason == "" {
		t.Fatal("relative cd inside the worktree was not refused")
	}
}
