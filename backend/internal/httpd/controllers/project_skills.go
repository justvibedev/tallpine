package controllers

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"sort"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

// ProjectSkillsController serves the read-only inventory behind Settings >
// Instructions and skills: every skill any harness convention offers for a
// project, where it lives, which harnesses see it, and the account's Claude
// Code plugins. It scans with the same code the launch path bridges with.
type ProjectSkillsController struct {
	Projects AgentPolicyProjects
	// DefaultBranch resolves the ref session worktrees start from, the same
	// resolution session spawn uses. Nil disables committing.
	DefaultBranch DefaultBranchResolver
	// GitCredentials fetches and pushes as the project's GitHub account. Nil
	// keeps the machine's git login.
	GitCredentials ports.ProjectGitCredentials
	// DataDir is the daemon data dir, whose skills Tallpine provides to every
	// session. Empty leaves them out of the inventory.
	DataDir string
	// AppDefaults supplies the app-wide skill switches a project inherits
	// (B740). Nil means none.
	AppDefaults AppDefaultsReader
}

// AppDefaultsReader reads the app-wide workflow defaults.
type AppDefaultsReader interface {
	WorkflowDefaults(ctx context.Context) domain.WorkflowDefaults
}

// appSkillRoles is the app layer of the skill switches, nil without one.
func (c *ProjectSkillsController) appSkillRoles(ctx context.Context) domain.SkillRoles {
	if c.AppDefaults == nil {
		return nil
	}
	return c.AppDefaults.WorkflowDefaults(ctx).SkillRoles
}

// ProjectSkillHarness is how one harness sees a skill or the instruction file:
// native, pending (in the checkout but not committed, so sessions do not get it yet), missing (the harness cannot see it), shadowed (another skill
// with the same name wins), none (no instruction file) or unverified.
type ProjectSkillHarness struct {
	Harness string `json:"harness"`
	Status  string `json:"status" enum:"native,pending,missing,shadowed,none,unverified,listed"`
}

// ProjectSkill is one skill found for a project.
type ProjectSkill struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Scope       string                `json:"scope" enum:"repo,user,tallpine,custom,reviewer-repo"`
	Convention  string                `json:"convention" enum:"claude,agents,codex,tallpine,custom,custom-app,reviewer-repo"`
	Path        string                `json:"path"`
	Harnesses   []ProjectSkillHarness `json:"harnesses"`
	// Deselected is true when the user chose not to bridge this repo skill.
	Deselected bool `json:"deselected"`
	// Removable is true when Path is a link or copy the bridge created in the
	// checkout (its source skill is a different folder).
	Removable bool `json:"removable"`
	// Roles are the skill's per-role switches (skillRoles) and what each does
	// on every harness.
	Roles []ProjectSkillRole `json:"roles"`
	// Access is the master switch (key "all") and one switch per agent type
	// (keys agentCodex etc.); off ones cut the skill off for every role.
	Access []ProjectSkillAccess `json:"access"`
}

// ProjectSkillAccess is the master or an agent-type switch for a skill, with
// the same On/Set/Default/App meaning as ProjectSkillRole. Unset means on.
type ProjectSkillAccess struct {
	Key     string `json:"key"`
	On      bool   `json:"on"`
	Set     bool   `json:"set"`
	Default bool   `json:"default"`
	App     bool   `json:"app"`
}

// ProjectSkillRole is one role's switch for a skill or plugin. On is the
// resolved value; Set says this layer (the project, or the app on the app
// page) chose it; Default is the value when unset, which a reset returns to:
// the app switch when App is true, else the role default (reviewers default
// off, B740 q1).
type ProjectSkillRole struct {
	Role    string `json:"role" enum:"worker,orchestrator,reviewer"`
	On      bool   `json:"on"`
	Set     bool   `json:"set"`
	Default bool   `json:"default"`
	// App says Default is the app-wide switch (the row follows the app default).
	App     bool                 `json:"app"`
	Effects []ProjectSkillEffect `json:"effects"`
}

