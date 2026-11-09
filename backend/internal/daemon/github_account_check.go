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
	// First remote ghaccount can parse: the origin, or after a move the canonical repo.
	var tracked string
	var r ghaccount.Repo
	ok = false
	for _, remote := range row.Config.AccountRemotes(row.RepoOriginURL) {
		if r, ok = ghaccount.ParseRemote(ctx, remote, c.accounts.resolver.SSHHost); ok {
			tracked = remote
			break
		}
	}
	if !ok || row.Kind == domain.ProjectKindScratch {
		return ghaccount.Inspection{Status: ghaccount.StatusNoAccount}, "", override, true, nil
	}
	login := c.accounts.LoginForProject(ctx, tracked, override)
	return c.accounts.resolver.Inspect(ctx, login, r), r.Owner + "/" + r.Name, override, true, nil
}

// Accounts lists the signed-in gh accounts and, for a project, which of them
// can push to its repo and which one auto-pick would choose. An empty id lists
// logins only. It reads the project's remotes the same way CheckProject does.
func (c githubAccountChecker) Accounts(ctx context.Context, id domain.ProjectID) (res ghaccount.AccountList, found bool, err error) {
	var repo *ghaccount.Repo
	var tracked string
	if id != "" {
		row, ok, err := c.projects.GetProject(ctx, string(id))
		if err != nil || !ok {
			return res, false, err
		}
		res.Override = row.Config.GitHubUser
		if row.Kind != domain.ProjectKindScratch {
			for _, remote := range row.Config.AccountRemotes(row.RepoOriginURL) {
				if r, ok := ghaccount.ParseRemote(ctx, remote, c.accounts.resolver.SSHHost); ok {
					repo, tracked = &r, remote
					res.Repo = r.Owner + "/" + r.Name
					break
				}
			}
		}
	}
	if res.Accounts, err = c.accounts.resolver.SignedIn(ctx, repo); err != nil {
		// gh missing or not answering: report it instead of failing the page.
		res.Unavailable = true
		return res, true, nil //nolint:nilerr // an unavailable gh is part of the answer
	}
	if repo != nil {
		res.AutoPick = c.accounts.LoginForProject(ctx, tracked, "")
	}
	return res, true, nil
}
