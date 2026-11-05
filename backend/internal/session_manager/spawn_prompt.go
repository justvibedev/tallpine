package sessionmanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/skillassets"
)

func buildPrompt(cfg ports.SpawnConfig) string {
	return buildTaskPrompt(taskPromptConfig{
		Role:           promptRoleForKind(cfg.Kind),
		Prompt:         cfg.Prompt,
		IssueID:        string(cfg.IssueID),
		IssueContext:   cfg.IssueContext,
		BacklogContext: cfg.BacklogContext,
	})
}

func promptRoleForKind(kind domain.SessionKind) sessionPromptRole {
	switch kind {
	case domain.KindOrchestrator:
		return sessionPromptRoleOrchestrator
	case domain.KindWorker:
		return sessionPromptRoleWorker
	default:
		return ""
	}
}

func promptProjectContext(projectID domain.ProjectID, project domain.ProjectRecord) promptProject {
	cfg := project.Config.WithDefaults()
	if project.Kind.WithDefault() == domain.ProjectKindScratch || cfg.DefaultBranch == domain.DefaultBranchAuto {
		cfg.DefaultBranch = ""
	}
	id := project.ID
	if strings.TrimSpace(id) == "" {
		id = string(projectID)
	}
	return promptProject{
		ID:            id,
		Name:          project.DisplayName,
		Repo:          project.RepoOriginURL,
		DefaultBranch: cfg.DefaultBranch,
		Path:          project.Path,
	}
}

func projectKindForSession(project domain.ProjectRecord, projectID domain.ProjectID) domain.ProjectKind {
	if projectID == "" {
		return domain.ProjectKindScratch
	}
	return project.Kind.WithDefault()
}

// buildSpawnTexts returns the user-facing prompt and the system prompt to
// deliver separately to the agent. Orchestrator role instructions and worker
// coordination hints are placed in the system prompt so they are treated as
// standing instructions rather than part of the human's task request. A
// promptless spawn delivers no user prompt at all: the agent simply lands at an
// empty input box rather than receiving an auto-generated kickoff turn.
func (m *Manager) buildSpawnTexts(ctx context.Context, cfg ports.SpawnConfig, sessionID domain.SessionID) (prompt, systemPrompt string, err error) {
	prompt = buildPrompt(cfg)
	systemPrompt, err = m.buildSystemPromptFor(ctx, cfg.Kind, cfg.ProjectID, sessionID, cfg.RequestedOutput, cfg.Role, domain.NormalizeSessionMode(cfg.RequestedMode) == domain.SessionModeChat)
	if err != nil {
		return "", "", err
	}
	return prompt, systemPrompt, nil
}

// buildSystemPrompt derives the standing instructions for a session of the
// given kind from current store state. Restore recomputes them through here
// rather than persisting them, so a restored worker points at the orchestrator
// that is active now, not the one from its original spawn.
// buildSystemPrompt assembles the standing instructions. chat says whether the
// agent runs as a chat session, the only mode with the html tools and tallpine render.
// requestedOutput is the deliverable requested at spawn.
func (m *Manager) buildSystemPrompt(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID, sessionID domain.SessionID, requestedOutput domain.RequestedOutput, chat bool) (string, error) {
	return m.buildSystemPromptFor(ctx, kind, projectID, sessionID, requestedOutput, "", chat)
}

