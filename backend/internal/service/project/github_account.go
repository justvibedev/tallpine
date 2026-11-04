package project

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// githubAccountLookupTimeout bounds the account lookup a project read may do on
// a cold cache, so listing projects never waits on gh or the GitHub API.
const githubAccountLookupTimeout = 3 * time.Second

// GitHubAccountResolver names the signed-in gh account Tallpine uses for a project's
// remote. override is the project's githubUser setting.
type GitHubAccountResolver interface {
	LoginForProject(ctx context.Context, remote, override string) string
}

func (m *Service) githubAccount(ctx context.Context, row domain.ProjectRecord) string {
	if m.ghAccounts == nil || row.Kind == domain.ProjectKindScratch {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, githubAccountLookupTimeout)
	defer cancel()
	return m.ghAccounts.LoginForProject(ctx, row.RepoOriginURL, row.Config.GitHubUser)
}
