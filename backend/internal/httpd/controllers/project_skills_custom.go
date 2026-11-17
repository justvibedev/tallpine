package controllers

import (
	"errors"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

// ProjectCustomSkill is one owner-managed custom skill of a project.
type ProjectCustomSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	// Content is the SKILL.md text.
	Content string `json:"content"`
	// Files lists the skill's other Markdown files, relative to its directory.
	Files []string `json:"files"`
	// Skipped names the files an import left out (scripts, executables,
	// symlinks, non-Markdown files).
	Skipped []string `json:"skipped"`
}

// ProjectCustomSkillParam addresses one custom skill.
type ProjectCustomSkillParam struct {
	ID   string `path:"id" description:"Project identifier (registry key)."`
	Name string `path:"name" description:"Custom skill name (one lowercase path segment)."`
}

// CreateProjectCustomSkillRequest creates a custom skill from SKILL.md text.
type CreateProjectCustomSkillRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// ImportProjectCustomSkillRequest copies a local skill directory, text-only.
type ImportProjectCustomSkillRequest struct {
	Name string `json:"name"`
	// Source is the absolute path of a skill directory holding SKILL.md.
	Source string `json:"source"`
}

// UpdateProjectCustomSkillRequest replaces a custom skill's SKILL.md.
type UpdateProjectCustomSkillRequest struct {
	Content string `json:"content"`
}

func (c *ProjectSkillsController) registerCustom(r chi.Router) {
	registerCustomAt(r, "/projects/{id}/skills/custom", c.customStore)
}

// customStoreFor resolves the custom skills store of a request (project or
// app), answering the request itself when it cannot.
type customStoreFor func(w http.ResponseWriter, r *http.Request, write bool, method, path string) (skillbridge.CustomStore, bool)

// registerCustomAt mounts create, import, read, edit and delete of the custom
// skills store resolves under base.
func registerCustomAt(r chi.Router, base string, store customStoreFor) {
	api := "/api/v1" + base
	r.Post(base, func(w http.ResponseWriter, r *http.Request) { createCustom(w, r, store, api) })
	r.Post(base+"/import", func(w http.ResponseWriter, r *http.Request) { importCustom(w, r, store, api+"/import") })
	r.Get(base+"/{name}", func(w http.ResponseWriter, r *http.Request) { getCustom(w, r, store, api+"/{name}") })
	r.Put(base+"/{name}", func(w http.ResponseWriter, r *http.Request) { updateCustom(w, r, store, api+"/{name}") })
	r.Delete(base+"/{name}", func(w http.ResponseWriter, r *http.Request) { deleteCustom(w, r, store, api+"/{name}") })
}

// customStore resolves the project's custom skills store. Writes are the
// user's only: a custom skill switched on for the reviewer becomes part of
// its task, so no agent may author one (as with skillRoles.reviewer).
func (c *ProjectSkillsController) customStore(w http.ResponseWriter, r *http.Request, write bool, method, path string) (skillbridge.CustomStore, bool) {
	if c.Projects == nil || c.DataDir == "" {
		apispec.NotImplemented(w, r, method, path)
		return skillbridge.CustomStore{}, false
	}
	if _, agent := scopedAgent(r); agent && write {
		writeAgentScopeForbidden(w, r, "create, import, edit or delete custom skills")
		return skillbridge.CustomStore{}, false
	}
	id := chi.URLParam(r, "id")
	if res, err := c.Projects.Get(r.Context(), domain.ProjectID(id)); err != nil {
		envelope.WriteError(w, r, err)
		return skillbridge.CustomStore{}, false
	} else if res.Project == nil {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PROJECT_DEGRADED", "project config could not be loaded", nil)
		return skillbridge.CustomStore{}, false
	}
	root := skillbridge.CustomDir(c.DataDir, id)
	if root == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CUSTOM_SKILL_INVALID", "project id cannot hold custom skills", nil)
		return skillbridge.CustomStore{}, false
	}
	return skillbridge.CustomStore{Root: root}, true
}

