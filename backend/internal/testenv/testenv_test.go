package testenv

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { Main(m) }

func TestScrubbed(t *testing.T) {
	for name, want := range map[string]bool{
		"TALLPINE_SESSION_ID":                   true,
		"TALLPINE_TMUX_SOCKET_NAME":             true,
		"TALLPINE_DATA_DIR":                     true,
		"TALLPINE_WAKE_HELPER":                  true,
		"AO_SESSION_ID":                         true,
		"CLAUDECODE":                            true,
		"CLAUDE_CODE_SESSION_ID":                true,
		"ANTHROPIC_API_KEY":                     true,
		"CODEX_HOME":                            true,
		"TMUX":                                  true,
		"TMUX_PANE":                             true,
		"GH_TOKEN":                              true,
		"GITHUB_TOKEN":                          true,
		"GIT_DIR":                               true,
		"HISTFILE":                              true,
		"ZDOTDIR":                               true,
		"TALLPINE_TEST_PERSISTENT_ACP_PROVIDER": false,
		"TALLPINE_LIVE_CLAUDE_ACP":              false,
		"TALLPINE_CODEX_LIVE":                   false,
		"TALLPINE_SOCKET_ALIAS_ROOT":            false, // B463: the fenced gate's alias dir must reach the helpers
		"TALLPINE_CHAT_HOST_ACP_HELPER":         false,
		"GO_WANT_GITWORKTREE_EXIT":              false,
		"PATH":                                  false,
		"TMPDIR":                                false,
		"GOFLAGS":                               false,
		"LANG":                                  false,
	} {
		if got := scrubbed(name); got != want {
			t.Errorf("scrubbed(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestSetupInChild re-runs this binary without the active marker, as `go
// test` would start it from an agent session, and checks what a test sees.
func TestSetupInChild(t *testing.T) {
	if os.Getenv("TALLPINE_TEST_TESTENV_REPORT") != "" {
		return // the helper run below
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	realHome := t.TempDir()
	cmd := exec.Command(exe, "-test.run=^TestReportEnv$", "-test.v")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + realHome,
		"TMPDIR=" + os.TempDir(),
		"TALLPINE_SESSION_ID=agent-1",
		"TALLPINE_TMUX_SOCKET_NAME=tallpine",
		"TALLPINE_DATA_DIR=" + filepath.Join(realHome, ".tallpine"),
		"CLAUDECODE=1",
		"GH_TOKEN=secret",
		"HISTFILE=" + filepath.Join(realHome, ".bash_history"),
		"TALLPINE_TEST_TESTENV_REPORT=1",
		"TALLPINE_LIVE_CLAUDE_ACP=1",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	got := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		if line, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "ENV "); ok {
			name, value, _ := strings.Cut(line, "=")
			got[name] = value
		}
	}
	for _, name := range []string{"TALLPINE_SESSION_ID", "TALLPINE_TMUX_SOCKET_NAME", "TALLPINE_DATA_DIR", "CLAUDECODE", "GH_TOKEN"} {
		if v, ok := got[name]; !ok || v != "" {
			t.Errorf("%s = %q in the child, want unset (report: %v)", name, v, got)
		}
	}
	if got["TALLPINE_LIVE_CLAUDE_ACP"] != "1" {
		t.Errorf("TALLPINE_LIVE_CLAUDE_ACP = %q, want the opt-in kept", got["TALLPINE_LIVE_CLAUDE_ACP"])
	}
	if home := got["HOME"]; home == realHome || !strings.HasPrefix(home, os.TempDir()) {
		t.Errorf("HOME = %q, want a sandbox under %s", home, os.TempDir())
	}
	if got["HISTFILE"] != os.DevNull {
		t.Errorf("HISTFILE = %q, want %s", got["HISTFILE"], os.DevNull)
	}
	if !strings.HasPrefix(got["XDG_CONFIG_HOME"], got["HOME"]) {
		t.Errorf("XDG_CONFIG_HOME = %q, want inside HOME %q", got["XDG_CONFIG_HOME"], got["HOME"])
	}
	if want := filepath.Join(realHome, "Library", "Caches", "go-build"); runtime.GOOS == "darwin" && got["GOCACHE"] != want {
		t.Errorf("GOCACHE = %q, want the pre-sandbox default %q", got["GOCACHE"], want)
	}
	// B21: the caches stay machine-wide. A copy inside the scratch HOME is
	// re-downloaded and re-built per run and left behind on the disk.
	for _, name := range []string{"GOCACHE", "GOMODCACHE", "GOPATH", "npm_config_cache"} {
		v := got[name]
		if v == "" {
			t.Errorf("%s is unset in the child; it would default to a path inside the scratch HOME", name)
		} else if strings.HasPrefix(v, got["HOME"]) {
			t.Errorf("%s = %q is inside the scratch HOME %q", name, v, got["HOME"])
		}
	}
	if want := filepath.Join(realHome, "go", "pkg", "mod"); got["GOMODCACHE"] != want {
		t.Errorf("GOMODCACHE = %q, want the pre-sandbox default %q", got["GOMODCACHE"], want)
	}
	if want := filepath.Join(realHome, ".npm"); got["npm_config_cache"] != want {
		t.Errorf("npm_config_cache = %q, want %q", got["npm_config_cache"], want)
	}
	if got["GH_EXIT"] != "127" {
		t.Errorf("gh exit = %q, want 127 from the shim", got["GH_EXIT"])
	}
	if _, err := os.Stat(filepath.Join(realHome, ".bash_history")); err == nil {
		t.Error("the child wrote to the inherited HOME")
	}
	if dir := got["HOME"]; dir != "" {
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("sandbox %s survived the child's exit", dir)
		}
	}
}

func TestReportEnv(t *testing.T) {
	if os.Getenv("TALLPINE_TEST_TESTENV_REPORT") == "" {
		t.Skip("helper for TestSetupInChild")
	}
	for _, name := range []string{
		"TALLPINE_SESSION_ID", "TALLPINE_TMUX_SOCKET_NAME", "TALLPINE_DATA_DIR", "CLAUDECODE", "GH_TOKEN",
		"TALLPINE_LIVE_CLAUDE_ACP", "HOME", "HISTFILE", "XDG_CONFIG_HOME", "GOCACHE", "GOMODCACHE", "GOPATH", "npm_config_cache",
	} {
		fmt.Printf("ENV %s=%s\n", name, os.Getenv(name))
	}
	code := "0"
	if err := exec.Command("gh", "auth", "status").Run(); err != nil {
		code = "start-failed"
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = strconv.Itoa(exitErr.ExitCode())
		}
	}
	fmt.Printf("ENV GH_EXIT=%s\n", code)
}

// TestTestMainsUseTestenv keeps every backend test package hermetic: each
// directory with tests must call testenv.Main (or Setup) from its TestMain.
func TestTestMainsUseTestenv(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	hooked := map[string]bool{}
	var dirs []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		if _, seen := hooked[dir]; !seen {
			hooked[dir] = false
			dirs = append(dirs, dir)
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(src), "testenv.Main(m)") || strings.Contains(string(src), "testenv.Setup()") ||
			(dir == filepath.Join(root, "internal", "testenv") && strings.Contains(string(src), "Main(m)")) {
			hooked[dir] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		if !hooked[dir] {
			rel, _ := filepath.Rel(root, dir)
			t.Errorf("%s has tests but no TestMain calling testenv; add a testmain_test.go:\n\n\tfunc TestMain(m *testing.M) { testenv.Main(m) }", rel)
		}
	}
}

// sharedGoroot asks scripts/go for the machine's shared toolchain. The sandbox
// points XDG_CACHE_HOME at itself and scripts/go installs wherever that points,
// so inheriting it would download and unpack a whole Go toolchain (some 15,000
// files) on every test run; that was B602's largest single source of FSEvents
// traffic from tests.
func sharedGoroot(t *testing.T, script string) string {
	t.Helper()
	cmd := exec.Command(script, "--goroot")
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "XDG_CACHE_HOME=") })
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		t.Skip("pinned toolchain is not available")
	}
	goroot := strings.TrimSpace(string(out))
	if home := os.Getenv("HOME"); strings.HasPrefix(goroot, home+string(filepath.Separator)) {
		t.Fatalf("scripts/go resolved the toolchain inside the test sandbox: %s", goroot)
	}
	return goroot
}

