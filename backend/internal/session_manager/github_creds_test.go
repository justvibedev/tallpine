package sessionmanager

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// credentialShapedName is what Codex's default shell environment policy and
// DeepSeek Harness's subprocess scrub drop from the commands they run.
var credentialShapedName = regexp.MustCompile(`(?i)KEY|TOKEN|SECRET|PASSWORD`)

func githubCredsManager(t *testing.T, mode string, tokens GitHubAccountTokens) *Manager {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the unix shim and sh-based assertions do not run on Windows")
	}
	m := githubEnvManager(t, tokens)
	m.dataDir = t.TempDir()
	m.executable = func() (string, error) { return "/opt/tallpine bin/tallpine", nil }
	engine, _ := policytest.Engine(t, map[string]map[string]any{"githubCreds": {"mode": mode}})
	m.SetAgentPolicy(engine)
	return m
}

func TestHelperModeKeepsTokensOutOfTheSessionEnvironment(t *testing.T) {
	f := &fakeGitHubTokens{token: "picked-token"}
	m := githubCredsManager(t, "helper", f)
	env := m.runtimeEnv("mer-1", "mer", "", map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.autocrlf", "GIT_CONFIG_VALUE_0": "false"})
	if _, ok := env["GH_TOKEN"]; ok {
		t.Fatal("no token in the environment")
	}
	for _, v := range env {
		if strings.Contains(v, "picked-token") {
			t.Fatal("the account token must not appear anywhere in the session environment")
		}
	}
	if got := agentlaunch.UnsetNames(env); len(got) < 3 {
		t.Fatalf("the daemon's own GitHub tokens must be withheld from the agent: %v", got)
	}
	dir := env["GH_CONFIG_DIR"]
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 || dir == "" {
		t.Fatalf("an empty per-session gh config dir (no stored login of the user; no file for a harness the shim always wins for): %q %v %v", dir, entries, err)
	}
	params := env["GIT_CONFIG_PARAMETERS"]
	if env["GIT_CONFIG_KEY_0"] != "core.autocrlf" || env["GIT_CONFIG_COUNT"] != "1" ||
		!strings.Contains(params, `'credential.https://github.com.helper'='!'\''/opt/tallpine bin/tallpine'\'' git-credential'`) ||
		!strings.Contains(params, `'credential.https://github-xyz.helper'=''`) {
		t.Fatalf("the project's own git config survives and the helper is added per host: %v", env)
	}
	// A harness that drops credential-shaped variable names (Codex, DeepSeek
	// Harness) must not be able to strip half of the helper config: nothing the
	// policy adds may depend on such a name.
	for k := range env {
		if strings.HasPrefix(k, "GIT_CONFIG_") && k != "GIT_CONFIG_COUNT" && k != "GIT_CONFIG_KEY_0" && k != "GIT_CONFIG_VALUE_0" && credentialShapedName.MatchString(k) {
			t.Fatalf("%s would be dropped by a harness's credential scrub", k)
		}
	}
	shim := env[agentlaunch.EnvGHShimDir]
	script, err := os.ReadFile(filepath.Join(shim, "gh"))
	if err != nil || !strings.Contains(string(script), "'/opt/tallpine bin/tallpine' gh-exec -- \"$@\"") {
		t.Fatalf("shim = %q %v", script, err)
	}
	if info, _ := os.Stat(filepath.Join(shim, "gh")); info == nil || info.Mode()&0o100 == 0 {
		t.Fatal("the shim must be executable")
	}
	if !strings.HasPrefix(env["PATH"], shim+string(os.PathListSeparator)) {
		t.Fatalf("the shim dir must be first on PATH: %s", env["PATH"])
	}
}

func TestHelperModeKeepsAProjectsExplicitTokenAndConfigDir(t *testing.T) {
	m := githubCredsManager(t, "helper", &fakeGitHubTokens{token: "picked"})
	env := m.runtimeEnv("mer-1", "mer", "", map[string]string{"GH_TOKEN": "project-token", "GH_CONFIG_DIR": "/p/gh"})
	if env["GH_TOKEN"] != "project-token" || env["GH_CONFIG_DIR"] != "/p/gh" {
		t.Fatalf("an explicit project choice keeps working in helper mode: %v", env)
	}
	facts := githubFacts(m.decideFor(t.Context(), domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}), map[string]string{"GH_TOKEN": "project-token"})
	if facts["githubCreds"]["explicitProjectToken"] != "GH_TOKEN" {
		t.Fatalf("the decision record names the explicit token variable: %v", facts)
	}
	for _, v := range facts["githubCreds"] {
		if v == "project-token" {
			t.Fatal("the record must hold the variable name, never the value")
		}
	}
	if githubFacts(m.decideFor(t.Context(), domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}), nil) != nil {
		t.Fatal("no explicit token, nothing to record")
	}
}

