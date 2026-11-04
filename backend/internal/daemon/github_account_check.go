package daemon

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ghaccount"
)

type projectGetter interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// githubAccountChecker answers Settings' "which login is this project's token"
// question with the same resolution sessions use.
type githubAccountChecker struct {
	accounts *githubAccounts
	projects projectGetter
}

// CheckProject returns the inspection and the project's explicit override.
func (c githubAccountChecker) CheckProject(ctx context.Context, id domain.ProjectID) (insp ghaccount.Inspection, repo, override string, found bool, err error) {
	row, ok, err := c.projects.GetProject(ctx, string(id))
	if err != nil || !ok {
		return insp, "", "", false, err
	}
	override = row.Config.GitHubUser
	r, ok := ghaccount.ParseRemote(ctx, row.RepoOriginURL, c.accounts.resolver.SSHHost)
	if !ok || row.Kind == domain.ProjectKindScratch {
		return ghaccount.Inspection{Status: ghaccount.StatusNoAccount}, "", override, true, nil
	}
	login := c.accounts.LoginForProject(ctx, row.RepoOriginURL, override)
	return c.accounts.resolver.Inspect(ctx, login, r), r.Owner + "/" + r.Name, override, true, nil
}
