package githubcred

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// credentialFill runs the real git credential machinery with only env added.
func credentialFill(t *testing.T, env []string, host string) (string, error) {
	t.Helper()
	return credentialFillHome(t, t.TempDir(), env, host)
}

func credentialFillHome(t *testing.T, home string, env []string, host string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", "credential", "fill")
	cmd.Dir = t.TempDir()
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"}, env...)
	cmd.Stdin = strings.NewReader("protocol=https\nhost=" + host + "\n\n")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func httpsProject(env map[string]string) domain.ProjectRecord {
	return domain.ProjectRecord{ID: "p", RepoOriginURL: "https://github.com/acme/app.git", Config: domain.ProjectConfig{GitHubUser: "acme-bot", Env: env}}
}

// Real git gets the project's account for the project's host, and the token
// never lands in the config git shows in its argv-like traces.
func TestProjectGitEnvAuthenticatesRealGitForTheProjectHostOnly(t *testing.T) {
	const token = "account-token-do-not-log"
	s, logs := newService(t, "env", httpsProject(nil), &fakeAccounts{token: token}, nil)
	env := s.ProjectGitEnv(context.Background(), "p")
	if len(env) != 2 {
		t.Fatalf("env entries = %d, want config and password", len(env))
	}
	if !strings.HasPrefix(env[0], "GIT_CONFIG_PARAMETERS=") || strings.Contains(env[0], token) {
		t.Fatalf("token must not be in git's config: %q", env[0])
	}
	out, err := credentialFill(t, env, "github.com")
	if err != nil {
		t.Fatalf("credential fill: %v\n%s", err, out)
	}
	if !strings.Contains(out, "username=x-access-token\n") || !strings.Contains(out, "password="+token+"\n") {
		t.Fatalf("git did not get the account: %s", out)
	}
	if out, err := credentialFill(t, env, "other.example.com"); err == nil || strings.Contains(out, token) {
		t.Fatalf("token offered to another host: err=%v out=%s", err, out)
	}
	if strings.Contains(logs.String(), token) {
		t.Fatalf("token logged: %s", logs.String())
	}
}

// The machine's own helper (here a stand-in for `gh auth git-credential` on the
// active account) must not answer for the project's host.
func TestProjectGitEnvReplacesTheMachineHelperForTheHost(t *testing.T) {
	home := t.TempDir()
	config := "[credential \"https://github.com\"]\n\thelper = !echo username=active && echo password=active-account-token\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := newService(t, "env", httpsProject(nil), &fakeAccounts{token: "project-account"}, nil)
	out, err := credentialFillHome(t, home, s.ProjectGitEnv(context.Background(), "p"), "github.com")
	if err != nil {
		t.Fatalf("credential fill: %v\n%s", err, out)
	}
	if strings.Contains(out, "active-account-token") || !strings.Contains(out, "password=project-account\n") {
		t.Fatalf("machine helper answered for the project's host: %s", out)
	}
}

// The credential order is the agents' order, not a new one.
func TestProjectGitEnvFollowsTheExistingCredentialOrder(t *testing.T) {
	ctx := context.Background()
	password := func(env []string) string {
		for _, e := range env {
			if v, ok := strings.CutPrefix(e, envGitPassword+"="); ok {
				return v
			}
		}
		return ""
	}
	accounts := &fakeAccounts{token: "account"}
	s, _ := newService(t, "env", httpsProject(map[string]string{"GH_TOKEN": "project"}), accounts, map[string]string{"TALLPINE_GITHUB_TOKEN": "daemon"})
	if got := password(s.ProjectGitEnv(ctx, "p")); got != "project" {
		t.Fatalf("project env token must win, got %q", got)
	}
	s, _ = newService(t, "env", httpsProject(nil), accounts, map[string]string{"TALLPINE_GITHUB_TOKEN": "daemon"})
	if got := password(s.ProjectGitEnv(ctx, "p")); got != "daemon" {
		t.Fatalf("daemon pin must beat the account, got %q", got)
	}
	s, _ = newService(t, "env", httpsProject(nil), accounts, nil)
	if got := password(s.ProjectGitEnv(ctx, "p")); got != "account" {
		t.Fatalf("account must be used last, got %q", got)
	}
}

func TestProjectGitEnvLeavesGitAloneWithoutACredential(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, "env", httpsProject(nil), &fakeAccounts{}, nil)
	if env := s.ProjectGitEnv(ctx, "p"); env != nil {
		t.Fatalf("no account: env = %d entries, want nil", len(env))
	}
	gitlab := domain.ProjectRecord{ID: "p", RepoOriginURL: "https://gitlab.com/acme/app.git", Config: domain.ProjectConfig{Env: map[string]string{"GH_TOKEN": "t"}}}
	s, _ = newService(t, "env", gitlab, &fakeAccounts{token: "t"}, nil)
	if env := s.ProjectGitEnv(ctx, "p"); env != nil {
		t.Fatalf("a GitHub token must not go to a non-GitHub host, got %d entries", len(env))
	}
	s, _ = newService(t, "env", httpsProject(nil), &fakeAccounts{token: "t"}, nil)
	if env := s.ProjectGitEnv(ctx, ""); env != nil {
		t.Fatalf("no project: env = %d entries, want nil", len(env))
	}
}

