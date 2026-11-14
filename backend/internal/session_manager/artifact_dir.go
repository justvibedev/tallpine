package sessionmanager

import (
	"context"
	"os"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// artifactDir is the session's artifact directory: the stored one for a session
// created before the per-project layout, the canonical path otherwise.
func (m *Manager) artifactDir(id domain.SessionID) string {
	if id == "" || m.store == nil {
		return sessionartifacts.Dir(m.dataDir, id)
	}
	return sessionartifacts.ResolveForID(context.Background(), m.dataDir, m.store, id, false)
}

func (m *Manager) reserveArtifactDir(id domain.SessionID) string {
	dir := m.artifactDir(id)
	if dir == "" {
		return ""
	}
	if dir != sessionartifacts.LegacyDir(m.dataDir, id) && sessionartifacts.CollidesWithLegacySession(m.dataDir, id) {
		m.logger.Warn("artifact dir would sit inside another session's flat dir; reserving path only", "session", id, "path", dir)
		return dir
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		m.logger.Warn("artifact dir unavailable; reserving path only", "session", id, "path", dir, "err", err)
	}
	return dir
}

func (m *Manager) cleanupArtifactDir(id domain.SessionID) {
	dir := m.artifactDir(id)
	if dir == "" {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		m.logger.Warn("artifact dir cleanup failed", "session", id, "path", dir, "err", err)
	}
}

// removeEmptyArtifactDir drops the directory of an ended session when nothing
// was written to it; a directory with files is left for the retention rule.
func (m *Manager) removeEmptyArtifactDir(id domain.SessionID) {
	if _, err := sessionartifacts.RemoveIfEmpty(m.artifactDir(id)); err != nil {
		m.logger.Warn("empty artifact dir removal failed", "session", id, "err", err)
	}
}

// artifactPrompt tells the agent where deliverables go. Only a chat session can
// show a page in its thread, so only a chat prompt sends charts there.
func (m *Manager) artifactPrompt(id domain.SessionID, chat bool) string {
	dir := filepath.ToSlash(m.artifactDir(id))
	if dir == "" {
		return ""
	}
	inThread := ""
	if chat {
		inThread = "In a chat session, a chart, table, or diagram that answers a question goes in the thread with `html_render`, not into this directory. "
	}
	return "## Session Artifacts\n\n" +
		"Any deliverable that is not part of a pull request — a one-pager, analysis, plan, design doc, report, or other generated file — must be written to `" + dir + "`, never into the git workspace, even temporarily. " +
		"This applies even when a workspace-relative path like `docs/`, `docs/plans/`, or `notes/` would otherwise feel like the natural place for it: if it is not shipping in a PR, it does not belong in the workspace at all. " +
		inThread +
		"This is the user's preferred location for plans, specs, and other working files, and it overrides any default save path a skill or plugin names: a superpowers plan that would go to `docs/superpowers/plans/`, a spec for `docs/superpowers/specs/`, and handoff or memory notes for `docs/plans/`, `.plan/`, or `docs/memories/` all go here instead, and you do not commit them. " +
		"Screenshots and audit images go here when they are a deliverable and into the system temp directory when they are scratch; never save them in the workspace. " +
		"If a plan or other working file must live in the repository, delete it in the pull request that finishes the work. " +
		"Keep the workspace limited to code changes that will ship in a PR. Preserve any relative asset links between files you place in the artifact directory. " +
		"Create a separate document when the user requests a durable document or when the task needs a reviewable deliverable. Ordinary progress updates, concise final answers, and validation summaries can stay in chat or Tallpine report notes; do not create files solely because a response is a summary, plan, analysis, or report. " +
		"Routine test logs, command output, scratch notes, and intermediate diagnostics are working material, not deliverables. Keep them out of report attachments unless requested or needed to explain an actionable failure. Prefer one consolidated deliverable over many diagnostic files. " +
		"Scripts, source code, and other helper files you use to produce a deliverable are working material too: keep them in the system temp directory, not in the artifact directory, unless the user asked for that code as the deliverable. " +
		"This directory is local storage. Saving a file here or attaching its reference to a Tallpine report does not authorize external publishing; follow the user's publishing scope."
}

// artifactOutputPrompt is added for a session spawned with the artifact output
// kind.
const artifactOutputPrompt = "## Artifact Output\n\n" +
	"The user's request in this session is for an artifact. Write the finished deliverable as a file in the session artifact directory: HTML for visual work such as pages, dashboards, and diagrams; Markdown for documents. " +
	"Do not open a pull request, and do not commit the deliverable to the git workspace. If the request is unclear, ask the user what the artifact should contain before writing it."

func (m *Manager) cleanupSystemPromptDir(id domain.SessionID) {
	dir := m.systemPromptDir(id)
	if dir == "" {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		m.logger.Warn("system prompt cleanup failed", "session", id, "path", dir, "err", err)
	}
}