// TestScriptsGoSharesCachesUnderScratchHome runs scripts/go the way workers do
// (`env -i HOME=$(mktemp -d) scripts/go ...`): its GOCACHE, GOMODCACHE and npm
// cache must resolve to the shared home, never the scratch HOME. The shared
// home is pinned with TALLPINE_REAL_HOME (a stand-in with the toolchain linked in),
// so the result does not depend on the passwd entry or on GOCACHE/GOMODCACHE
// being set in the developer's shell. Both the unset and the explicit case run.
func TestScriptsGoSharesCachesUnderScratchHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts/go is a POSIX shell script")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skip("scripts/go not present")
	}
	toolchains := filepath.Dir(sharedGoroot(t, script))
	shared := t.TempDir()
	cacheRel := filepath.Join(".cache")
	if runtime.GOOS == "darwin" {
		cacheRel = filepath.Join("Library", "Caches")
	}
	link := filepath.Join(shared, cacheRel, "vo-dev", "toolchains")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(toolchains, link); err != nil {
		t.Fatal(err)
	}
	explicitCache, explicitMod := filepath.Join(t.TempDir(), "gocache"), filepath.Join(t.TempDir(), "gomod")
	for name, tc := range map[string]struct {
		env             []string
		wantCache, want string
	}{
		"unset": {nil, filepath.Join(shared, cacheRel, "go-build"), filepath.Join(shared, "go", "pkg", "mod")},
		"set":   {[]string{"GOCACHE=" + explicitCache, "GOMODCACHE=" + explicitMod}, explicitCache, explicitMod},
	} {
		t.Run(name, func(t *testing.T) {
			scratch := t.TempDir()
			cmd := exec.Command(script, "env", "GOCACHE", "GOMODCACHE")
			cmd.Env = append([]string{"HOME=" + scratch, "PATH=" + os.Getenv("PATH"), "TALLPINE_REAL_HOME=" + shared}, tc.env...)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("scripts/go env: %v", err)
			}
			// Telemetry must be off under a scratch HOME, or Go's background writer
			// keeps filling it after scripts/go exits and races TempDir cleanup.
			tel := exec.Command(script, "telemetry")
			tel.Env = cmd.Env
			telOut, err := tel.Output()
			if err != nil || !strings.HasPrefix(strings.TrimSpace(string(telOut)), "off") {
				t.Errorf("scripts/go telemetry = %q (err %v), want off", telOut, err)
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) != 2 {
				t.Fatalf("unexpected output %q", out)
			}
			for i, w := range []string{tc.wantCache, tc.want} {
				if lines[i] != w {
					t.Errorf("line %d = %q, want %q (scratch HOME %s)", i, lines[i], w, scratch)
				}
			}
		})
	}
}

// With the real HOME, scripts/go must leave Go's own resolution alone, so a
// developer's `go env -w GOMODCACHE=...` (the GOENV file) still applies.
func TestScriptsGoHonorsGoenvWithRealHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts/go is a POSIX shell script")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "go"))
	if err != nil {
		t.Fatal(err)
	}
	sharedGoroot(t, script)
	want := filepath.Join(t.TempDir(), "modcache")
	goenv := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(goenv, []byte("GOMODCACHE="+want+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string][]string{
		"real HOME":    {"HOME=" + home},
		"scratch HOME": {"HOME=" + t.TempDir()},
	} {
		cmd := exec.Command(script, "env", "GOMODCACHE")
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "GOENV=" + goenv}, env...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("%s: GOMODCACHE = %q, want the GOENV value %q", name, got, want)
		}
	}
}
