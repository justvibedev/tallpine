package sessionmanager

import (
	"context"
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// recoversLostConversation reports a resume that failed because the agent
// itself says the saved conversation does not exist (ACP -32002, a missing
// Codex thread), for a session that can go on without it. An orchestrator
// can: the daemon holds its state, so it rebuilds from there in a fresh
// conversation. A worker keeps the refusal, since its conversation is its work.
//
// B673's fallbacks never reach a handoff successor (B678): its branch is native,
// not a switch with a note, and its project conversation carries the previous
// orchestrator's history, so nothing proves the id was never used. Resuming it
// then failed on every attempt, and the session stayed stuck.
func recoversLostConversation(rec domain.SessionRecord, err error) bool {
	return rec.Kind == domain.KindOrchestrator && rec.Metadata.ProviderConversationID != "" &&
		errors.Is(err, ports.ErrChatResumeFailed) && errors.Is(err, ports.ErrChatConversationMissing)
}

// lostConversationConfirmed asks the harness's own transcript probe whether
// the saved conversation is really gone. Discarding it cannot be undone, and a
// -32002 can also come from the environment (a wrong config dir, a broken
// shared link) while the transcript is still on disk. Only a probe that finds
// no transcript confirms the loss; one that finds it, fails, or does not exist
// keeps the refusal. The probe runs under the checked launch environment: if
// the session's account environment is incomplete, the failed launch and the
// probe would both look in the same wrong place, so that too keeps the refusal.
func (m *Manager) lostConversationConfirmed(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) bool {
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return false
	}
	probe, ok := agent.(ports.AgentInterfaceHandoffHistoryProbe)
	if !ok {
		return false
	}
	env, err := m.runtimeEnvChecked(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env, m.accountOverride(rec.ID), rec.Harness)
	if err != nil {
		m.logger.Warn("chat: saved orchestrator conversation not confirmed lost; account environment incomplete",
			"session", rec.ID, "error", err)
		return false
	}
	persisted, err := m.persistedNativeConversationIDIn(ctx, rec, rec.Metadata.ProviderConversationID, probe, env)
	return errors.Is(err, ErrNativeConversationMissing) || (err == nil && persisted == "")
}

// lostConversationStart turns a failed resume into a fresh start of the same
// session. With no provider id the chat service reserves a new provider
// boundary on the conversation and records the context reset on its timeline;
// the note is stored on that boundary and seeds the fresh agent.
func lostConversationStart(start ChatStart, rec domain.SessionRecord, artifactDir string) ChatStart {
	start.ProviderConversationID = ""
	start.FreshIfProviderConversationMissing = false
	start.ContextResetNote = domain.LostConversationNote(rec.ID, artifactDir, rec.Metadata.Prompt)
	return start
}