// A GIT_CONFIG_PARAMETERS the daemon already has must survive the helper.
func TestProjectGitEnvKeepsExistingGitConfigParameters(t *testing.T) {
	t.Setenv("GIT_CONFIG_PARAMETERS", "'user.name'='kept'")
	s, _ := newService(t, "env", httpsProject(nil), &fakeAccounts{token: "t"}, nil)
	env := s.ProjectGitEnv(context.Background(), "p")
	if len(env) == 0 || !strings.Contains(env[0], "'user.name'='kept'") {
		t.Fatalf("existing config lost: %v", len(env))
	}
}

// Real hooks must not run under the credential env: a hook inherits the token.
// The repository sets its own core.hooksPath, the husky case, and each control
// run proves the hook fires when the credential env is absent.
func TestProjectGitEnvKeepsTheTokenAwayFromHooks(t *testing.T) {
	const token = "account-token-for-hooks"
	s, _ := newService(t, "env", httpsProject(nil), &fakeAccounts{token: token}, nil)
	credEnv := s.ProjectGitEnv(context.Background(), "p")

	base := t.TempDir()
	baseEnv := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	}
	git := func(dir string, extra []string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(append([]string{}, baseEnv...), extra...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	origin, repo, other := filepath.Join(base, "o.git"), filepath.Join(base, "repo"), filepath.Join(base, "other")
	git(base, nil, "init", "-q", "--bare", "-b", "main", origin)
	git(base, nil, "clone", "-q", origin, other)
	git(other, nil, "checkout", "-q", "-b", "main")
	git(other, nil, "commit", "-q", "--allow-empty", "-m", "init")
	git(other, nil, "push", "-q", "origin", "main")
	git(base, nil, "clone", "-q", origin, repo)

	marker := filepath.Join(base, "hook-ran")
	hooks := filepath.Join(repo, ".husky")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat >/dev/null\necho \"${" + envGitPassword + ":-none}\" >> '" + marker + "'\n"
	for _, name := range []string{"reference-transaction", "pre-push"} {
		if err := os.WriteFile(filepath.Join(hooks, name), []byte(script), 0o755); err != nil { // #nosec G306 -- a hook must be executable.
			t.Fatal(err)
		}
	}
	git(repo, nil, "config", "core.hooksPath", ".husky")

	hookRan := func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}
	resetMarker := func() {
		t.Helper()
		if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	advance := func() string {
		t.Helper()
		git(other, nil, "commit", "-q", "--allow-empty", "-m", "next")
		git(other, nil, "push", "-q", "origin", "main")
		return git(other, nil, "rev-parse", "HEAD")
	}

	advance()
	resetMarker()
	git(repo, nil, "fetch", "-q", "origin", "main:refs/remotes/origin/main")
	if !hookRan() {
		t.Fatal("control: the fetch hook did not run, so this test proves nothing")
	}
	want := advance()
	resetMarker()
	git(repo, credEnv, "fetch", "-q", "origin", "main:refs/remotes/origin/main")
	if hookRan() {
		t.Fatal("a hook ran during an authenticated fetch and could read the token")
	}
	if got := git(repo, nil, "rev-parse", "origin/main"); got != want {
		t.Fatalf("fetch did not work: %s, want %s", got, want)
	}

	git(repo, nil, "commit", "-q", "--allow-empty", "-m", "local")
	resetMarker()
	git(repo, nil, "push", "-q", "origin", "HEAD:refs/heads/control")
	if !hookRan() {
		t.Fatal("control: the push hook did not run, so this test proves nothing")
	}
	resetMarker()
	git(repo, credEnv, "push", "-q", "origin", "HEAD:refs/heads/authenticated")
	if hookRan() {
		t.Fatal("a hook ran during an authenticated push and could read the token")
	}
	git(origin, nil, "rev-parse", "--verify", "refs/heads/authenticated")
}