// The gh shim must be first on PATH to authenticate, but the shells Codex and
// Kiro run re-prepend the real gh (rc files). That gh reads this file instead.
func TestHelperModeWritesAPrivateGHLoginForTheRealGH(t *testing.T) {
	f := &fakeGitHubTokens{token: "picked-token"}
	m := githubCredsManager(t, "helper", f)
	seedSessionHarness(t, m, "mer-1", domain.HarnessCodex)
	seedSessionHarness(t, m, "mer-2", domain.HarnessKiro)
	env := m.runtimeEnv("mer-1", "mer", "", nil)
	dir := env["GH_CONFIG_DIR"]
	raw, err := os.ReadFile(filepath.Join(dir, "hosts.yml"))
	if err != nil {
		t.Fatalf("hosts.yml: %v", err)
	}
	for _, want := range []string{"github.com:", "oauth_token: 'picked-token'", "git_protocol: https"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("hosts.yml lacks %q:\n%s", want, raw)
		}
	}
	if info, _ := os.Stat(filepath.Join(dir, "hosts.yml")); info == nil || info.Mode().Perm() != 0o600 {
		t.Errorf("hosts.yml must be 0600: %v", info)
	}
	if info, _ := os.Stat(dir); info == nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the gh config dir must be 0700: %v", info)
	}
	if !strings.HasPrefix(dir, m.dataDir) {
		t.Errorf("the login must live under the session's Tallpine data dir: %s", dir)
	}
	for k, v := range env {
		if strings.Contains(v, "picked-token") {
			t.Fatalf("the token must stay out of the environment: %s", k)
		}
	}
	// A rotated-away account leaves no stale login behind.
	f.token = ""
	m.runtimeEnv("mer-1", "mer", "", nil)
	if _, err := os.Stat(filepath.Join(dir, "hosts.yml")); err == nil {
		t.Fatal("hosts.yml must be removed when the account has no token")
	}
	// Another session gets its own directory.
	f.token = "picked-token"
	if other := m.runtimeEnv("mer-2", "mer", "", nil)["GH_CONFIG_DIR"]; other == dir {
		t.Fatal("sessions must not share a gh config dir")
	}
}

// The session temp dir outlives a relaunch. A login written under helper mode
// must be gone when the same session relaunches under none (a managed push).
func TestRelaunchUnderNoneRemovesTheGHLogin(t *testing.T) {
	f := &fakeGitHubTokens{token: "picked-token"}
	m := githubCredsManager(t, "helper", f)
	seedSessionHarness(t, m, "mer-1", domain.HarnessCodex)
	dir := m.runtimeEnv("mer-1", "mer", "", nil)["GH_CONFIG_DIR"]
	if _, err := os.Stat(filepath.Join(dir, "hosts.yml")); err != nil {
		t.Fatalf("helper mode must write the login first: %v", err)
	}
	engine, _ := policytest.Engine(t, map[string]map[string]any{"githubCreds": {"mode": "none"}})
	m.SetAgentPolicy(engine)
	again := m.runtimeEnv("mer-1", "mer", "", nil)["GH_CONFIG_DIR"]
	if again != dir {
		t.Fatalf("the relaunch reuses the session's directory: %q vs %q", again, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "hosts.yml")); err == nil {
		t.Fatal("mode none must not leave the old login behind")
	}
}

