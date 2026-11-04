// Package testenv makes a test binary hermetic to the agent session and the
// human's home directory it was started from.
//
// Agents run `go test` inside Tallpine (or official AO, or Claude
// Code) sessions whose environment carries TALLPINE_SESSION_ID, TALLPINE_TMUX_SOCKET_NAME,
// TALLPINE_DATA_DIR, CLAUDE_CODE_* and similar variables. Production code reads
// several of them, so an inherited value silently changes what a test
// exercises. Tests that start interactive shells also wrote their commands to
// the human's real ~/.bash_history, and a real `gh` on PATH could read the
// human's GitHub credentials and reach the network.
//
// Every test package calls Main from its TestMain:
//
//	func TestMain(m *testing.M) { testenv.Main(m) }
//
// TestTestMainsUseTestenv fails for a package that does not.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// activeVar marks a process whose environment testenv already prepared. A
// test that re-executes its own binary as a helper usually passes
// os.Environ() plus a signal variable; the child keeps that environment as is
// instead of being scrubbed a second time. Production code that rebuilds a
// child environment from an allowlist drops the marker but keeps HOME, so a
// sandbox HOME (see inSandbox) means the same.
const activeVar = "TALLPINE_TESTENV_ACTIVE"

const sandboxPrefix = "tallpine-testenv-"

// inSandbox reports whether HOME is a sandbox an ancestor test made.
func inSandbox() bool {
	home := os.Getenv("HOME")
	return filepath.Base(home) == "home" && strings.HasPrefix(filepath.Base(filepath.Dir(home)), sandboxPrefix)
}

// scrubPrefixes are inherited variable families that select a live session,
// daemon, harness or credential. Production code reads several of them.
var scrubPrefixes = []string{
	"TALLPINE_", "VO_", "AO_", // Tallpine (current and legacy prefix) and official AO session/daemon wiring
	"CLAUDE", "ANTHROPIC_", // Claude Code session and credentials (CLAUDECODE, CLAUDE_CODE_*)
	"CODEX_", "OPENAI_", "KIRO_", "CURSOR_",
	"TMUX", // TMUX, TMUX_PANE, TMUX_TMPDIR: the caller's tmux server
	"GH_", "GITHUB_", "GITLAB_", "GLAB_",
	"GIT_",          // GIT_DIR and friends leak in from git hooks
	"npm_", "NODE_", // npm run lifecycle variables
}

// scrubNames are single inherited variables that change shell or tool behavior.
var scrubNames = []string{
	"HISTFILE", "ZDOTDIR", "BASH_ENV", "ENV", "PROMPT_COMMAND",
	"SSH_ASKPASS", "GNUPGHOME",
}

// keep reports whether an inherited variable is a deliberate opt-in by the
// person running the tests, or a helper-process signal, rather than session
// state: live-provider suites and re-exec helpers read these.
func keep(name string) bool {
	switch {
	case name == activeVar:
		return false
	case strings.HasPrefix(name, "TALLPINE_TEST_"),
		strings.HasPrefix(name, "TALLPINE_LIVE_"),
		name == "TALLPINE_CODEX_LIVE",
		name == "TALLPINE_SOCKET_ALIAS_ROOT", // B463: where long unix-socket paths alias; the Claude fence allows only one dir
		strings.HasPrefix(name, "TALLPINE_CHAT_HOST_"),
		strings.HasPrefix(name, "GO_WANT_"):
		return true
	}
	return false
}

func scrubbed(name string) bool {
	if keep(name) {
		return false
	}
	for _, prefix := range scrubPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for _, n := range scrubNames {
		if name == n {
			return true
		}
	}
	return false
}

// Main prepares the hermetic environment, runs the tests, removes the
// sandbox and exits with the tests' status.
func Main(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	cleanup, err := Setup()
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}
	defer cleanup()
	return m.Run()
}