// buildSystemPromptFor is buildSystemPrompt for a session with a special role:
// a planner (B205) gets the planner section, and the artifact section is
// replaced by it.
func (m *Manager) buildSystemPromptFor(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID, sessionID domain.SessionID, requestedOutput domain.RequestedOutput, role domain.SessionRole, chat bool) (string, error) {
	project, err := m.loadProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	cfg := systemPromptConfig{
		Role:       promptRoleForKind(kind),
		Standalone: projectID == "",
		Project:    promptProjectContext(projectID, project),
	}
	if kind == domain.KindOrchestrator {
		cfg.Project.AvailableAgents = m.readyAgents()
	}

	switch kind {
	case domain.KindOrchestrator:
		cfg.OrchestratorRules = project.Config.OrchestratorRules
		if m.plannerEnabled {
			cfg.AdditionalSections = append(cfg.AdditionalSections, fmt.Sprintf(plannerOrchestratorPrompt, plannerModeRule(m.planningModeFor(ctx, project.Config))))
		}
		if section := m.crossProjectPrompt(ctx, projectID, sessionID); section != "" {
			cfg.AdditionalSections = append(cfg.AdditionalSections, section)
		}
	case domain.KindWorker:
		if projectID != "" {
			orchestratorID, ok, err := m.orchestratorForSession(ctx, projectID, sessionID)
			if err != nil {
				return "", err
			}
			if ok {
				cfg.OrchestratorSessionID = string(orchestratorID)
			}
		}
		rules, err := m.workerProjectRules(sessionID, project)
		if err != nil {
			return "", err
		}
		cfg.ProjectRules = rules
		cfg.VerifyCommand = project.Config.VerifyCommand
	default:
		return "", nil
	}

	if projectID != "" {
		workspacePrompt, err := m.workspaceProjectPrompt(ctx, kind, projectID)
		if err != nil {
			return "", err
		}
		if workspacePrompt != "" {
			cfg.AdditionalSections = append(cfg.AdditionalSections, workspacePrompt)
		}
	}
	if pointer := strings.TrimSpace(m.tallpineSkillPointer(chat)); pointer != "" {
		cfg.AdditionalSections = append(cfg.AdditionalSections, pointer)
	}
	if artifactPrompt := strings.TrimSpace(m.artifactPrompt(sessionID, chat)); artifactPrompt != "" {
		cfg.AdditionalSections = append(cfg.AdditionalSections, artifactPrompt)
	}
	if kind == domain.KindWorker && role != domain.SessionRolePlanner && m.plannerEnabled {
		cfg.AdditionalSections = append(cfg.AdditionalSections, plannerWorkerPrompt)
	}
	if kind == domain.KindWorker && role == domain.SessionRolePlanner {
		cfg.AdditionalSections = append(cfg.AdditionalSections, plannerPrompt)
	} else if kind == domain.KindWorker && requestedOutput == domain.RequestedOutputArtifact {
		cfg.AdditionalSections = append(cfg.AdditionalSections, artifactOutputPrompt)
	}
	return buildSystemPromptText(cfg), nil
}

// workerProjectRules is the project rules section of a worker system prompt:
// agentRules plus agentRulesFile, capped at domain.MaxAgentRulesBytes. An
// over-cap value (saved before the cap existed, or a large rules file) is
// truncated with a visible marker and logged; it never fails the spawn.
func (m *Manager) workerProjectRules(sessionID domain.SessionID, project domain.ProjectRecord) (string, error) {
	rules, truncated, err := buildProjectRules(projectRulesConfig{
		ProjectPath:    project.Path,
		AgentRules:     project.Config.AgentRules,
		AgentRulesFile: project.Config.AgentRulesFile,
	})
	if err != nil {
		return "", err
	}
	if truncated {
		m.logger.Warn("worker project rules over cap; truncated", "session", sessionID, "project", project.ID, "limitBytes", domain.MaxAgentRulesBytes)
	}
	return rules, nil
}

// WorkerInstructions returns the project rules section a worker session
// receives in its system prompt, for read-only display. Empty when the project
// sets none or the session is not a worker.
func (m *Manager) WorkerInstructions(ctx context.Context, id domain.SessionID) (string, error) {
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return "", err
	}
	if !found || rec.Kind != domain.KindWorker || rec.ProjectID == "" {
		return "", nil
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return "", err
	}
	return m.workerProjectRules(id, project)
}

