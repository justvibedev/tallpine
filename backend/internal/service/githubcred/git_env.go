package githubcred

import (
	"context"
	"os"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/githubcreds"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// gitEnvTimeout bounds the token lookup so a slow gh cannot hold up the git
// command it is for; on timeout that command runs with the machine's login.
const gitEnvTimeout = 15 * time.Second

// envGitPassword carries the token to the credential helper of one daemon-run
// git process. The token stays out of git's argv and config.
var envGitPassword = brand.Env("GIT_PASSWORD")

// ProjectGitEnv returns the environment entries that make one daemon-run git
// process authenticate to the project's GitHub host as the project's account,
// resolved in the same order agents get (see token). nil means there is no such
// credential and git uses the machine's login, as it always did.
//
// The credential is scoped to the project's own host, replaces the helpers
// configured for that host only, and answers only `get`, so nothing is stored
// in the user's keychain. Hooks and fsmonitor are off for that process, as in
// the daemon's other git commands, because whatever they ran (repository
// scripts an agent may have written) would inherit the token. The caller must
// pass these entries to the git process only, and never log them.
func (s *Service) ProjectGitEnv(ctx context.Context, id domain.ProjectID) []string {
	if s == nil || id == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, gitEnvTimeout)
	defer cancel()
	project, found, err := s.projects.GetProject(ctx, string(id))
	if err != nil || !found {
		return nil
	}
	host := githubcreds.HostOf(project.RepoOriginURL)
	if host == "" || !domain.IsGitHubHost(host) {
		return nil
	}
	cred, err := s.token(ctx, project, host)
	if err != nil {
		return nil
	}
	helper := `f() { test "$1" = get || exit 0; printf '%s\n' 'username=` + cred.Username + `' "password=$` + envGitPassword + `"; }; f`
	existing := map[string]string{githubcreds.EnvGitConfigParameters: os.Getenv(githubcreds.EnvGitConfigParameters)}
	params := githubcreds.AppendGitConfig(githubcreds.GitConfigEnv(existing, helper, []string{host}),
		[2]string{"core.hooksPath", os.DevNull}, [2]string{"core.fsmonitor", "false"})
	return []string{githubcreds.EnvGitConfigParameters + "=" + params, envGitPassword + "=" + cred.Token}
}