// Setup is Main without running the tests, for a TestMain that has its own
// work around m.Run. Call the returned cleanup after m.Run and before os.Exit.
//
// It unsets inherited session variables, points HOME, the XDG base dirs and
// TMUX_TMPDIR at a fresh sandbox, sets HISTFILE=/dev/null, disables the git
// system config, and puts failing `gh` and `glab` shims first on PATH. Go's own cache dirs
// stay where they were, so a test that runs `go build` reuses the warm cache.
func Setup() (func(), error) {
	if os.Getenv(activeVar) != "" || inSandbox() {
		return func() {}, nil
	}
	reapStaleE2EDaemons()
	reapStaleTmuxServers(tmuxReapRoot())
	goDirs := resolveGoDirs()

	root, err := os.MkdirTemp("", sandboxPrefix)
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	fail := func(err error) (func(), error) {
		cleanup()
		return nil, err
	}

	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if scrubbed(name) {
			if err := os.Unsetenv(name); err != nil {
				return fail(err)
			}
		}
	}

	home := filepath.Join(root, "home")
	shimDir := filepath.Join(root, "bin")
	dirs := map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
	}
	if runtime.GOOS != "windows" {
		// A private tmux socket dir keeps tests off the caller's tmux
		// servers. tmux appends tmux-<uid>/<socket>, and macOS's long
		// $TMPDIR leaves too little of the 104-byte sun_path, so use /tmp.
		tmuxDir, err := mkTmuxDir()
		if err != nil {
			return fail(err)
		}
		cleanup = func() {
			// Removing the socket dir alone orphans any server still in it.
			killTmuxServers(tmuxDir)
			_ = os.RemoveAll(root)
			_ = os.RemoveAll(tmuxDir)
		}
		dirs["TMUX_TMPDIR"] = tmuxDir
	}
	for name, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fail(err)
		}
		if err := os.Setenv(name, dir); err != nil {
			return fail(err)
		}
	}
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		return fail(err)
	}
	// A global identity in the sandbox HOME lets tests commit; unlike
	// GIT_AUTHOR_* it stays below a repository's own user.name/email.
	gitconfig := "[user]\n\tname = testenv\n\temail = testenv@example.invalid\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(gitconfig), 0o600); err != nil {
		return fail(err)
	}
	for _, tool := range []string{"gh", "glab"} {
		if err := writeShim(shimDir, tool); err != nil {
			return fail(err)
		}
	}

	path := shimDir
	if goBin := toolchainBin(); goBin != "" {
		path += string(os.PathListSeparator) + goBin
	}
	if inherited := os.Getenv("PATH"); inherited != "" {
		path += string(os.PathListSeparator) + inherited
	}

	set := map[string]string{
		"PATH":     path,
		"HISTFILE": os.DevNull,
		// Skip the system git config (the macOS keychain credential
		// helper) and never prompt for credentials.
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0",
		activeVar:             "1",
	}
	for name, value := range goDirs {
		set[name] = value
	}
	for name, value := range set {
		if err := os.Setenv(name, value); err != nil {
			return fail(err)
		}
	}
	return cleanup, nil
}

// resolveGoDirs pins Go's per-user directories to the values they have
// before HOME moves, so `go` run by a test keeps the shared build and module
// caches (and npm its package cache) and the user's `go env -w` settings instead of starting cold.
func resolveGoDirs() map[string]string {
	dirs := map[string]string{}
	home, _ := os.UserHomeDir()
	if v := os.Getenv("GOCACHE"); v != "" {
		dirs["GOCACHE"] = v
	} else if cache, err := os.UserCacheDir(); err == nil {
		dirs["GOCACHE"] = filepath.Join(cache, "go-build")
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" && home != "" {
		gopath = filepath.Join(home, "go")
	}
	if gopath != "" {
		dirs["GOPATH"] = gopath
	}
	if v := os.Getenv("GOMODCACHE"); v != "" {
		dirs["GOMODCACHE"] = v
	} else if gopath != "" {
		dirs["GOMODCACHE"] = filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod")
	}
	// npm's default cache is ~/.npm; a sandbox HOME would redownload every
	// package a test installs into a private copy.
	if v := os.Getenv("npm_config_cache"); v != "" {
		dirs["npm_config_cache"] = v
	} else if home != "" {
		dirs["npm_config_cache"] = filepath.Join(home, ".npm")
	}
	if v := os.Getenv("GOENV"); v != "" {
		dirs["GOENV"] = v
	} else if config, err := os.UserConfigDir(); err == nil {
		dirs["GOENV"] = filepath.Join(config, "go", "env")
	}
	return dirs
}

// toolchainBin is the bin dir of the Go toolchain that built this test
// binary. Putting it ahead of the inherited PATH means a nested `go` does not
// go through scripts/bin/go, which would look for its toolchain under the
// sandboxed cache dir and download a fresh copy.
func toolchainBin() string {
	goroot := runtime.GOROOT() //nolint:staticcheck // SA1019: the build-time GOROOT is exactly the toolchain wanted here.
	if goroot == "" {
		return ""
	}
	bin := filepath.Join(goroot, "bin")
	if _, err := os.Stat(filepath.Join(bin, exeName("go"))); err != nil {
		return ""
	}
	return bin
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// writeShim installs a `name` on PATH that fails loudly, so a test that
// forgot to fake the tool cannot reach the human's credentials or the
// network. Tests that need the tool put their own fake earlier on PATH.
func writeShim(dir, name string) error {
	msg := fmt.Sprintf("testenv: the real %s is disabled in tests; put a fake %s on PATH (see backend/internal/testenv)", name, name)
	if runtime.GOOS == "windows" {
		script := "@echo off\r\necho " + msg + " 1>&2\r\nexit /b 127\r\n"
		return os.WriteFile(filepath.Join(dir, name+".cmd"), []byte(script), 0o600)
	}
	script := "#!/bin/sh\necho '" + msg + "' >&2\nexit 127\n"
	return os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700) //nolint:gosec // G306: the shim must be executable.
}
