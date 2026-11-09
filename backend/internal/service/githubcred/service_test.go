package githubcred

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeSessions struct{ rec domain.SessionRecord }

func (f fakeSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	return f.rec, id == f.rec.ID, nil
}

type fakeProjects struct{ rec domain.ProjectRecord }

func (f fakeProjects) GetProject(context.Context, string) (domain.ProjectRecord, bool, error) {
	return f.rec, true, nil
}

type fakeAccounts struct {
	token string
	calls int
	err   error
	repo  string
}

func (f *fakeAccounts) TokenForProject(_ context.Context, repo, _ string) (string, error) {
	f.calls++
	f.repo = repo
	return f.token, f.err
}

func newService(t *testing.T, mode string, project domain.ProjectRecord, accounts Accounts, env map[string]string) (*Service, *bytes.Buffer) {
	t.Helper()
	settings := map[string]map[string]any{"githubCreds": {"mode": mode}}
	engine, _ := policytest.Engine(t, settings)
	var logs bytes.Buffer
	s := New(engine, fakeSessions{rec: domain.SessionRecord{ID: "p-1", ProjectID: "p", Kind: domain.KindWorker}}, fakeProjects{rec: project}, accounts, slog.New(slog.NewTextHandler(&logs, nil)))
	s.getenv = func(k string) string { return env[k] }
	return s, &logs
}

var acme = domain.ProjectRecord{ID: "p", RepoOriginURL: "git@github.com:acme/app.git", Config: domain.ProjectConfig{GitHubUser: "acme-bot"}}

func TestIssue_HelperModeServesTheProjectsAccountTokenAndAuditsWithoutIt(t *testing.T) {
	accounts := &fakeAccounts{token: "account-secret"}
	s, logs := newService(t, "helper", acme, accounts, nil)
	got, err := s.Issue(context.Background(), "p-1", "github.com")
	if err != nil || got.Token != "account-secret" || got.Source != SourceAccount || got.Username != "x-access-token" {
		t.Fatalf("%+v %v", got, err)
	}
	if !strings.Contains(logs.String(), "github credential issued") || !strings.Contains(logs.String(), "source=account") || strings.Contains(logs.String(), "account-secret") {
		t.Fatalf("audit line must name the source and never the token:\n%s", logs)
	}
}

// The order is env mode's: project env, then the daemon's pinned token, then
// the account. A narrow token pinned on the daemon must not be replaced by the
// broader gh account token just because the stricter helper mode is on.
func TestIssue_ProjectEnvThenDaemonEnvThenAccount(t *testing.T) {
	withEnv := acme
	withEnv.Config.Env = map[string]string{"GH_TOKEN": "project-token"}
	accounts := &fakeAccounts{token: "account-secret"}
	s, _ := newService(t, "helper", withEnv, accounts, map[string]string{"GH_TOKEN": "daemon-token"})
	if got, _ := s.Issue(context.Background(), "p-1", "github.com"); got.Token != "project-token" || got.Source != SourceProjectEnv || accounts.calls != 0 {
		t.Fatalf("an explicit project token wins without an account lookup: %+v calls=%d", got, accounts.calls)
	}
	accounts = &fakeAccounts{token: "account-secret"}
	s, _ = newService(t, "helper", acme, accounts, map[string]string{"TALLPINE_GITHUB_TOKEN": "daemon-token"})
	if got, _ := s.Issue(context.Background(), "p-1", "github.com"); got.Token != "daemon-token" || got.Source != SourceDaemonEnv || accounts.calls != 0 {
		t.Fatalf("a daemon-pinned token must beat the account token, as in env mode: %+v calls=%d", got, accounts.calls)
	}
	s, _ = newService(t, "helper", acme, &fakeAccounts{token: "account-secret"}, nil)
	if got, _ := s.Issue(context.Background(), "p-1", "github.com"); got.Token != "account-secret" || got.Source != SourceAccount {
		t.Fatalf("with nothing pinned the project's account is used: %+v", got)
	}
	s, _ = newService(t, "helper", acme, &fakeAccounts{}, nil)
	if _, err := s.Issue(context.Background(), "p-1", "github.com"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no token anywhere: %v", err)
	}
}

func TestIssue_NoneRefusesWithoutLookup(t *testing.T) {
	accounts := &fakeAccounts{token: "t"}
	s, _ := newService(t, "none", acme, accounts, nil)
	if _, err := s.Issue(context.Background(), "p-1", "github.com"); !errors.Is(err, ErrDenied) || accounts.calls != 0 {
		t.Errorf("mode none: err=%v calls=%d, want ErrDenied and no lookup", err, accounts.calls)
	}
}

// B483: a session launched under helper mode has no token in its environment
// and gh/git ask the daemon. When Security mode (or githubCreds) is turned off
// while it runs, the policy now says env; refusing then left the session with
// no GitHub access at all ("gh auth login"). Env mode is served.
func TestIssue_EnvModeServesASessionLaunchedUnderHelper(t *testing.T) {
	s, _ := newService(t, "env", acme, &fakeAccounts{token: "account-secret"}, nil)
	if got, err := s.Issue(context.Background(), "p-1", "github.com"); err != nil || got.Token != "account-secret" {
		t.Fatalf("env mode must still serve the token: %+v %v", got, err)
	}
}

