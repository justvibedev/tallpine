package sessionmanager

import (
	"context"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
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
// repo skills by path in the session worktree, so for Kiro a skill missing
// from the ref that worktree is cut from is marked pending and left out.
func (m *Manager) launchSkillInventory(ctx context.Context, project domain.ProjectRecord, env map[string]string, harness domain.AgentHarness) skillbridge.Inventory {
	opts := skillbridge.OptionsFor(m.dataDir, project.Path, env)
	if harness == domain.HarnessKiro && project.Path != "" {
		opts.Committed = m.committedAtSessionBase(ctx, project)
	}
	return skillbridge.Discover(opts)
}

// committedAtSessionBase judges whether a checkout path reaches a new session:
// it is on the ref session worktrees are cut from, which can differ from the
// checkout's HEAD (another branch checked out, or the remote ahead). Resolution
// is local only; spawn has already refreshed the ref. Without a resolved ref
// it falls back to HEAD.
func (m *Manager) committedAtSessionBase(ctx context.Context, project domain.ProjectRecord) func(string) bool {
	if resolver, ok := m.workspace.(ports.WorkspaceDefaultBranchRefresher); ok && project.Kind.WithDefault() != domain.ProjectKindScratch {
		if t, err := resolver.ResolveDefaultBranch(ctx, project.Path, project.Config.WorktreeBaseBranch()); err == nil && t.BaseRef != "" {
			return skillbridge.CommittedAt(project.Path, t.BaseRef)
		}
	}
	return skillbridge.GitCommitted(project.Path)
}

// projectRecord is the project, or the zero record (no checkout path) when it
// cannot be read; the launch then switches only the account's skills.
func (m *Manager) projectRecord(ctx context.Context, id domain.ProjectID) domain.ProjectRecord {
	if m.store == nil || id == "" {
		return domain.ProjectRecord{}
	}
	record, err := m.loadProject(ctx, id)
	if err != nil {
		return domain.ProjectRecord{}
	}
	return record
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
