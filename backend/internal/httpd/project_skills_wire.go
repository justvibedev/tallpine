package httpd

import (
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

// newProjectSkillsController serves the project and app skills pages; the
// settings service supplies the app-wide skill switches projects inherit.
func newProjectSkillsController(deps APIDeps, dataDir string) *controllers.ProjectSkillsController {
	c := &controllers.ProjectSkillsController{Projects: deps.Projects, DefaultBranch: deps.DefaultBranch, GitCredentials: deps.GitHubCredentials, DataDir: dataDir}
	if d, ok := deps.Settings.(controllers.AppDefaultsReader); ok {
		c.AppDefaults = d
	}
	return c
}
