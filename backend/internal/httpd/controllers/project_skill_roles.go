package controllers

import (
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

// skillRoles is a skill's switch per role, resolved like the launch does
// (skillbridge.RoleLaunch), with the effect on each harness.
// Effects count every source offering the name, as the launch does.
func skillRoles(inv skillbridge.Inventory, roles, app domain.SkillRoles, s skillbridge.Skill, harnesses []string) []ProjectSkillRole {
	return roleSwitches(roles, app, s.Name, skillbridge.SourceOf(s.Scope), func(role string) []ProjectSkillEffect {
		effects := make([]ProjectSkillEffect, 0, len(harnesses))
		for _, h := range harnesses {
			effects = append(effects, ProjectSkillEffect{Harness: h, Effect: string(skillbridge.EffectFor(inv, s.Name, h, role))})
		}
		return effects
	})
}

// roleSwitches resolves one skill's switch per role: the layer's own value
// (Set), else what it inherits (Default): the app switch when app has one
// (App), else the role default for src. On the app page app is nil.
func roleSwitches(roles, app domain.SkillRoles, name string, src domain.SkillSource, effects func(role string) []ProjectSkillEffect) []ProjectSkillRole {
	out := make([]ProjectSkillRole, 0, len(domain.SkillRoleNames))
	for _, role := range domain.SkillRoleNames {
		def, fromApp := domain.ResolveSkillRole(nil, app, role, name)
		if !fromApp {
			def = domain.SkillDefault(role, src)
		}
		on, set := roles[role][name]
		if !set {
			on = def
		}
		out = append(out, ProjectSkillRole{Role: role, On: on, Set: set, Default: def, App: fromApp, Effects: effects(role)})
	}
	return out
}

// pluginRoles is a Claude plugin's switch per role from claudePlugins: unset
// roles keep the account's value, except the reviewer, which defaults off.
func pluginRoles(plugins map[string]map[string]bool, p skillbridge.Plugin, harnesses []string) []ProjectSkillRole {
	out := make([]ProjectSkillRole, 0, len(domain.SkillRoleNames))
	for _, role := range domain.SkillRoleNames {
		def := p.AccountEnabled && domain.SkillDefault(role, domain.SkillSourcePlugin)
		on, set := plugins[role][p.ID]
		if !set {
			on = def
		}
		out = append(out, ProjectSkillRole{Role: role, On: on, Set: set, Default: def, Effects: skillEffects(harnesses, domain.SkillSourcePlugin, role)})
	}
	return out
}

func skillEffects(harnesses []string, src domain.SkillSource, role string) []ProjectSkillEffect {
	out := make([]ProjectSkillEffect, 0, len(harnesses))
	for _, h := range harnesses {
		out = append(out, ProjectSkillEffect{Harness: h, Effect: string(domain.SkillEffectFor(h, src, role))})
	}
	return out
}

// skillAccess is a skill's master and agent-type switches, resolved like the
// launch does (domain.SkillAllowed): unset is on, the app's value is the
// default a project's reset returns to. On the app page app is nil.
func skillAccess(roles, app domain.SkillRoles, name string) []ProjectSkillAccess {
	keys := make([]string, 1, 1+len(domain.AllHarnesses))
	keys[0] = domain.SkillMasterKey
	for _, h := range domain.AllHarnesses {
		keys = append(keys, domain.SkillAgentKey(string(h)))
	}
	out := make([]ProjectSkillAccess, 0, len(keys))
	for _, key := range keys {
		def, fromApp := domain.ResolveSkillRole(nil, app, key, name)
		if !fromApp {
			def = true
		}
		on, set := roles[key][name]
		if !set {
			on = def
		}
		out = append(out, ProjectSkillAccess{Key: key, On: on, Set: set, Default: def, App: fromApp})
	}
	return out
}
