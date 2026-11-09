package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ghaccount"
)

type stubGH struct{ logins []string }

func (s stubGH) Logins(context.Context) ([]string, error) { return s.logins, nil }
func (s stubGH) Token(_ context.Context, l string) (string, error) {
	return "tok-" + l, nil
}

// pushFor grants push to one login on every repo.
type pushFor string

func (p pushFor) CanPush(_ context.Context, token string, _ ghaccount.Repo) (bool, error) {
	return token == "tok-"+string(p), nil
}

type stubProjects []domain.ProjectRecord

func (s stubProjects) ListProjects(context.Context) ([]domain.ProjectRecord, error) { return s, nil }

func testAccounts(projects stubProjects) *githubAccounts {
	a := newGitHubAccounts(projects, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	a.resolver.GH = stubGH{logins: []string{"alice", "bob"}}
	a.resolver.Push = pushFor("bob")
	a.resolver.SSHHost = func(_ context.Context, alias string) (string, error) { return alias, nil }
	return a
}

func TestGitHubAccountsOverrideOrder(t *testing.T) {
	t.Setenv(envGitHubUser, "")
	remote := "git@github.com:acme/app.git"
	a := testAccounts(stubProjects{{ID: "p", RepoOriginURL: remote, Config: domain.ProjectConfig{GitHubUser: "alice"}}})
	ctx := context.Background()

	// project override beats auto-pick (bob has push) for sessions and SCM.
	if got := a.LoginForProject(ctx, remote, "alice"); got != "alice" {
		t.Fatalf("session login = %q, want project override alice", got)
	}
	if got := a.LoginForRepo(ctx, "acme", "app"); got != "alice" {
		t.Fatalf("SCM login = %q, want project override alice", got)
	}
	// global TALLPINE_GITHUB_USER beats auto-pick when the project sets nothing.
	t.Setenv(envGitHubUser, "alice")
	if got := a.LoginForProject(ctx, remote, ""); got != "alice" {
		t.Fatalf("login = %q, want TALLPINE_GITHUB_USER alice", got)
	}
	// no override anywhere: auto-pick finds bob.
	t.Setenv(envGitHubUser, "")
	b := testAccounts(nil)
	if got := b.LoginForProject(ctx, remote, ""); got != "bob" {
		t.Fatalf("login = %q, want auto-picked bob", got)
	}
	if tok, err := b.TokenForProject(ctx, remote, ""); err != nil || tok != "tok-bob" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	if tok, _ := b.TokenForProject(ctx, "git@gitlab.com:o/r.git", ""); tok != "" {
		t.Fatal("non-GitHub remote must yield no token")
	}
}

func TestLogAutoPickBypassNamesTheVariableNotTheToken(t *testing.T) {
	for _, name := range []string{"TALLPINE_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		t.Setenv(name, "")
	}
	var quiet bytes.Buffer
	logAutoPickBypass(slog.New(slog.NewTextHandler(&quiet, nil)))
	if quiet.Len() != 0 {
		t.Fatalf("logged without a pinned token: %s", quiet.String())
	}
	t.Setenv("GITHUB_TOKEN", "ambient-value")
	var ambient bytes.Buffer
	logAutoPickBypass(slog.New(slog.NewTextHandler(&ambient, nil)))
	if ambient.Len() != 0 {
		t.Fatalf("ambient GITHUB_TOKEN must not count as a pin: %s", ambient.String())
	}
	t.Setenv("TALLPINE_GITHUB_TOKEN", "super-secret-value")
	var buf bytes.Buffer
	logAutoPickBypass(slog.New(slog.NewTextHandler(&buf, nil)))
	out := buf.String()
	if !strings.Contains(out, "bypassed") || !strings.Contains(out, "TALLPINE_GITHUB_TOKEN") || strings.Contains(out, "super-secret") {
		t.Fatalf("unexpected log: %s", out)
	}
}

// On the SCM path a project's githubUser beats TALLPINE_GITHUB_USER, and an invalid
// TALLPINE_GITHUB_USER is ignored rather than passed on to gh.
func TestLoginForRepoProjectOverrideBeatsGlobalUser(t *testing.T) {
	remote := "git@github.com:acme/app.git"
	a := testAccounts(stubProjects{{ID: "p", RepoOriginURL: remote, Config: domain.ProjectConfig{GitHubUser: "alice"}}})
	t.Setenv(envGitHubUser, "carol")
	if got := a.LoginForRepo(context.Background(), "acme", "app"); got != "alice" {
		t.Fatalf("SCM login = %q, want project githubUser alice over TALLPINE_GITHUB_USER", got)
	}
	t.Setenv(envGitHubUser, "carol")
	if got := testAccounts(nil).LoginForRepo(context.Background(), "acme", "app"); got != "carol" {
		t.Fatalf("SCM login = %q, want TALLPINE_GITHUB_USER carol when no project override", got)
	}
}

func TestInvalidGlobalGitHubUserIsIgnored(t *testing.T) {
	t.Setenv(envGitHubUser, "-oBad")
	a := testAccounts(nil)
	if got := a.LoginForProject(context.Background(), "git@github.com:acme/app.git", ""); got != "bob" {
		t.Fatalf("login = %q, want auto-pick bob (invalid TALLPINE_GITHUB_USER ignored)", got)
	}
}

func TestGhTokenSourceTokenForRepoUsesProjectAccount(t *testing.T) {
	t.Setenv(envGitHubUser, "")
	for _, name := range []string{"TALLPINE_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		t.Setenv(name, "")
	}
	accounts := testAccounts(stubProjects{{ID: "p", RepoOriginURL: "git@github.com:acme/app.git", Config: domain.ProjectConfig{GitHubUser: "alice"}}})
	activeGitHubAccounts.Store(accounts)
	t.Cleanup(func() { activeGitHubAccounts.Store(nil) })

	tok, err := (&ghTokenSource{}).TokenForRepo(context.Background(), "acme", "app")
	if err != nil || tok != "tok-alice" {
		t.Fatalf("token = %q, %v; want the project's account token", tok, err)
	}

	// A token pinned in the daemon env wins over per-project accounts.
	t.Setenv("TALLPINE_GITHUB_TOKEN", "pinned")
	tok, err = (&ghTokenSource{}).TokenForRepo(context.Background(), "acme", "app")
	if err != nil || tok != "pinned" {
		t.Fatalf("pinned token = %q, %v", tok, err)
	}
}

type tokenlessGH struct{ stubGH }

func (tokenlessGH) Token(context.Context, string) (string, error) {
	return "", errors.New("logged out")
}

func TestGhTokenSourceTokenForRepoFallsBackWhenAccountTokenFails(t *testing.T) {
	t.Setenv(envGitHubUser, "")
	for _, name := range []string{"TALLPINE_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		t.Setenv(name, "")
	}
	accounts := testAccounts(stubProjects{{ID: "p", RepoOriginURL: "git@github.com:acme/app.git", Config: domain.ProjectConfig{GitHubUser: "alice"}}})
	accounts.resolver.GH = tokenlessGH{stubGH{logins: []string{"alice"}}}
	activeGitHubAccounts.Store(accounts)
	t.Cleanup(func() { activeGitHubAccounts.Store(nil) })

	src := &ghTokenSource{token: "global-token", expiresAt: time.Now().Add(time.Hour)}
	tok, err := src.TokenForRepo(context.Background(), "acme", "app")
	if err != nil || tok != "global-token" {
		t.Fatalf("token = %q, %v; want the global token when the account's token fails", tok, err)
	}
}

type oneProject struct{ rec domain.ProjectRecord }

func (o oneProject) GetProject(context.Context, string) (domain.ProjectRecord, bool, error) {
	return o.rec, true, nil
}

// B592: a project moved from Bitbucket to GitHub reports the account and repo
// of its canonical repository in Settings, not "no account".
func TestGitHubAccountCheckerUsesCanonicalRepoForMovedProject(t *testing.T) {
	t.Setenv(envGitHubUser, "")
	rec := domain.ProjectRecord{ID: "p", RepoOriginURL: "git@bitbucket.org:acme/app.git", Config: domain.ProjectConfig{CanonicalRepoURL: "https://github.com/acme/app"}}
	c := githubAccountChecker{accounts: testAccounts(stubProjects{rec}), projects: oneProject{rec}}
	insp, repo, _, found, err := c.CheckProject(context.Background(), "p")
	if err != nil || !found || repo != "acme/app" || insp.Status == ghaccount.StatusNoAccount {
		t.Fatalf("insp=%+v repo=%q found=%v err=%v", insp, repo, found, err)
	}
}

// A canonical URL on a non-GitHub host must not hide a GitHub origin's account
// override: Settings and the SCM observer still resolve from the origin.
func TestGitHubAccountsNonGitHubCanonicalKeepsOriginOverride(t *testing.T) {
	t.Setenv(envGitHubUser, "")
	rec := domain.ProjectRecord{ID: "p", RepoOriginURL: "git@github.com:acme/app.git", Config: domain.ProjectConfig{GitHubUser: "alice", CanonicalRepoURL: "https://bitbucket.org/x/y"}}
	a := testAccounts(stubProjects{rec})
	if got := a.LoginForRepo(context.Background(), "acme", "app"); got != "alice" {
		t.Fatalf("SCM login = %q, want override alice", got)
	}
	c := githubAccountChecker{accounts: a, projects: oneProject{rec}}
	if _, repo, _, _, err := c.CheckProject(context.Background(), "p"); err != nil || repo != "acme/app" {
		t.Fatalf("repo=%q err=%v", repo, err)
	}
}

// A github.com origin with a canonical URL on a GHE host keeps its override and
// its Settings account: ghaccount only parses github.com remotes, so the origin
// must still be tried.
func TestGitHubAccountsGitHubComOriginWithGHECanonical(t *testing.T) {
	t.Setenv(envGitHubUser, "")
	rec := domain.ProjectRecord{ID: "p", RepoOriginURL: "git@github.com:acme/app.git", Config: domain.ProjectConfig{GitHubUser: "alice", CanonicalRepoURL: "https://acme.ghe.com/new/app"}}
	a := testAccounts(stubProjects{rec})
	if got := a.LoginForRepo(context.Background(), "acme", "app"); got != "alice" {
		t.Fatalf("SCM login = %q, want override alice", got)
	}
	c := githubAccountChecker{accounts: a, projects: oneProject{rec}}
	if insp, repo, _, _, err := c.CheckProject(context.Background(), "p"); err != nil || repo != "acme/app" || insp.Status == ghaccount.StatusNoAccount {
		t.Fatalf("insp=%+v repo=%q err=%v", insp, repo, err)
	}
}

type getProject struct{ rows stubProjects }

func (g getProject) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	for _, r := range g.rows {
		if r.ID == id {
			return r, true, nil
		}
	}
	return domain.ProjectRecord{}, false, nil
}

