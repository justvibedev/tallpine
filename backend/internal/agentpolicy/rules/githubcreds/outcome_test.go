package githubcreds_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/githubcreds"
)

func mode(t *testing.T, m string) githubcreds.Outcome {
	t.Helper()
	return githubcreds.Of(policytest.Decision(t, map[string]map[string]any{githubcreds.Name: {githubcreds.FieldMode: m}}))
}

func TestDefaultAndZeroDecisionChangeNothing(t *testing.T) {
	for name, o := range map[string]githubcreds.Outcome{
		"zero decision": githubcreds.Of(agentpolicy.Decision{}),
		"default":       githubcreds.Of(policytest.Decision(t, nil)),
		"explicit env":  mode(t, "env"),
	} {
		if o.Mode() != githubcreds.Env || o.UsesHelper() || o.Denied() || o.KeepsTokensOutOfEnv() || o.WithheldVariables() != nil {
			t.Errorf("%s must be today's behaviour: %+v", name, o)
		}
	}
}

func TestHelperAndNoneKeepTokensOutOfTheEnvironment(t *testing.T) {
	h, n := mode(t, "helper"), mode(t, "none")
	if !h.UsesHelper() || h.Denied() || !h.KeepsTokensOutOfEnv() {
		t.Fatalf("helper: %+v", h)
	}
	if n.UsesHelper() || !n.Denied() || !n.KeepsTokensOutOfEnv() {
		t.Fatalf("none: %+v", n)
	}
	for _, o := range []githubcreds.Outcome{h, n} {
		w := o.WithheldVariables()
		for _, want := range []string{"GH_TOKEN", "GITHUB_TOKEN", "TALLPINE_GITHUB_TOKEN"} {
			if !slices.Contains(w, want) {
				t.Errorf("%s must be withheld: %v", want, w)
			}
		}
	}
}

func TestGitConfigEnvAppendsAfterTheProjectsOwnEntries(t *testing.T) {
	existing := map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.autocrlf", "GIT_CONFIG_VALUE_0": "false", "GIT_CONFIG_PARAMETERS": "'core.pager'='cat'"}
	got := githubcreds.GitConfigEnv(existing, "'/usr/bin/tallpine' git-credential", []string{"github.com", "GitHub.com", "ghe.example.com"})
	if len(got) != 1 {
		t.Fatalf("only GIT_CONFIG_PARAMETERS is written (the project's COUNT/KEY/VALUE stay untouched): %v", got)
	}
	want := `'core.pager'='cat' ` +
		`'credential.https://ghe.example.com.helper'='' ` +
		`'credential.https://ghe.example.com.helper'='!'\''/usr/bin/tallpine'\'' git-credential' ` +
		`'credential.https://github.com.helper'='' ` +
		`'credential.https://github.com.helper'='!'\''/usr/bin/tallpine'\'' git-credential'`
	if got[githubcreds.EnvGitConfigParameters] != want {
		t.Fatalf("existing parameters kept, then per host a reset and the helper (hosts de-duplicated and sorted):\n got  %s\n want %s", got[githubcreds.EnvGitConfigParameters], want)
	}
	if only := githubcreds.GitConfigEnv(nil, "x", nil); len(only) != 0 {
		t.Fatalf("no hosts, no entries: %v", only)
	}
}

// Harnesses that sanitize the environment of the commands they run drop every
// variable whose name contains KEY, TOKEN, SECRET or PASSWORD (Codex's default
// shell environment policy, DeepSeek Harness's subprocess scrub). The credential
// config must not depend on such a name: GIT_CONFIG_KEY_n used to vanish while
// GIT_CONFIG_COUNT stayed, and git then failed on every call.
var harnessScrub = regexp.MustCompile(`(?i)KEY|TOKEN|SECRET|PASSWORD`)

func TestGitConfigEnvNamesSurviveHarnessEnvironmentScrubbing(t *testing.T) {
	for name := range githubcreds.GitConfigEnv(nil, "'/usr/bin/tallpine' git-credential", []string{"github.com"}) {
		if harnessScrub.MatchString(name) {
			t.Errorf("%s would be dropped by a harness's credential scrub", name)
		}
	}
}

func TestHostOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/o/r.git": "github.com", "git@github.com:o/r.git": "github.com",
		"ssh://git@ghe.example.com/o/r": "ghe.example.com", "git@github-xyz:acme/app.git": "github-xyz", "": "", "/local/path": "",
	} {
		if got := githubcreds.HostOf(in); got != want {
			t.Errorf("HostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// The encoding must be something real git honours: ask git itself which helper
// it would run for github.com, with the user's global helper in play.
func TestGitConfigEnvIsHonouredByRealGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh helper")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper.sh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ncat >/dev/null\nprintf 'username=x-access-token\\npassword=from-daemon\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(global, []byte("[credential]\n\thelper = !printf 'username=user\\\\npassword=keychain-token\\\\n'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_GLOBAL=" + global, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"}
	for k, v := range githubcreds.GitConfigEnv(nil, "'"+helper+"'", []string{"github.com"}) {
		if harnessScrub.MatchString(k) {
			continue // what a harness's environment scrub does to the variable
		}
		env = append(env, k+"="+v)
	}
	run := func(host string) string {
		cmd := exec.Command(git, "credential", "fill")
		cmd.Env = env
		cmd.Stdin = strings.NewReader("protocol=https\nhost=" + host + "\n\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git credential fill %s: %v\n%s", host, err, out)
		}
		return string(out)
	}
	if out := run("github.com"); !strings.Contains(out, "password=from-daemon") || strings.Contains(out, "keychain-token") {
		t.Fatalf("github.com must use only our helper, not the user's keychain one:\n%s", out)
	}
	if out := run("gitlab.example.com"); !strings.Contains(out, "keychain-token") {
		t.Fatalf("other hosts keep their own helpers:\n%s", out)
	}
}
