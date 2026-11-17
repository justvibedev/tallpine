package sessionmanager

import (
	"context"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
	"tallpine.eu/tallpine/backend/internal/skillsprompt"
)

// skillInventory scans the skills a session of the project at projectPath
// can see, with the same discovery the skills page shows. An empty path still
// finds the account's skills and plugins.
func (m *Manager) skillInventory(projectPath string, env map[string]string) skillbridge.Inventory {
	return skillbridge.Discover(skillbridge.OptionsFor(m.dataDir, projectPath, env))
}

// launchSkillInventory is skillInventory for a launch of harness. Kiro lists
// repo skills by path in the session worktree, so for Kiro an uncommitted
// skill is marked pending (not in the worktree yet) and left out.
func (m *Manager) launchSkillInventory(projectPath string, env map[string]string, harness domain.AgentHarness) skillbridge.Inventory {
	opts := skillbridge.OptionsFor(m.dataDir, projectPath, env)
	if harness == domain.HarnessKiro && projectPath != "" {
		opts.Committed = skillbridge.GitCommitted(projectPath)
	}
	return skillbridge.Discover(opts)
}

// projectPath is the project's checkout, or "" when it cannot be read; the
// launch then switches only the account's skills.
func (m *Manager) projectPath(ctx context.Context, id domain.ProjectID) string {
	if m.store == nil || id == "" {
		return ""
	}
	record, err := m.loadProject(ctx, id)
	if err != nil {
		return ""
	}
	return record.Path
}

// roleSkillLaunch is what the project's skill switches give a launch of a
// session of kind on harness, with the app switches (app) under the project's: the encoded domain.SkillLaunch and the Claude
// enabledPlugins override ("" keeps the account's plugins).
func roleSkillLaunch(inv skillbridge.Inventory, project domain.ProjectConfig, app domain.SkillRoles, kind domain.SessionKind, harness domain.AgentHarness) (skills, plugins string) {
	launch, plugins := skillbridge.RoleLaunch(inv, project, app, domain.SessionRoleFor(kind), string(harness))
	return launch.Encode(), plugins
}

// skillsOffSection tells a worker or orchestrator which skills are switched
// off for its role. Where the harness hides them (SkillEffectFor enforced)
// this repeats the launch; where it cannot (advisory), it is the switch.
func skillsOffSection(project domain.ProjectConfig, app domain.SkillRoles, kind domain.SessionKind, harness domain.AgentHarness) string {
	off := domain.SkillsOffFor(project.SkillRoles, app, domain.SessionRoleFor(kind), string(harness))
	if len(off) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Skills switched off for this session\n\n")
	b.WriteString("The owner switched these skills off for your role. Do not load, read or follow them, even if your agent lists them:\n")
	for _, name := range off {
		b.WriteString("- " + strconv.Quote(name) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// skillsAvailableSection lists the project's and the app's custom skills
// switched on for a worker's or orchestrator's role (project, then app
// switches), with the same builder the reviewer's task
// file uses. Custom skills live under the data dir, which sessions read.
func (m *Manager) skillsAvailableSection(project domain.ProjectRecord, app domain.SkillRoles, kind domain.SessionKind, harness domain.AgentHarness) string {
	if m.dataDir == "" || project.ID == "" {
		return ""
	}
	opts := skillbridge.OptionsFor(m.dataDir, project.Path, project.Config.Env).WithProject(project.ID)
	entries := skillbridge.AvailableFor(skillbridge.Discover(opts), project.Config, app, domain.SessionRoleFor(kind), string(harness))
	return skillsprompt.AvailableSection(entries, nil)
}