func TestIssue_HostsAndSessions(t *testing.T) {
	ghe := acme
	ghe.RepoOriginURL = "git@ghe.example.com:acme/app.git"
	s, _ := newService(t, "helper", ghe, &fakeAccounts{token: "t"}, nil)
	for host, ok := range map[string]bool{"github.com": true, "GHE.example.com": true, "evil.example.com": false, "": false} {
		_, err := s.Issue(context.Background(), "p-1", host)
		if ok != (err == nil) {
			t.Errorf("host %q: err=%v, allowed=%v", host, err, ok)
		}
	}
	if _, err := s.Issue(context.Background(), "p-9", "github.com"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("unknown session: %v", err)
	}
}

// B592: a project moved from Bitbucket to GitHub resolves its account and
// allowed host from the canonical repository, not the stale origin.
func TestIssue_MovedProjectUsesCanonicalRepo(t *testing.T) {
	moved := domain.ProjectRecord{ID: "p", RepoOriginURL: "git@bitbucket.org:acme/app.git", Config: domain.ProjectConfig{CanonicalRepoURL: "https://github.com/acme/app"}}
	accounts := &fakeAccounts{token: "t"}
	s, _ := newService(t, "helper", moved, accounts, nil)
	if _, err := s.Issue(context.Background(), "p-1", "github.com"); err != nil || accounts.repo != "https://github.com/acme/app" {
		t.Fatalf("repo=%q err=%v", accounts.repo, err)
	}
}

// A non-GitHub canonical host must not widen where the GitHub token is offered.
func TestIssue_NonGitHubCanonicalHostRefused(t *testing.T) {
	p := domain.ProjectRecord{ID: "p", RepoOriginURL: "git@github.com:acme/app.git", Config: domain.ProjectConfig{CanonicalRepoURL: "https://gitlab.com/acme/app"}}
	s, _ := newService(t, "helper", p, &fakeAccounts{token: "t"}, nil)
	if _, err := s.Issue(context.Background(), "p-1", "gitlab.com"); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err=%v, want ErrHostNotAllowed", err)
	}
}

// Checkout and push stay on origin after a move: a GitHub-family origin host is
// still issued a credential, resolved from the origin (not the canonical repo).
func TestIssue_OriginHostStillAllowedAfterCanonicalMove(t *testing.T) {
	domain.SetGitHubEnterpriseHosts([]string{"ghes.corp.example"})
	t.Cleanup(func() { domain.SetGitHubEnterpriseHosts(nil) })
	p := domain.ProjectRecord{ID: "p", RepoOriginURL: "git@ghes.corp.example:o/r.git", Config: domain.ProjectConfig{CanonicalRepoURL: "https://github.com/o/r"}}
	accounts := &fakeAccounts{token: "t"}
	s, _ := newService(t, "helper", p, accounts, nil)
	if _, err := s.Issue(context.Background(), "p-1", "ghes.corp.example"); err != nil || accounts.repo != p.RepoOriginURL {
		t.Fatalf("origin host: repo=%q err=%v", accounts.repo, err)
	}
	if _, err := s.Issue(context.Background(), "p-1", "github.com"); err != nil || accounts.repo != "https://github.com/o/r" {
		t.Fatalf("canonical host: repo=%q err=%v", accounts.repo, err)
	}
}

// B592: the GitHub account token for the moved-to repo is never served to the
// old non-GitHub origin host; no account resolves from a Bitbucket origin.
func TestIssue_MovedProjectDoesNotServeGitHubTokenToOldOriginHost(t *testing.T) {
	p := domain.ProjectRecord{ID: "p", RepoOriginURL: "https://bitbucket.org/acme/app.git", Config: domain.ProjectConfig{GitHubUser: "work", CanonicalRepoURL: "https://github.com/acme/app"}}
	accounts := &originBlindAccounts{}
	s, _ := newService(t, "helper", p, accounts, nil)
	if got, err := s.Issue(context.Background(), "p-1", "bitbucket.org"); err == nil || got.Token != "" {
		t.Fatalf("bitbucket.org was served %+v err=%v", got, err)
	}
	if got, err := s.Issue(context.Background(), "p-1", "github.com"); err != nil || got.Token != "gh-token" {
		t.Fatalf("github.com: %+v %v", got, err)
	}
}

// originBlindAccounts mimics the real resolver: only a GitHub remote resolves an account.
type originBlindAccounts struct{}

func (originBlindAccounts) TokenForProject(_ context.Context, repo, override string) (string, error) {
	// Like the real resolver, a non-empty override wins before the remote is parsed.
	if override != "" || strings.Contains(repo, "github.com") {
		return "gh-token", nil
	}
	return "", nil
}
