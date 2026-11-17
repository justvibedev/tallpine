package skillbridge

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/skillsprompt"
)

// SourceOf maps a skill scope to the source its switch defaults and effects
// are decided by.
func SourceOf(s Scope) domain.SkillSource {
	switch s {
	case ScopeRepo:
		return domain.SkillSourceRepo
	case ScopeUser:
		return domain.SkillSourceUser
	case ScopePlugin:
		return domain.SkillSourcePlugin
	case ScopeCustom:
		return domain.SkillSourceCustom
	case ScopeReviewerRepo:
		return domain.SkillSourceReviewerRepo
	}
	return domain.SkillSourceTallpine
}

// RoleLaunch works out what a role's skill switches give one launch of
// harness: the skills to hide or (Kiro) to list, and the Claude enabledPlugins
// JSON ("" keeps the account's plugins). app is the app-wide layer (nil until
// it exists). Only switches whose SkillEffectFor is enforced change the launch;
// advisory ones reach the agent through its prompt.
func RoleLaunch(inv Inventory, project domain.ProjectConfig, app domain.SkillRoles, role, harness string) (domain.SkillLaunch, string) {
	var out domain.SkillLaunch
	listed := map[string]bool{}
	// Every source, duplicates included: a reviewer skips the checkout's copy
	// of a name but still loads the account's, so each entry counts by its
	// own scope (not only the name's winner).
	for _, s := range inv.Skills {
		src := SourceOf(s.Scope)
		// Prompt-delivered skills are no harness's to hide or list: a switched
		// off one is simply not in the prompt (AvailableFor).
		if domain.SkillPromptDelivered(src) || domain.SkillEffectFor(harness, src, role) != domain.SkillEnforced {
			continue
		}
		on := domain.SkillOn(project.SkillRoles, app, role, harness, s.Name, src)
		if harness == string(domain.HarnessKiro) {
			// One resource per name, in source precedence; an uncommitted
			// repo skill is not in the session worktree yet.
			if on && !s.Pending && !listed[s.Name] {
				listed[s.Name] = true
				out.Resources = append(out.Resources, kiroSkillResource(inv.Workspace, s))
			}
			continue
		}
		if !on {
			out.Off = appendNames(out.Off, s.Name, s.DeclaredName)
		}
	}
	slices.Sort(out.Off)
	return out, rolePlugins(inv, project, role)
}

// EffectFor is what a role's switch for the skill name does on harness: the
// strongest effect over every source offering that name (enforced, then
// advisory, then unsupported), matching RoleLaunch.
func EffectFor(inv Inventory, name, harness, role string) domain.SkillEffect {
	best := domain.SkillUnsupported
	for _, s := range inv.Skills {
		if s.Name != name {
			continue
		}
		switch e := domain.SkillEffectFor(harness, SourceOf(s.Scope), role); e {
		case domain.SkillEnforced:
			return e
		case domain.SkillAdvisory:
			best = e
		}
	}
	return best
}

func appendNames(dst []string, names ...string) []string {
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" && !slices.Contains(dst, n) {
			dst = append(dst, n)
		}
	}
	return dst
}

// kiroSkillResource is the skill:// resource of s. A repo skill is relative,
// so it resolves in the session worktree rather than the owner's checkout.
func kiroSkillResource(workspace string, s Skill) string {
	path := filepath.Join(s.Path, "SKILL.md")
	if s.Scope == ScopeRepo && workspace != "" {
		if rel, err := filepath.Rel(workspace, path); err == nil && !strings.HasPrefix(rel, "..") {
			path = rel
		}
	}
	return "skill://" + filepath.ToSlash(path)
}

// rolePlugins is the role's enabledPlugins override: its claudePlugins entry,
// and for the reviewer every other discovered plugin off (B740 q1).
func rolePlugins(inv Inventory, project domain.ProjectConfig, role string) string {
	plugins := map[string]bool{}
	for k, v := range project.ClaudePlugins[role] {
		if k = strings.TrimSpace(k); k != "" {
			plugins[k] = v
		}
	}
	if !domain.SkillDefault(role, domain.SkillSourcePlugin) {
		for _, p := range inv.Plugins {
			if _, set := plugins[p.ID]; !set {
				plugins[p.ID] = false
			}
		}
	}
	if len(plugins) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(plugins) // sorted keys: canonical
	return string(encoded)
}

// AvailableFor lists the custom skills switched on for role on harness, by SKILL.md
// path, for the prompt's "Skills available to you" section. A custom skill
// whose name a higher-priority source owns is left out (that skill wins).
func AvailableFor(inv Inventory, project domain.ProjectConfig, app domain.SkillRoles, role, harness string) []skillsprompt.Entry {
	var out []skillsprompt.Entry
	for _, s := range inv.Winners {
		if s.Scope != ScopeCustom || !domain.SkillOn(project.SkillRoles, app, role, harness, s.Name, domain.SkillSourceCustom) {
			continue
		}
		out = append(out, skillsprompt.Entry{Name: s.Name, Description: s.Description, Path: filepath.Join(s.Path, "SKILL.md")})
	}
	return out
}
