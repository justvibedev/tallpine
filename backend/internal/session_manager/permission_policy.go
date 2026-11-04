package sessionmanager

import (
	"context"
	"runtime"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// PolicyEngine decides the agent policy for a session.
type PolicyEngine interface {
	Decide(ctx context.Context, session agentpolicy.SessionRef) agentpolicy.Decision
}

// SetAgentPolicy completes daemon wiring before request handling begins. Nil
// leaves every session unrestricted.
func (m *Manager) SetAgentPolicy(engine PolicyEngine) { m.policy = engine }

// decide computes the agent policy decision for a session. Each flow (spawn,
// relaunch, restore, chat start and resume, interface transition) calls it once
// and hands the result to every seam it drives; no seam re-reads settings. With
// no engine wired it returns the zero Decision, which restricts nothing.
func (m *Manager) decide(ctx context.Context, project domain.ProjectID, kind domain.SessionKind, harness domain.AgentHarness, id domain.SessionID) agentpolicy.Decision {
	if m.policy == nil {
		return agentpolicy.Decision{}
	}
	return m.policy.Decide(ctx, agentpolicy.SessionRef{ID: id, Project: project, Kind: kind, Harness: harness})
}

// decideFor is decide for an existing session record.
func (m *Manager) decideFor(ctx context.Context, rec domain.SessionRecord) agentpolicy.Decision {
	return m.decide(ctx, rec.ProjectID, rec.Kind, rec.Harness, rec.ID)
}

// applySpawnAgentConfigUnder is the package function followed by the decision's
// permission cap, so spawn, chat and naming all launch with the same lowered
// mode.
func applySpawnAgentConfigUnder(decision agentpolicy.Decision, harness domain.AgentHarness, base, override ports.AgentConfig) ports.AgentConfig {
	out := applySpawnAgentConfig(base, override)
	out.Permissions = permissioncap.Of(decision).Lower(harness, out.Permissions)
	return out
}

// restoredAgentConfigUnder is restoredAgentConfig followed by the decision's
// permission cap, so a session remembered in bypass mode relaunches within the
// policy in force now.
func restoredAgentConfigUnder(decision agentpolicy.Decision, rec domain.SessionRecord, cfg domain.ProjectConfig) ports.AgentConfig {
	out := restoredAgentConfig(rec, cfg)
	out.Permissions = permissioncap.Of(decision).Lower(rec.Harness, out.Permissions)
	return out
}

// fenceGOOS is the host OS launches are admitted for; tests swap it.
var fenceGOOS = runtime.GOOS

// admitTerminalFence refuses a terminal launch the hardened profile requires to
// be fenced when harness's agent adapter cannot fence it on this OS. Every
// terminal launch path (spawn, relaunch, restore, account move, chat->terminal)
// calls it with the decision it launches under and the post-cap permission mode
// the launch uses (Codex fences only a bypass launch); chat launches are admitted by
// the chat service against the chat driver. An unknown harness has no adapter
// and is refused.
func (m *Manager) admitTerminalFence(decision agentpolicy.Decision, harness domain.AgentHarness, permissions ports.PermissionMode) error {
	out := protectstate.Of(decision)
	if !out.Required() {
		return nil
	}
	return out.Admit(ports.FencesLaunch(m.harnessAdapter(harness), fenceGOOS, permissions), string(harness), fenceGOOS)
}

// harnessAdapter is the agent adapter of harness, nil when there is none.
func (m *Manager) harnessAdapter(harness domain.AgentHarness) any {
	if m.agents == nil {
		return nil
	}
	if agent, ok := m.agents.Agent(harness); ok {
		return agent
	}
	return nil
}

// fenceGapFacts is the decision-record fact for a session Security mode wants
// fenced but whose harness cannot be on this OS ("not fenced: <reason>"); nil
// when there is no gap. permissions is the post-cap mode the launch uses. It
// never refuses a launch.
func (m *Manager) fenceGapFacts(decision agentpolicy.Decision, harness domain.AgentHarness, permissions ports.PermissionMode) map[string]map[string]any {
	gap := protectstate.Of(decision).Gap(ports.FencesLaunch(m.harnessAdapter(harness), fenceGOOS, permissions), string(harness), fenceGOOS)
	if gap == "" {
		return nil
	}
	return map[string]map[string]any{protectstate.Name: {"fenceGap": gap}}
}