// tallpineSkillPointer is appended to every agent system prompt. It points the agent
// at the using-tallpine skill the daemon installs under the data dir, rather than
// inlining the whole CLI catalog. The path is absolute so it resolves from any
// project's worktree, not just the Tallpine repo (the only place a repo-relative
// skills/ path would exist). The skill file carries exact flags and examples,
// so the standing prompt stays a short pointer rather than a command dump.
// tallpineSkillPointer is the always-on Tallpine guide pointer. The html tools and tallpine
// render work only in chat sessions, so only a chat prompt names them.
func (m *Manager) tallpineSkillPointer(chat bool) string {
	dir := skillassets.Dir(m.dataDir)
	skillFile := filepath.ToSlash(filepath.Join(dir, "SKILL.md"))
	commandsGlob := filepath.ToSlash(filepath.Join(dir, "commands", "*.md"))
	browserFile := filepath.ToSlash(filepath.Join(dir, "commands", "browser.md"))
	previewFile := filepath.ToSlash(filepath.Join(dir, "commands", "preview.md"))
	renderFile := filepath.ToSlash(filepath.Join(dir, "commands", "render.md"))
	pointer := "\n\n" + "## Using the tallpine CLI\n\n" +
		"When using `tallpine`, read `" + skillFile + "` and only the relevant file under `" + commandsGlob + "`; do not load unrelated command guides.\n\n" +
		"## Tallpine desktop Browser panel\n\n" +
		"For frontend work, read `" + previewFile + "` before previewing or starting an app. Static file targets passed to `tallpine preview` are relative to the session workspace root, regardless of the shell's current directory: use `tallpine preview README.md`, not `../README.md`. Tallpine serves workspace files through its existing confined loopback preview; do not use `file://` or start a server just to display static files. Never create or modify `package.json` or install dependencies solely to display static files. Do not create `.tallpine/launch.json` unless the user asks. Automatically open the primary requested browser-displayable artifact immediately after creating or materially updating it, but do not replace an active application preview with a supporting asset. " +
		"For page inspection or interaction, read `" + browserFile + "` and use `tallpine browser` from this Tallpine session. Browser network capture is optional and off by default; follow that guide and never enable it for routine browser actions. " +
		"Do not use Codex/host in-app browser connectors, `agent.browsers.get(\"iab\")`, or a browser MCP for the Tallpine Browser panel: those are separate browser runtimes and cannot see or control Tallpine's session-owned page. " +
		"`tallpine browser` operates the same live page the user sees in that panel."
	if !chat {
		return pointer
	}
	return pointer + "\n\n" +
		"## Showing pages in chat\n\n" +
		"When a chart, table, diagram, or mockup is clearer than text, call `html_preview`, then `html_render`. " +
		"If you cannot see them, search your tools for them. " +
		chatToolsDeferredHint +
		"If you find nothing, read `" + renderFile + "` and use `tallpine render`. " +
		"Do not use a built-in visualize skill. " +
		"To show screenshots or local images, render them by absolute path (`<img src=\"/abs/shot.png\">`); never claim you cannot show images. " +
		chatImageRequestRule
}

// chatToolsDeferredHint covers harnesses that list MCP tools by name only
// (Claude Code's deferred tools): the agent "sees" html_render yet cannot call
// it, and an agent that never loads it falls back to listing file paths.
const chatToolsDeferredHint = "If they are listed only as deferred tools, load them first " +
	"(Claude Code: ToolSearch `select:mcp__tallpine__html_preview,mcp__tallpine__html_render`). "

// chatImageRequestRule names the request itself, so "send me screenshots"
// reaches html_render rather than a list of paths the phone cannot open.
const chatImageRequestRule = "When asked for screenshots or images, call `html_render` with one `<img>` per image; it shows them on desktop and mobile. " +
	"Never reply with only local paths: the phone cannot open them."

func (m *Manager) workspaceProjectPrompt(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID) (string, error) {
	project, err := m.loadProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return "", nil
	}
	repos, err := m.store.ListWorkspaceRepos(ctx, string(projectID))
	if err != nil {
		return "", fmt.Errorf("list workspace repos for prompt: %w", err)
	}
	switch kind {
	case domain.KindOrchestrator:
		return workspaceOrchestratorPrompt(repos), nil
	case domain.KindWorker:
		return workspaceWorkerPrompt(repos), nil
	default:
		return "", nil
	}
}

