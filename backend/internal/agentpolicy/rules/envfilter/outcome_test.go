package envfilter_test

import (
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/envfilter"
)

func outcome(t *testing.T, mode string, passthrough ...string) envfilter.Outcome {
	t.Helper()
	fields := map[string]any{envfilter.FieldMode: mode}
	if passthrough != nil {
		fields[envfilter.FieldPassthrough] = passthrough
	}
	return envfilter.Of(policytest.Decision(t, map[string]map[string]any{envfilter.Name: fields}))
}

func keys(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		k, _, _ := strings.Cut(e, "=")
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

var daemonEnv = []string{
	"PATH=/usr/bin", "HOME=/home/u", "LANG=en_US.UTF-8", "LC_ALL=C", "SHELL=/bin/zsh", "USER=u",
	"SSH_AUTH_SOCK=/tmp/agent", "HTTPS_PROXY=http://proxy", "no_proxy=localhost",
	"GOPATH=/go", "ANTHROPIC_API_KEY=k", "AWS_SECRET_ACCESS_KEY=s",
	"GH_TOKEN=gh", "GITHUB_TOKEN=gh2", "TALLPINE_GITHUB_TOKEN=gh3",
	"TALLPINE_SESSION_TOKEN=tok", "DATABASE_URL=postgres://secret", "STRIPE_KEY=sk", "MY_CORP_VAR=1",
	"GIT_CONFIG_COUNT=1", "GIT_AUTHOR_NAME=a", "NODE_OPTIONS=--inspect",
}

func TestDefaultAndZeroDecisionInheritEverything(t *testing.T) {
	for name, o := range map[string]envfilter.Outcome{
		"zero decision": envfilter.Of(agentpolicy.Decision{}),
		"default":       envfilter.Of(policytest.Decision(t, nil)),
		"explicit":      outcome(t, "inherit"),
	} {
		if o.Allowlist() {
			t.Errorf("%s must inherit the daemon environment", name)
		}
	}
}

func TestAllowlistKeepsToolchainAndHarnessLoginDropsSecrets(t *testing.T) {
	res := outcome(t, "allowlist").Filter(daemonEnv, false, nil)
	kept := keys(res.Keep)
	for _, want := range []string{"PATH", "HOME", "LANG", "LC_ALL", "SHELL", "USER", "SSH_AUTH_SOCK", "HTTPS_PROXY", "no_proxy", "GOPATH", "ANTHROPIC_API_KEY", "AWS_SECRET_ACCESS_KEY", "GIT_AUTHOR_NAME"} {
		if !slices.Contains(kept, want) {
			t.Errorf("%s must survive the allowlist, got %v", want, kept)
		}
	}
	for _, gone := range []string{"DATABASE_URL", "STRIPE_KEY", "MY_CORP_VAR", "GH_TOKEN", "GITHUB_TOKEN", "TALLPINE_GITHUB_TOKEN", "TALLPINE_SESSION_TOKEN", "GIT_CONFIG_COUNT", "NODE_OPTIONS"} {
		if slices.Contains(kept, gone) || !slices.Contains(res.Dropped, gone) {
			t.Errorf("%s must be dropped and reported by name", gone)
		}
	}
	if !slices.IsSorted(res.Dropped) {
		t.Errorf("dropped names are sorted: %v", res.Dropped)
	}
}

func TestPassthroughNamesAndPrefixesButNeverTheSessionToken(t *testing.T) {
	res := outcome(t, "allowlist", "MY_CORP_*", "DATABASE_URL", "TALLPINE_SESSION_TOKEN").Filter(daemonEnv, false, nil)
	kept := keys(res.Keep)
	if !slices.Contains(kept, "MY_CORP_VAR") || !slices.Contains(kept, "DATABASE_URL") {
		t.Fatalf("passthrough names and prefixes must be kept: %v", kept)
	}
	if slices.Contains(kept, "TALLPINE_SESSION_TOKEN") {
		t.Fatal("a daemon session token is never passed, even when listed")
	}
	if slices.Contains(kept, "STRIPE_KEY") {
		t.Fatal("an unlisted secret leaked")
	}
}

func TestWithheldNamesStayOutEvenIfPassedThrough(t *testing.T) {
	res := outcome(t, "allowlist", "GH_TOKEN", "GITHUB_*").Filter(daemonEnv, false, []string{"GH_TOKEN", "GITHUB_TOKEN"})
	kept := keys(res.Keep)
	if slices.Contains(kept, "GH_TOKEN") || slices.Contains(kept, "GITHUB_TOKEN") {
		t.Fatalf("the githubCreds rule's withheld tokens must stay out: %v", kept)
	}
	if res2 := outcome(t, "allowlist", "GH_TOKEN").Filter(daemonEnv, false, nil); !slices.Contains(keys(res2.Keep), "GH_TOKEN") {
		t.Fatal("with no withholding, an explicit passthrough of GH_TOKEN works")
	}
}

func TestWindowsNamesAreCaseInsensitive(t *testing.T) {
	res := outcome(t, "allowlist").Filter([]string{"Path=C:\\x", "SystemRoot=C:\\Windows", "Secret=1"}, true, nil)
	if k := keys(res.Keep); !slices.Contains(k, "Path") || !slices.Contains(k, "SystemRoot") || slices.Contains(k, "Secret") {
		t.Fatalf("%v", k)
	}
	if res := outcome(t, "allowlist").Filter([]string{"path=/x"}, false, nil); len(res.Keep) != 0 {
		t.Fatalf("POSIX names are case-sensitive: %v", res.Keep)
	}
}

func TestFactsAreNamesAndCountsCapped(t *testing.T) {
	o := outcome(t, "allowlist")
	var environ []string
	for i := 0; i < 300; i++ {
		environ = append(environ, "ZZ_VAR_"+strings.Repeat("x", 3)+string(rune('a'+i%26))+strings.Repeat("y", i/26)+"=secret-value")
	}
	res := o.Filter(append(environ, "PATH=/bin"), false, nil)
	facts := o.Facts(res)
	names, _ := facts["droppedNames"].([]string)
	if facts["keptCount"] != 1 || facts["droppedCount"] != len(res.Dropped) || len(names) > 200 {
		t.Fatalf("%v", facts)
	}
	for _, n := range names {
		if strings.Contains(n, "secret-value") || strings.Contains(n, "=") {
			t.Fatalf("facts must carry names only: %s", n)
		}
	}
}

func TestPassthroughValidation(t *testing.T) {
	e, _ := policytest.Engine(t, nil)
	_ = e
	for _, bad := range []string{"has space", "1BAD", "A-B", "", "*", "A**"} {
		d := policytest.Decision(t, map[string]map[string]any{envfilter.Name: {envfilter.FieldMode: "allowlist", envfilter.FieldPassthrough: []string{bad}}})
		if len(d.Warnings()) == 0 {
			t.Errorf("%q must be reported as inconsistent", bad)
		}
	}
	d := policytest.Decision(t, map[string]map[string]any{envfilter.Name: {envfilter.FieldPassthrough: []string{"GOOD", "PRE_*", "PROGRAMFILES(X86)"}}})
	if len(d.Warnings()) != 0 {
		t.Fatalf("valid names: %v", d.Warnings())
	}
}

func TestNonIdentifierNamesAreDroppedOnPosixButKeptOnWindows(t *testing.T) {
	out := outcome(t, "allowlist", "A(b)", "ProgramFiles(x86)", "OK_VAR")
	env := []string{"A(b)=1", "ProgramFiles(x86)=3", "OK_VAR=4", "PATH=/bin"}
	posix := out.Filter(env, false, nil)
	if got := keys(posix.Keep); !slices.Equal(got, []string{"OK_VAR", "PATH"}) {
		t.Fatalf("posix keep = %v", got)
	}
	if !slices.Equal(posix.Dropped, []string{"A(b)", "ProgramFiles(x86)"}) {
		t.Fatalf("posix dropped = %v", posix.Dropped)
	}
	if win := out.Filter(env, true, nil); len(win.Keep) != 4 {
		t.Fatalf("windows keep = %v", win.Keep)
	}
}
