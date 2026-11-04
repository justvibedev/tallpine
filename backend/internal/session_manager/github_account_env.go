package sessionmanager

import (
	"context"
	"os"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
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

// explicitGitHubTokenEnv are the variables that already pin a GitHub token. When
// the project env or the daemon env sets one, auto-pick stays out of the way.
var explicitGitHubTokenEnv = []string{"GH_TOKEN", "GITHUB_TOKEN", "TALLPINE_GITHUB_TOKEN"}

// applyGitHubAccountEnv sets GH_TOKEN to the project's gh account so `gh pr
// create` and friends run as the account that can push to the repo. The token
// goes into the session env only; it is never logged.
func (m *Manager) applyGitHubAccountEnv(project domain.ProjectID, env, projectEnv map[string]string, caseInsensitive bool) {
	if m.githubAccounts == nil || project == "" {
		return
	}
	for _, key := range explicitGitHubTokenEnv {
		if v, ok := lookupEnv(projectEnv, key, caseInsensitive); ok && strings.TrimSpace(v) != "" {
			return
		}
		if key == "TALLPINE_GITHUB_TOKEN" && strings.TrimSpace(os.Getenv(key)) != "" && m.daemonPassesEnv(project, key, caseInsensitive) {
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), githubAccountEnvTimeout)
	defer cancel()
	row, err := m.loadProject(ctx, project)
	if err != nil || row.RepoOriginURL == "" {
		return
	}
	token, err := m.githubAccounts.TokenForProject(ctx, row.RepoOriginURL, row.Config.GitHubUser)
	if err != nil {
		m.logger.Warn("github account token unavailable; session keeps the default login", "project", project, "error", err)
		return
	}
	if token != "" {
		setProtectedEnv(env, "GH_TOKEN", token, caseInsensitive)
	}
}