// Claude Code keeps the PATH the daemon built, so its shim wins and the token
// need not be at rest in a file.
func TestClaudeCodeGetsNoGHLoginFile(t *testing.T) {
	m := githubCredsManager(t, "helper", &fakeGitHubTokens{token: "picked-token"})
	seedSessionHarness(t, m, "mer-1", domain.HarnessClaudeCode)
	dir := m.runtimeEnv("mer-1", "mer", "", nil)["GH_CONFIG_DIR"]
	if _, err := os.Stat(filepath.Join(dir, "hosts.yml")); err == nil {
		t.Fatal("a harness whose shim always wins gets no token at rest")
	}
}

func seedSessionHarness(t *testing.T, m *Manager, id domain.SessionID, h domain.AgentHarness) {
	t.Helper()
	st, ok := m.store.(*fakeStore)
	if !ok {
		t.Fatalf("store is %T", m.store)
	}
	st.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "mer", Harness: h}
}

func TestNoneModeAndExplicitProjectTokenWriteNoGHLogin(t *testing.T) {
	none := githubCredsManager(t, "none", &fakeGitHubTokens{token: "picked"})
	if dir := none.runtimeEnv("mer-1", "mer", "", nil)["GH_CONFIG_DIR"]; dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "hosts.yml")); err == nil {
			t.Fatal("mode none must not give gh a login")
		}
	}
	helper := githubCredsManager(t, "helper", &fakeGitHubTokens{token: "picked"})
	seedSessionHarness(t, helper, "mer-1", domain.HarnessCodex)
	env := helper.runtimeEnv("mer-1", "mer", "", map[string]string{"GH_TOKEN": "project-token"})
	if _, err := os.Stat(filepath.Join(env["GH_CONFIG_DIR"], "hosts.yml")); err == nil {
		t.Fatal("a token the project's environment sets keeps winning; no file")
	}
}

func TestDaemonPinnedTokenKeepsTheBroaderAccountTokenOutOfTheGHLogin(t *testing.T) {
	m := githubCredsManager(t, "helper", &fakeGitHubTokens{token: "broad-account"})
	t.Setenv("TALLPINE_GITHUB_TOKEN", "narrow-daemon-pin") // after the manager: its helper blanks the explicit vars
	seedSessionHarness(t, m, "mer-1", domain.HarnessCodex)
	env := m.runtimeEnv("mer-1", "mer", "", nil)
	b, err := os.ReadFile(filepath.Join(env["GH_CONFIG_DIR"], "hosts.yml"))
	if err != nil || !strings.Contains(string(b), "narrow-daemon-pin") || strings.Contains(string(b), "broad-account") {
		t.Fatalf("the real gh must get the daemon-pinned token, never the broader account token: err=%v\n%s", err, b)
	}
}

func TestNoneModeHasNoHelperNoShimAndNoTokens(t *testing.T) {
	m := githubCredsManager(t, "none", &fakeGitHubTokens{token: "picked"})
	env := m.runtimeEnv("mer-1", "mer", "", nil)
	if _, ok := env["GH_TOKEN"]; ok || env[agentlaunch.EnvGHShimDir] != "" || env["GH_CONFIG_DIR"] == "" {
		t.Fatalf("%v", env)
	}
	for k, v := range env {
		if strings.HasPrefix(k, "GIT_CONFIG_") && strings.Contains(v, "git-credential") {
			t.Fatalf("mode none must not install a helper: %s=%s", k, v)
		}
	}
	if env["GIT_CONFIG_PARAMETERS"] == "" {
		t.Fatal("mode none still resets other helpers for the GitHub hosts, so the user's keychain token is not offered")
	}
}

func TestHelperModeFailsClosedWhenTheDaemonExecutableIsUnknown(t *testing.T) {
	m := githubCredsManager(t, "helper", &fakeGitHubTokens{token: "picked"})
	m.executable = func() (string, error) { return "", errors.New("no executable") }
	env := m.runtimeEnv("mer-1", "mer", "", nil)
	if _, ok := env["GH_TOKEN"]; ok || env[agentlaunch.EnvGHShimDir] != "" {
		t.Fatalf("with no helper to install, the session gets no token either: %v", env)
	}
}

