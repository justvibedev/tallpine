package httpd

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

// newProjectSkillsController serves the project and app skills pages; the
// settings service supplies the app-wide skill switches projects inherit.
func newProjectSkillsController(deps APIDeps, dataDir string) *controllers.ProjectSkillsController {
	c := &controllers.ProjectSkillsController{Projects: deps.Projects, DefaultBranch: deps.DefaultBranch, GitCredentials: deps.GitHubCredentials, DataDir: dataDir}
	if d, ok := deps.Settings.(controllers.AppDefaultsReader); ok {
		c.AppDefaults = d
	}
	c.LSPInstaller = &skillbridge.LSPInstaller{Claude: deps.ClaudeBinary}
	if a, ok := deps.AgentAccounts.(newSessionLaunchEnv); ok {
		c.ClaudeSessionEnv = func(ctx context.Context) (map[string]string, error) {
			return a.NewSessionLaunchEnv(ctx, domain.HarnessClaudeCode)
		}
	}
	return c
}

// newSessionLaunchEnv is the account env a new session of a harness launches with.
type newSessionLaunchEnv interface {
	NewSessionLaunchEnv(ctx context.Context, harness domain.AgentHarness) (map[string]string, error)
}