// ProjectSkillEffect is what the switch does on one harness: enforced (the
// launch hides an off skill), advisory (only the prompt says so) or
// unsupported (the switch changes nothing there).
type ProjectSkillEffect struct {
	Harness string `json:"harness"`
	Effect  string `json:"effect" enum:"enforced,advisory,unsupported"`
}

// ProjectPlugin is a Claude Code plugin from the account's Claude config. Its
// per-role switches are the project's claudePlugins setting; a role without an
// override keeps accountEnabled.
type ProjectPlugin struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Marketplace    string `json:"marketplace"`
	Description    string `json:"description"`
	Version        string `json:"version"`
	Path           string `json:"path"`
	Installed      bool   `json:"installed"`
	AccountEnabled bool   `json:"accountEnabled"`
	// Roles are the plugin's per-role switches (claudePlugins).
	Roles []ProjectSkillRole `json:"roles"`
}

// ProjectSkillCollision is a name clash: the winner is used, the loser is left alone.
type ProjectSkillCollision struct {
	Name   string `json:"name"`
	Winner string `json:"winner"`
	Loser  string `json:"loser"`
	Reason string `json:"reason"`
}

// ProjectSkillOfferItem is one file or link an offer would create, relative to
// the project checkout.
type ProjectSkillOfferItem struct {
	Path    string `json:"path"`
	Kind    string `json:"kind" enum:"file,link"`
	Target  string `json:"target,omitempty"`
	Content string `json:"content,omitempty"`
}

// ProjectSkillOffer is a change the user may apply to make the project visible
// to a harness. Nothing happens until it is applied.
type ProjectSkillOffer struct {
	ID      string                  `json:"id" enum:"claude-md,claude-skills,codex-md,codex-skills"`
	Harness string                  `json:"harness"`
	Items   []ProjectSkillOfferItem `json:"items"`
}

// ProjectSkillOfferParam addresses one offer of a project.
type ProjectSkillOfferParam struct {
	ID      string `path:"id" description:"Project identifier (registry key)."`
	OfferID string `path:"offerId" description:"Offer identifier from the skills inventory."`
}

// ApplyProjectSkillOfferResponse lists what was created. Mode is file, symlink
// or copy.
type ApplyProjectSkillOfferResponse struct {
	Created []ProjectSkillCreated `json:"created"`
}

// ProjectSkillCreated is one created path.
type ProjectSkillCreated struct {
	Path string `json:"path"`
	Mode string `json:"mode" enum:"file,symlink,copy"`
}

// ProjectSkillsResponse is the skills inventory of one project.
type ProjectSkillsResponse struct {
	ProjectID    string                `json:"projectId"`
	Workspace    string                `json:"workspace"`
	AgentsMd     bool                  `json:"agentsMd"`
	ClaudeMd     bool                  `json:"claudeMd"`
	Instructions []ProjectSkillHarness `json:"instructions"`
	// Harnesses are the agent harnesses the project launches (default claude-code).
	Harnesses []string            `json:"harnesses"`
	Offers    []ProjectSkillOffer `json:"offers"`
	// OffersEnabled is false once the user chose "don't ask again" for the project:
	// the notice on project add and session start stays away; the list stays.
	OffersEnabled bool `json:"offersEnabled"`
	// PendingCommit lists instruction files and skill directories present in
	// the checkout but not committed; sessions run in worktrees of a commit and
	// do not see them until they are committed and merged.
	PendingCommit []string                `json:"pendingCommit"`
	Skills        []ProjectSkill          `json:"skills"`
	Collisions    []ProjectSkillCollision `json:"collisions"`
	// Plugins are the Claude Code plugins the account config offers.
	Plugins []ProjectPlugin `json:"plugins"`
	// CommitTarget is where "Commit" publishes pending files: the branch new
	// sessions start from. Absent when it cannot be resolved.
	CommitTarget *ProjectSkillCommitTarget `json:"commitTarget,omitempty"`
}