func TestEnvModeIsUnchangedByThePolicyEngine(t *testing.T) {
	f := &fakeGitHubTokens{token: "picked-token"}
	m := githubCredsManager(t, "env", f)
	env := m.runtimeEnv("mer-1", "mer", "", nil)
	if env["GH_TOKEN"] != "picked-token" || env["GH_CONFIG_DIR"] != "" || env[agentlaunch.EnvUnset] != "" || env[agentlaunch.EnvGHShimDir] != "" {
		t.Fatalf("mode env must behave as before: %v", env)
	}
}

// End to end through the pieces the production path uses: the env the session
// manager builds, run by real git and the real shim script, with a stand-in `tallpine`
// that answers like `tallpine git-credential` / `tallpine gh-exec` would. The executable's
// path contains a space to prove the quoting git's shell invocation needs.
func TestHelperModeEnvironmentWorksWithRealGitAndTheShim(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	m := githubCredsManager(t, "helper", &fakeGitHubTokens{token: "picked"})
	binDir := filepath.Join(t.TempDir(), "tallpine bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fakeVO := filepath.Join(binDir, "tallpine")
	script := `#!/bin/sh
case "$1" in
  git-credential)
    cat >/dev/null
    printf 'username=x-access-token\npassword=from-daemon\n\n' ;;
  gh-exec)
    shift; shift
    echo "gh-exec args: $* shim=$TALLPINE_GH_SHIM_DIR" ;;
esac
`
	if err := os.WriteFile(fakeVO, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	m.executable = func() (string, error) { return fakeVO, nil }
	env := m.runtimeEnv("mer-1", "mer", "", nil)

	home := t.TempDir()
	global := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(global, []byte("[credential]\n\thelper = !printf 'username=u\\\\npassword=keychain-token\\\\n'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environ := []string{"PATH=" + env["PATH"], "HOME=" + home, "GIT_CONFIG_GLOBAL=" + global, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"}
	for k, v := range env {
		// Apply what the harnesses' environment scrub does: drop credential-shaped names.
		if strings.HasPrefix(k, "GIT_CONFIG_") && !credentialShapedName.MatchString(k) {
			environ = append(environ, k+"="+v)
		}
	}
	fill := exec.Command(git, "credential", "fill")
	fill.Env = environ
	fill.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")
	out, err := fill.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "password=from-daemon") || strings.Contains(string(out), "keychain-token") {
		t.Fatalf("git must get the daemon's token through the helper, not the user's keychain one: %v\n%s", err, out)
	}

	sh := exec.Command("/bin/sh", "-c", "gh pr view 1")
	sh.Env = append(environ, "TALLPINE_GH_SHIM_DIR="+env[agentlaunch.EnvGHShimDir])
	shOut, err := sh.CombinedOutput()
	if err != nil || !strings.Contains(string(shOut), "gh-exec args: pr view 1") {
		t.Fatalf("`gh` must resolve to the shim, which runs `tallpine gh-exec -- <args>`: %v\n%s", err, shOut)
	}
}

// Windows has no helper or shim yet, so helper mode keeps the token in the
// environment there. Mode none is an explicit deny and must never gain a token,
// whatever the OS. The OS is injected so this runs on every host.
func TestWindowsFallbackAppliesToHelperOnlyNeverToNone(t *testing.T) {
	for _, tc := range []struct {
		mode         string
		wantToken    bool
		wantWithheld bool
	}{{"helper", true, false}, {"none", false, true}} {
		f := &fakeGitHubTokens{token: "picked-token"}
		m := githubCredsManager(t, tc.mode, f)
		m.goos = "windows"
		env := m.runtimeEnv("mer-1", "mer", "", nil)
		if got := env["GH_TOKEN"] == "picked-token"; got != tc.wantToken {
			t.Errorf("mode %s on windows: token in env = %v, want %v", tc.mode, got, tc.wantToken)
		}
		if got := len(agentlaunch.UnsetNames(env)) > 0; got != tc.wantWithheld {
			t.Errorf("mode %s on windows: daemon tokens withheld = %v, want %v", tc.mode, got, tc.wantWithheld)
		}
	}
}