// orchestratorForSession returns the live orchestrator a worker reports to:
// the one bound to the worker's lane, else the project's default.
func (m *Manager) orchestratorForSession(ctx context.Context, project domain.ProjectID, session domain.SessionID) (domain.SessionID, bool, error) {
	recs, err := m.store.ListSessions(ctx, project)
	if err != nil {
		return "", false, fmt.Errorf("list sessions for %s: %w", project, err)
	}
	var lanes map[domain.SessionID]string
	lane := ""
	if ls, ok := m.store.(laneStore); ok {
		if lanes, err = ls.ListSessionLanes(ctx, project); err != nil {
			return "", false, err
		}
		lane = lanes[session]
	}
	rec, ok := domain.PickOrchestrator(recs, lanes, lane)
	return rec.ID, ok, nil
}

func (m *Manager) writeSystemPromptFile(id domain.SessionID, systemPrompt string) (string, error) {
	if systemPrompt == "" || strings.TrimSpace(m.dataDir) == "" {
		return "", nil
	}
	path := filepath.Join(m.systemPromptDir(id), "system.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(strings.TrimRight(systemPrompt, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (m *Manager) prepareSystemPromptFile(id domain.SessionID, harness domain.AgentHarness, systemPrompt string) (string, error) {
	path, err := m.writeSystemPromptFile(id, systemPrompt)
	if err == nil || path != "" {
		return path, err
	}
	if systemPromptFileRequired(harness) {
		return "", err
	}
	m.logger.Warn("system prompt file unavailable; falling back to inline system prompt", "session", id, "harness", harness, "err", err)
	return "", nil
}

func systemPromptFileRequired(harness domain.AgentHarness) bool {
	switch harness {
	case domain.HarnessKiro:
		return true
	default:
		return false
	}
}

func (m *Manager) systemPromptDir(id domain.SessionID) string {
	if strings.TrimSpace(m.dataDir) == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "prompts", string(id))
}

func workspaceOrchestratorPrompt(repos []domain.WorkspaceRepoRecord) string {
	return fmt.Sprintf(`## Workspace project

This project is a multi-repository workspace. Sessions start at the workspace root. The root repository is %s at path `+"`.`"+`; child repositories are nested below it.

Repositories:
%s

When spawning workers, name the repository path or paths they should work in. Work can span multiple repositories, so track deliverables, pull requests, and checks by repository.`, domain.RootWorkspaceRepoName, workspaceRepoList(repos))
}

func workspaceWorkerPrompt(repos []domain.WorkspaceRepoRecord) string {
	return fmt.Sprintf(`## Workspace project

This session is a multi-repository workspace. You start at the workspace root. The root repository is %s at path `+"`.`"+`; child repositories are nested below it.

Repositories:
%s

Before editing, identify which repository owns the task and keep changes scoped to the requested repository or repositories. If you touch root files, call that out explicitly because root changes are separate from child-repository changes.`, domain.RootWorkspaceRepoName, workspaceRepoList(repos))
}

func workspaceRepoList(repos []domain.WorkspaceRepoRecord) string {
	lines := make([]string, 0, 1+len(repos))
	lines = append(lines, fmt.Sprintf("- %s: .", domain.RootWorkspaceRepoName))
	for _, repo := range repos {
		lines = append(lines, fmt.Sprintf("- %s: %s", repo.Name, repo.RelativePath))
	}
	return strings.Join(lines, "\n")
}

// cachedReadinessReader is the optional cache-only read of the readiness
// provider: no native probes, so building a prompt never waits on a CLI.
type cachedReadinessReader interface {
	CachedAgentSnapshots() []domain.AgentReadinessSnapshot
}

// readyAgents names the harnesses the cached readiness snapshot reports
// installed and signed in. Nil when readiness is unwired or nothing is cached.
func (m *Manager) readyAgents() []string {
	reader, ok := m.agentReadiness.(cachedReadinessReader)
	if !ok {
		return nil
	}
	var out []string
	for _, snap := range reader.CachedAgentSnapshots() {
		if snap.EffectiveReadiness == domain.AgentReadinessReady {
			out = append(out, snap.ID)
		}
	}
	return out
}