// Register mounts the skills route.
func (c *ProjectSkillsController) Register(r chi.Router) {
	r.Get("/projects/{id}/skills", c.get)
	r.Post("/projects/{id}/skills/offers/{offerId}/apply", c.apply)
	c.registerBranchCheck(r)
	r.Post("/projects/{id}/skills/remove", c.remove)
	c.registerCustom(r)
	c.registerApp(r)
	r.Post("/projects/{id}/skills/commit", c.commit)
	r.Post("/projects/{id}/skills/push-branch", c.pushBranch)
}

// projectScan is what the skills routes need of a project.
type projectScan struct {
	id            domain.ProjectID
	path          string
	env           map[string]string
	harnesses     []string
	offersEnabled bool
	// baseBranch is the configured default branch ("" means infer it).
	baseBranch string
	// deselected are skill names the user chose not to bridge.
	deselected []string
	// skillRoles and claudePlugins are the per-role switches.
	skillRoles    domain.SkillRoles
	claudePlugins map[string]map[string]bool
	// appSkillRoles are the app-wide switches the project inherits.
	appSkillRoles domain.SkillRoles
}

// scan resolves the project's checkout, its env and the harnesses it launches.
func (c *ProjectSkillsController) scan(w http.ResponseWriter, r *http.Request) (projectScan, bool) {
	res, err := c.Projects.Get(r.Context(), domain.ProjectID(chi.URLParam(r, "id")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return projectScan{}, false
	}
	if res.Project == nil {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PROJECT_DEGRADED", "project config could not be loaded", nil)
		return projectScan{}, false
	}
	out := projectScan{id: domain.ProjectID(chi.URLParam(r, "id")), path: res.Project.Path, offersEnabled: true, appSkillRoles: c.appSkillRoles(r.Context())}
	seen := map[string]bool{}
	add := func(h string) {
		if h != "" && !seen[h] {
			seen[h] = true
			out.harnesses = append(out.harnesses, h)
		}
	}
	if cfg := res.Project.Config; cfg != nil {
		out.env = cfg.Env
		out.offersEnabled = !cfg.SkillsOfferOff
		out.baseBranch = cfg.WorktreeBaseBranch()
		out.deselected = cfg.SkillsDeselected
		out.skillRoles, out.claudePlugins = cfg.SkillRoles, cfg.ClaudePlugins
		add(string(cfg.Worker.Harness))
		add(string(cfg.Orchestrator.Harness))
	}
	add(res.Project.Agent)
	if len(out.harnesses) == 0 {
		add(string(domain.FallbackHarness))
	}
	return out, true
}

// apply creates the files of one offer in the project's main checkout as plain
// uncommitted changes. It never overwrites and never touches git.
func (c *ProjectSkillsController) apply(w http.ResponseWriter, r *http.Request) {
	if c.Projects == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/{id}/skills/offers/{offerId}/apply")
		return
	}
	p, ok := c.scan(w, r)
	if !ok {
		return
	}
	made, err := skillbridge.ApplyOfferExcept(p.path, p.harnesses, p.deselected, chi.URLParam(r, "offerId"))
	if errors.Is(err, fs.ErrNotExist) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SKILL_OFFER_NOT_FOUND", "this offer no longer applies to the project", nil)
		return
	}
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SKILL_OFFER_FAILED", err.Error(), nil)
		return
	}
	resp := ApplyProjectSkillOfferResponse{Created: []ProjectSkillCreated{}}
	for _, m := range made {
		resp.Created = append(resp.Created, ProjectSkillCreated{Path: m.Path, Mode: m.Mode})
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *ProjectSkillsController) get(w http.ResponseWriter, r *http.Request) {
	if c.Projects == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{id}/skills")
		return
	}
	p, ok := c.scan(w, r)
	if !ok {
		return
	}
	path, env, project, enabled := p.path, p.env, p.harnesses, p.offersEnabled
	id := chi.URLParam(r, "id")
	opts := skillbridge.OptionsFor(c.DataDir, path, env).WithProject(id)
	opts.Committed = skillbridge.GitCommitted(path)
	inv := skillbridge.Discover(opts)
	harnesses := make([]string, 0, len(domain.AllHarnesses))
	for _, h := range domain.AllHarnesses {
		harnesses = append(harnesses, string(h))
	}
	statuses := make(map[string][]string, len(harnesses))
	for _, h := range harnesses {
		statuses[h] = skillbridge.Statuses(inv, h)
	}
	resp := ProjectSkillsResponse{
		ProjectID: id, Workspace: path, Harnesses: project, OffersEnabled: enabled, PendingCommit: append([]string{}, inv.PendingCommit...),
		Offers:   []ProjectSkillOffer{},
		AgentsMd: inv.Instructions.AgentsMD, ClaudeMd: inv.Instructions.ClaudeMD,
		Instructions: []ProjectSkillHarness{}, Skills: []ProjectSkill{}, Collisions: []ProjectSkillCollision{}, Plugins: []ProjectPlugin{},
	}
	for _, pl := range inv.Plugins {
		resp.Plugins = append(resp.Plugins, ProjectPlugin{ID: pl.ID, Name: pl.Name, Marketplace: pl.Marketplace, Description: pl.Description, Version: pl.Version, Path: pl.Path, Installed: pl.Installed, AccountEnabled: pl.AccountEnabled,
			Roles: pluginRoles(p.claudePlugins, pl, harnesses)})
	}
	for _, h := range harnesses {
		resp.Instructions = append(resp.Instructions, ProjectSkillHarness{Harness: h, Status: skillbridge.InstructionStatus(inv.Instructions, h)})
	}
	for i, s := range inv.Skills {
		sk := ProjectSkill{Name: s.Name, Description: s.Description, Scope: string(s.Scope), Convention: string(s.Convention), Path: s.Path, Harnesses: []ProjectSkillHarness{},
			Roles: skillRoles(inv, p.skillRoles, p.appSkillRoles, s, harnesses), Access: skillAccess(p.skillRoles, p.appSkillRoles, s.Name)}
		if s.Scope == skillbridge.ScopeRepo {
			sk.Deselected = slices.Contains(p.deselected, s.Name)
			if rel, err := filepath.Rel(path, s.Path); err == nil {
				sk.Removable = skillbridge.Removable(path, filepath.ToSlash(rel))
			}
		}
		for _, h := range harnesses {
			sk.Harnesses = append(sk.Harnesses, ProjectSkillHarness{Harness: h, Status: statuses[h][i]})
		}
		resp.Skills = append(resp.Skills, sk)
	}
	for _, o := range skillbridge.OffersExcept(path, project, p.deselected) {
		po := ProjectSkillOffer{ID: o.ID, Harness: o.Harness, Items: []ProjectSkillOfferItem{}}
		for _, it := range o.Items {
			po.Items = append(po.Items, ProjectSkillOfferItem{Path: it.Path, Kind: it.Kind, Target: it.Target, Content: it.Content})
		}
		resp.Offers = append(resp.Offers, po)
	}
	resp.Skills = append(resp.Skills, c.reviewerRepoRows(r, p, harnesses)...)
	sort.SliceStable(resp.Skills, func(a, b int) bool { return resp.Skills[a].Name < resp.Skills[b].Name })
	for _, col := range inv.Collisions {
		resp.Collisions = append(resp.Collisions, ProjectSkillCollision{Name: col.Name, Winner: col.Winner, Loser: col.Loser, Reason: col.Reason})
	}
	resp.CommitTarget = c.commitTarget(r.Context(), path, p.baseBranch)
	envelope.WriteJSON(w, http.StatusOK, resp)
}
