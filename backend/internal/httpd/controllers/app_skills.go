package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

// AppSkillsResponse is the skills inventory behind Settings > Instructions and
// skills (B740): the account's skills and the app-wide custom skills, each
// with its app-wide switch per role. Repository skills belong to a project and
// are switched on its own page.
type AppSkillsResponse struct {
	// Harnesses are every agent harness, the columns of the effect notes.
	Harnesses []string       `json:"harnesses"`
	Skills    []ProjectSkill `json:"skills"`
}

// registerApp mounts the app-wide skills routes. Settings routes are closed
// to agent tokens (httpd agent scopes), and every write here is the user's
// only, as on the project page.
func (c *ProjectSkillsController) registerApp(r chi.Router) {
	r.Get("/settings/skills", c.getApp)
	registerCustomAt(r, "/settings/skills/custom", c.appCustomStore)
}

func (c *ProjectSkillsController) getApp(w http.ResponseWriter, r *http.Request) {
	if c.DataDir == "" {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/skills")
		return
	}
	inv := skillbridge.Discover(skillbridge.OptionsFor(c.DataDir, "", nil))
	harnesses := make([]string, 0, len(domain.AllHarnesses))
	for _, h := range domain.AllHarnesses {
		harnesses = append(harnesses, string(h))
	}
	app := c.appSkillRoles(r.Context())
	resp := AppSkillsResponse{Harnesses: harnesses, Skills: []ProjectSkill{}}
	seen := map[string]bool{}
	for _, s := range inv.Skills {
		// Tallpine's own skills are not switchable; one row per name, as the
		// agents see them.
		if s.Scope == skillbridge.ScopeTallpine || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		resp.Skills = append(resp.Skills, ProjectSkill{Name: s.Name, Description: s.Description, Scope: string(s.Scope), Convention: string(s.Convention), Path: s.Path,
			Harnesses: []ProjectSkillHarness{}, Roles: skillRoles(inv, app, nil, s, harnesses), Access: skillAccess(app, nil, s.Name)})
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

// appCustomStore is the app-wide custom skills store, <dataDir>/skills-custom.
// No agent may write it: a skill switched on for the reviewer becomes part of
// every review's task.
func (c *ProjectSkillsController) appCustomStore(w http.ResponseWriter, r *http.Request, write bool, method, path string) (skillbridge.CustomStore, bool) {
	root := skillbridge.AppCustomDir(c.DataDir)
	if root == "" {
		apispec.NotImplemented(w, r, method, path)
		return skillbridge.CustomStore{}, false
	}
	if _, agent := scopedAgent(r); agent && write {
		writeAgentScopeForbidden(w, r, "create, import, edit or delete custom skills")
		return skillbridge.CustomStore{}, false
	}
	return skillbridge.CustomStore{Root: root}, true
}

// AppCustomSkillParam addresses one app-wide custom skill.
type AppCustomSkillParam struct {
	Name string `path:"name" description:"Custom skill name (one lowercase path segment)."`
}
