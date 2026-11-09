package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/domain"
)

const reviewerGitHubRemote = "https://github.com/o/private.git"

type fakeGitHubAccounts struct {
	token            string
	err              error
	calls            int
	remote, override string
}

func (f *fakeGitHubAccounts) TokenForProject(_ context.Context, remote, override string) (string, error) {
	f.calls++
	f.remote, f.override = remote, override
	return f.token, f.err
}

type fakeGitHubProjects struct{ record domain.ProjectRecord }

func (f fakeGitHubProjects) GetProject(context.Context, string) (domain.ProjectRecord, bool, error) {
	return f.record, f.record.RepoOriginURL != "", nil
}

func projectOnPrivateRepo(githubUser string) fakeGitHubProjects {
	record := domain.ProjectRecord{RepoOriginURL: reviewerGitHubRemote}
	record.Config.GitHubUser = githubUser
	return fakeGitHubProjects{record: record}
}

func spawnReviewerEnv(t *testing.T, spec LaunchSpec, opts ...LauncherOption) map[string]string {
	t.Helper()
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, t.TempDir(), opts...)
	if _, err := l.Spawn(context.Background(), spec); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	return rt.createCfg.Env
}

// B590: a reviewer used the machine-wide gh login, which could not see the
// private repo, so its inline findings never reached the PR.
func TestReviewerRunsAsProjectGitHubAccount(t *testing.T) {
	accounts := &fakeGitHubAccounts{token: "project-token"}
	env := spawnReviewerEnv(t, launchSpec(), WithGitHubAccounts(accounts, projectOnPrivateRepo("pinned-login")))
	if env["GH_TOKEN"] != "project-token" {
		t.Fatalf("GH_TOKEN = %q, want the project account's token", env["GH_TOKEN"])
	}
	if accounts.remote != reviewerGitHubRemote || accounts.override != "pinned-login" {
		t.Fatalf("resolved %q override %q, want the project's remote and githubUser", accounts.remote, accounts.override)
	}
}

// A reviewer brought back after a daemon restart must not lose the account.
func TestRestoredReviewerKeepsProjectGitHubAccount(t *testing.T) {
	rt := &fakeRuntime{}
	accounts := &fakeGitHubAccounts{token: "project-token"}
	l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, t.TempDir(), WithGitHubAccounts(accounts, projectOnPrivateRepo("")))
	if _, err := l.RestoreTerminal(context.Background(), launchSpec()); err != nil {
		t.Fatalf("RestoreTerminal: %v", err)
	}
	if rt.createCfg.Env["GH_TOKEN"] != "project-token" {
		t.Fatalf("GH_TOKEN = %q after restore", rt.createCfg.Env["GH_TOKEN"])
	}
}

// A token the project pinned itself is deliberate, exactly as for a worker.
func TestReviewerKeepsProjectPinnedGitHubToken(t *testing.T) {
	spec := launchSpec()
	spec.ProjectEnv = map[string]string{"GH_TOKEN": "pinned"}
	accounts := &fakeGitHubAccounts{token: "auto-picked"}
	env := spawnReviewerEnv(t, spec, WithGitHubAccounts(accounts, projectOnPrivateRepo("")))
	if env["GH_TOKEN"] != "pinned" || accounts.calls != 0 {
		t.Fatalf("GH_TOKEN = %q after %d lookups, want the project's own and none", env["GH_TOKEN"], accounts.calls)
	}
}

// A policy that keeps tokens out of the environment must not gain one here.
func TestReviewerGetsNoGitHubTokenWhenPolicyKeepsItOut(t *testing.T) {
	for _, mode := range []string{"helper", "none"} {
		t.Run(mode, func(t *testing.T) {
			engine, _ := policytest.Engine(t, map[string]map[string]any{"githubCreds": {"mode": mode}})
			accounts := &fakeGitHubAccounts{token: "auto-picked"}
			env := spawnReviewerEnv(t, launchSpec(), WithPolicy(engine), WithGitHubAccounts(accounts, projectOnPrivateRepo("")))
			if _, ok := env["GH_TOKEN"]; ok || accounts.calls != 0 {
				t.Fatalf("mode %s: GH_TOKEN present=%v after %d lookups", mode, ok, accounts.calls)
			}
		})
	}
}

// A failed account lookup must not stop the review from launching.
func TestReviewerLaunchesWhenGitHubAccountLookupFails(t *testing.T) {
	accounts := &fakeGitHubAccounts{err: errors.New("gh unavailable")}
	env := spawnReviewerEnv(t, launchSpec(), WithGitHubAccounts(accounts, projectOnPrivateRepo("")))
	if _, ok := env["GH_TOKEN"]; ok {
		t.Fatal("no token was available, GH_TOKEN must stay unset")
	}
}

// B600: a reviewer is an agent session too, and its git shim must outrank
// anything the launch adds to PATH.
func TestReviewerGetsTheGitShimFirstOnPath(t *testing.T) {
	projects := fakeGitHubProjects{record: domain.ProjectRecord{Path: "/owner/checkout"}}
	spec := launchSpec()
	spec.ProjectEnv = map[string]string{agentlaunch.EnvAllowSharedGitConfig: "1"}
	env := spawnReviewerEnv(t, spec, WithGitHubAccounts(nil, projects), WithExecutable(func() (string, error) { return "/opt/tallpine", nil }))
	if env[agentlaunch.EnvGitGuardRoot] != "/owner/checkout" {
		t.Fatalf("guard root = %q", env[agentlaunch.EnvGitGuardRoot])
	}
	if _, ok := env[agentlaunch.EnvAllowSharedGitConfig]; ok {
		t.Fatal("the override must not come from project env")
	}
	first, _, _ := strings.Cut(env["PATH"], string(os.PathListSeparator))
	if !strings.Contains(first, filepath.Join("runtime", "git-shim")) {
		t.Fatalf("PATH head = %q", first)
	}
}