func writeCustomSkill(w http.ResponseWriter, r *http.Request, status int, s skillbridge.CustomSkill, skipped []string, err error) {
	switch {
	case errors.Is(err, skillbridge.ErrSkillExists):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "CUSTOM_SKILL_EXISTS", err.Error(), nil)
	case errors.Is(err, fs.ErrNotExist):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "CUSTOM_SKILL_NOT_FOUND", "no custom skill with that name", nil)
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CUSTOM_SKILL_INVALID", err.Error(), nil)
	default:
		if skipped == nil {
			skipped = []string{}
		}
		files := s.Files
		if files == nil {
			files = []string{}
		}
		envelope.WriteJSON(w, status, ProjectCustomSkill{Name: s.Name, Description: s.Description, Path: s.Path, Content: s.Content, Files: files, Skipped: skipped})
	}
}

func createCustom(w http.ResponseWriter, r *http.Request, resolve customStoreFor, path string) {
	store, ok := resolve(w, r, true, "POST", path)
	if !ok {
		return
	}
	var in CreateProjectCustomSkillRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CUSTOM_SKILL_INVALID", "invalid request body", nil)
		return
	}
	s, err := store.Create(in.Name, in.Content)
	writeCustomSkill(w, r, http.StatusCreated, s, nil, err)
}

func importCustom(w http.ResponseWriter, r *http.Request, resolve customStoreFor, path string) {
	store, ok := resolve(w, r, true, "POST", path)
	if !ok {
		return
	}
	var in ImportProjectCustomSkillRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CUSTOM_SKILL_INVALID", "invalid request body", nil)
		return
	}
	s, skipped, err := store.Import(in.Name, in.Source)
	writeCustomSkill(w, r, http.StatusCreated, s, skipped, err)
}

func getCustom(w http.ResponseWriter, r *http.Request, resolve customStoreFor, path string) {
	store, ok := resolve(w, r, false, "GET", path)
	if !ok {
		return
	}
	s, err := store.Get(chi.URLParam(r, "name"))
	writeCustomSkill(w, r, http.StatusOK, s, nil, err)
}

func updateCustom(w http.ResponseWriter, r *http.Request, resolve customStoreFor, path string) {
	store, ok := resolve(w, r, true, "PUT", path)
	if !ok {
		return
	}
	var in UpdateProjectCustomSkillRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CUSTOM_SKILL_INVALID", "invalid request body", nil)
		return
	}
	s, err := store.Update(chi.URLParam(r, "name"), in.Content)
	writeCustomSkill(w, r, http.StatusOK, s, nil, err)
}

func deleteCustom(w http.ResponseWriter, r *http.Request, resolve customStoreFor, path string) {
	store, ok := resolve(w, r, true, "DELETE", path)
	if !ok {
		return
	}
	if err := store.Delete(chi.URLParam(r, "name")); err != nil {
		writeCustomSkill(w, r, 0, skillbridge.CustomSkill{}, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reviewerRepoRows are the skills page rows of the default branch's
// .tallpine/reviewer-skills: reviewer column only, read at the resolved base
// ref without fetching (the page only reads).
func (c *ProjectSkillsController) reviewerRepoRows(r *http.Request, p projectScan, harnesses []string) []ProjectSkill {
	if p.path == "" {
		return nil
	}
	target, ok := c.resolveTarget(r.Context(), p.path, p.baseBranch, false, nil)
	if !ok || target.BaseRef == "" {
		return nil
	}
	list, err := skillbridge.ReviewerRepoSkills(r.Context(), p.path, target.BaseRef)
	if err != nil {
		return nil
	}
	out := make([]ProjectSkill, 0, len(list))
	for _, s := range list {
		sk := ProjectSkill{Name: s.Name, Description: s.Description, Scope: string(s.Scope), Convention: string(s.Convention), Path: s.Path, Harnesses: []ProjectSkillHarness{}}
		for _, h := range harnesses {
			sk.Harnesses = append(sk.Harnesses, ProjectSkillHarness{Harness: h, Status: skillbridge.StatusListed})
		}
		sk.Roles = roleSwitches(p.skillRoles, p.appSkillRoles, s.Name, domain.SkillSourceReviewerRepo, func(role string) []ProjectSkillEffect {
			return skillEffects(harnesses, domain.SkillSourceReviewerRepo, role)
		})
		out = append(out, sk)
	}
	return out
}