func TestGitHubAccountCheckerAccounts(t *testing.T) {
	t.Setenv(envGitHubUser, "")
	rows := stubProjects{
		{ID: "gh", RepoOriginURL: "git@github.com:acme/app.git", Config: domain.ProjectConfig{GitHubUser: "alice"}},
		{ID: "bb", RepoOriginURL: "git@bitbucket.org:acme/app.git"},
	}
	accts := testAccounts(rows)
	accts.resolver.Identity = nil // no GitHub round trip for `GET /user`
	c := githubAccountChecker{accounts: accts, projects: getProject{rows}}
	ctx := context.Background()

	res, found, err := c.Accounts(ctx, "gh")
	if err != nil || !found || res.Repo != "acme/app" || res.Override != "alice" || res.AutoPick != "bob" {
		t.Fatalf("github project: %+v found=%v err=%v", res, found, err)
	}
	pushes := map[string]string{}
	for _, a := range res.Accounts {
		pushes[a.Login] = a.Push
	}
	if pushes["bob"] != ghaccount.PushYes || pushes["alice"] == ghaccount.PushYes {
		t.Fatalf("push per account = %v", pushes)
	}

	// A non-GitHub origin has no repo to check and auto-pick cannot choose.
	res, found, err = c.Accounts(ctx, "bb")
	if err != nil || !found || res.Repo != "" || res.AutoPick != "" || res.Accounts[0].Push != "" {
		t.Fatalf("bitbucket project: %+v found=%v err=%v", res, found, err)
	}
	if _, found, _ = c.Accounts(ctx, "missing"); found {
		t.Fatal("unknown project must not be found")
	}
	if res, _, _ = c.Accounts(ctx, ""); len(res.Accounts) != 2 || res.Repo != "" {
		t.Fatalf("machine-wide: %+v", res)
	}
}
