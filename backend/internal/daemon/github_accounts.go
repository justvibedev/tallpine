package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ghaccount"
)

const projectOverrideTTL = time.Minute

// envGitHubUser is the global default account, below a project's githubUser.
const envGitHubUser = "TALLPINE_GITHUB_USER"

type projectLister interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
}

// githubAccounts connects ghaccount's resolver to Tallpine's projects: it applies the
// override order (project githubUser, then TALLPINE_GITHUB_USER, then auto-pick) for
// SCM calls keyed by repo and for sessions keyed by project.
type githubAccounts struct {
	resolver *ghaccount.Resolver
	projects projectLister

	mu        sync.Mutex
	overrides map[string]string // repo key -> project githubUser
	loadedAt  time.Time
}

func newGitHubAccounts(projects projectLister, logger *slog.Logger) *githubAccounts {
	return &githubAccounts{resolver: ghaccount.NewResolver(logger), projects: projects}
}

// activeGitHubAccounts is the daemon-wide instance the SCM constructors read;
// set once in Run. Nil (tests, no store) keeps the single global token.
var activeGitHubAccounts atomic.Pointer[githubAccounts]

// explicitGitHubTokenEnvSet reports whether the daemon env pins a GitHub token.
// Only TALLPINE_GITHUB_TOKEN counts: GH_TOKEN and GITHUB_TOKEN are ambient (an
// install run from an agent session leaks one), so they never replace the
// per-project accounts.
func explicitGitHubTokenEnvSet() (string, bool) {
	for _, name := range []string{"TALLPINE_GITHUB_TOKEN"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return name, true
		}
	}
	return "", false
}

// logAutoPickBypass tells the operator once, at startup, when a daemon-env
// token makes the SCM observer ignore per-project accounts.
func logAutoPickBypass(logger *slog.Logger) {
	if name, ok := explicitGitHubTokenEnvSet(); ok {
		logger.Warn("per-project GitHub account auto-pick is bypassed for the SCM observer: a token is set in the daemon environment",
			"env", name)
	}
}

// globalUser is TALLPINE_GITHUB_USER; a value that is not a valid GitHub login is
// ignored (it would otherwise reach `gh auth token -u`).
func (a *githubAccounts) globalUser() string {
	user := strings.TrimSpace(os.Getenv(envGitHubUser))
	if user != "" && !domain.IsGitHubLogin(user) {
		return ""
	}
	return user
}

// InvalidateLogin drops an account's memoized token after an auth failure.
func (a *githubAccounts) InvalidateLogin(login string) { a.resolver.InvalidateToken(login) }

// LoginForRepo implements the SCM adapter's AccountTokens.
func (a *githubAccounts) LoginForRepo(ctx context.Context, owner, name string) string {
	repo := ghaccount.Repo{Owner: owner, Name: name}
	override := a.projectOverride(ctx, repo.Key())
	if override == "" {
		override = a.globalUser()
	}
	return a.resolver.ResolveRepo(ctx, repo, override)
}

// TokenForLogin implements the SCM adapter's AccountTokens.
func (a *githubAccounts) TokenForLogin(ctx context.Context, login string) (string, error) {
	return a.resolver.Token(ctx, login)
}

// LoginForProject names the account for a project remote ("" = default).
func (a *githubAccounts) LoginForProject(ctx context.Context, remote, override string) string {
	if override = strings.TrimSpace(override); override == "" {
		override = a.globalUser()
	}
	return a.resolver.ResolveRemote(ctx, remote, override)
}

// TokenForProject returns the account's token for a session env; "" means the
// project has no dedicated account.
func (a *githubAccounts) TokenForProject(ctx context.Context, remote, override string) (string, error) {
	login := a.LoginForProject(ctx, remote, override)
	if login == "" {
		return "", nil
	}
	return a.resolver.Token(ctx, login)
}

// projectOverride finds the githubUser of a project whose origin is repoKey.
// With several projects on one repo and different overrides, the last wins.
func (a *githubAccounts) projectOverride(ctx context.Context, repoKey string) string {
	a.mu.Lock()
	fresh := a.overrides != nil && time.Since(a.loadedAt) < projectOverrideTTL
	a.mu.Unlock()
	if !fresh {
		a.reloadOverrides(ctx)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.overrides[repoKey]
}

func (a *githubAccounts) reloadOverrides(ctx context.Context) {
	next := map[string]string{}
	rows, err := a.projects.ListProjects(ctx)
	if err == nil {
		for _, row := range rows {
			user := strings.TrimSpace(row.Config.GitHubUser)
			if user == "" {
				continue
			}
			for _, remote := range row.Config.AccountRemotes(row.RepoOriginURL) {
				if repo, ok := ghaccount.ParseRemote(ctx, remote, a.resolver.SSHHost); ok {
					next[repo.Key()] = user
				}
			}
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil && a.overrides != nil {
		return // keep the last good map
	}
	a.overrides, a.loadedAt = next, time.Now()
}
