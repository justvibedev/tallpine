package sessionmanager

import (
	"context"
	"os"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// githubAccountEnvTimeout bounds the account lookup on a cold cache so a slow
// gh or GitHub API cannot stall a launch; on timeout the env is left alone.
const githubAccountEnvTimeout = 15 * time.Second

// GitHubAccountTokens yields the token of the signed-in gh account chosen for a
// project's remote (the project's githubUser override, else auto-picked). An
// empty token means "no per-project account": the env stays as it is.
type GitHubAccountTokens interface {
	TokenForProject(ctx context.Context, remote, override string) (string, error)
}

// SetGitHubAccounts completes daemon wiring before request handling begins.
// Nil leaves every session on whatever GitHub login the machine defaults to.
func (m *Manager) SetGitHubAccounts(accounts GitHubAccountTokens) {
	m.githubAccounts = accounts
}

// SetProjectGitCredentials completes daemon wiring before request handling
// begins. Nil leaves the daemon's own git commands on the machine's login.
func (m *Manager) SetProjectGitCredentials(credentials ports.ProjectGitCredentials) {
	m.gitCredentials = credentials
}

// projectGitEnv is the environment that runs one daemon-side git process as the
// project's GitHub account; nil when it has none.
func (m *Manager) projectGitEnv(ctx context.Context, project domain.ProjectID) []string {
	if m.gitCredentials == nil {
		return nil
	}
	return m.gitCredentials.ProjectGitEnv(ctx, project)
}

// orchestratorRefreshGitEnv is projectGitEnv for the orchestrator worktree
// refresh; when no refresh will run, it skips the token lookup.
func (m *Manager) orchestratorRefreshGitEnv(ctx context.Context, kind domain.SessionKind, project domain.ProjectID, refresh bool) []string {
	if !refresh || kind != domain.KindOrchestrator {
		return nil
	}
	return m.projectGitEnv(ctx, project)
}

// explicitGitHubTokenEnv are the variables that already pin a GitHub token. When
// the project env or the daemon env sets one, auto-pick stays out of the way.
var explicitGitHubTokenEnv = []string{"GH_TOKEN", "GITHUB_TOKEN", "TALLPINE_GITHUB_TOKEN"}

// applyGitHubAccountEnv sets GH_TOKEN to the project's gh account so `gh pr
// create` and friends run as the account that can push to the repo.
func (m *Manager) applyGitHubAccountEnv(project domain.ProjectID, env, projectEnv map[string]string, caseInsensitive bool) {
	if m.githubAccounts == nil || project == "" {
		return
	}
	load := func(ctx context.Context) (domain.ProjectRecord, error) { return m.loadProject(ctx, project) }
	daemonPasses := func(key string) bool { return m.daemonPassesEnv(project, key, caseInsensitive) }
	if err := ApplyProjectGitHubToken(m.githubAccounts, load, env, projectEnv, caseInsensitive, daemonPasses); err != nil {
		m.logger.Warn("github account token unavailable; session keeps the default login", "project", project, "error", err)
	}
}

// ApplyProjectGitHubToken is the one rule for which GitHub account an agent
// runs as, shared by worker sessions and reviewers: unless a token is already
// pinned (by the project env, or the daemon's TALLPINE_GITHUB_TOKEN when
// daemonPasses lets it through), GH_TOKEN in env becomes the token of the gh
// account chosen for the project's remote. An unknown project or a project with
// no dedicated account leaves env alone; only a failed token lookup is an
// error. The token goes into env only and is never logged.
func ApplyProjectGitHubToken(accounts GitHubAccountTokens, load func(context.Context) (domain.ProjectRecord, error), env, projectEnv map[string]string, caseInsensitive bool, daemonPasses func(key string) bool) error {
	for _, key := range explicitGitHubTokenEnv {
		if v, ok := lookupEnv(projectEnv, key, caseInsensitive); ok && strings.TrimSpace(v) != "" {
			return nil
		}
		if key == "TALLPINE_GITHUB_TOKEN" && strings.TrimSpace(os.Getenv(key)) != "" && daemonPasses(key) {
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), githubAccountEnvTimeout)
	defer cancel()
	row, err := load(ctx)
	if err != nil || row.RepoOriginURL == "" {
		return nil
	}
	token, err := accounts.TokenForProject(ctx, row.Config.CredentialRemoteForHost(row.RepoOriginURL, "github.com"), row.Config.GitHubUser)
	if err != nil {
		return err
	}
	if token != "" {
		setProtectedEnv(env, "GH_TOKEN", token, caseInsensitive)
	}
	return nil
}
