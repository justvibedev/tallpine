package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// appSkillsOperations are Settings > Instructions and skills (B740): the
// app-wide skill inventory and the app custom skills every project sees.
func appSkillsOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/settings/skills", id: "getAppSkills", tag: "settings",
			summary: "List the account's skills and the app-wide custom skills with their app-wide switch per role",
			resps:   []respUnit{{http.StatusOK, controllers.AppSkillsResponse{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/settings/skills/custom", id: "createAppCustomSkill", tag: "settings",
			reqBody: controllers.CreateProjectCustomSkillRequest{},
			summary: "Create an app-wide custom skill, seen by every project, from SKILL.md text (user only)",
			resps:   customSkillResps(http.StatusCreated),
		},
		{
			method: http.MethodPost, path: "/api/v1/settings/skills/custom/import", id: "importAppCustomSkill", tag: "settings",
			reqBody: controllers.ImportProjectCustomSkillRequest{},
			summary: "Import a local skill directory as an app-wide custom skill, text-only: SKILL.md and Markdown references (user only)",
			resps:   customSkillResps(http.StatusCreated),
		},
		{
			method: http.MethodGet, path: "/api/v1/settings/skills/custom/{name}", id: "getAppCustomSkill", tag: "settings",
			pathParams: []any{controllers.AppCustomSkillParam{}},
			summary:    "Read an app-wide custom skill's SKILL.md and file list",
			resps:      customSkillResps(http.StatusOK),
		},
		{
			method: http.MethodPut, path: "/api/v1/settings/skills/custom/{name}", id: "updateAppCustomSkill", tag: "settings",
			pathParams: []any{controllers.AppCustomSkillParam{}}, reqBody: controllers.UpdateProjectCustomSkillRequest{},
			summary: "Replace an app-wide custom skill's SKILL.md (user only)",
			resps:   customSkillResps(http.StatusOK),
		},
		{
			method: http.MethodDelete, path: "/api/v1/settings/skills/custom/{name}", id: "deleteAppCustomSkill", tag: "settings",
			pathParams: []any{controllers.AppCustomSkillParam{}},
			summary:    "Delete an app-wide custom skill (user only)",
			resps:      []respUnit{{http.StatusNoContent, nil}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusForbidden, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
	}
}
