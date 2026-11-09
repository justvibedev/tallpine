package daemon

import (
	"tallpine.eu/tallpine/backend/internal/adapters/workspace/gitworktree"
	"tallpine.eu/tallpine/backend/internal/service/githubcred"
	"tallpine.eu/tallpine/backend/internal/service/localland"
)

// gitCredentials builds the service that issues GitHub credentials to agents
// and to the daemon's own git commands. It holds no state of its own.
func (b *boot) gitCredentials() *githubcred.Service {
	return githubcred.New(b.agentPolicySvc, b.store, b.store, b.ghAccounts, b.log)
}

// sessionLand builds `tallpine session land`, fetching and pushing as the
// project's GitHub account.
func (b *boot) sessionLand() *localland.Service {
	land := localland.New(b.store, b.store, gitworktree.NewDefaultBranchResolver(""), b.log)
	land.SetGitCredentials(b.gitCredentials())
	return land
}
