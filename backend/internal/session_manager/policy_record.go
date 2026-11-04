package sessionmanager

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// policyDecisionStore persists the decision record a session launched under;
// optional, like the other session side stores.
type policyDecisionStore interface {
	SetSessionPolicyDecision(ctx context.Context, id domain.SessionID, record []byte, now time.Time) error
}

// recordPolicy persists and logs the policy decision a session is launching
// under, so "why did my agent lose X" is answerable from the session and from
// the daemon log. requested and effective are the permission mode before and
// after the policy's cap; the record names rules, settings and sources, never a
// secret value. A launch with no policy wired records nothing.
func (m *Manager) recordPolicy(ctx context.Context, id domain.SessionID, decision agentpolicy.Decision, requested, effective ports.PermissionMode, extra ...map[string]map[string]any) {
	if m.policy == nil {
		return
	}
	facts := map[string]map[string]any{}
	if requested != effective {
		facts["permissionCap"] = map[string]any{"requested": string(requested), "effective": string(effective)}
	}
	for _, more := range extra {
		for rule, entries := range more {
			if facts[rule] == nil {
				facts[rule] = map[string]any{}
			}
			for k, v := range entries {
				facts[rule][k] = v
			}
		}
	}
	record := decision.WithSession(id).Record(facts)
	encoded := record.JSON()
	if store, ok := m.store.(policyDecisionStore); ok {
		if err := store.SetSessionPolicyDecision(ctx, id, encoded, m.clock()); err != nil {
			m.logger.Warn("agent policy: decision not recorded", "session", id, "error", err)
		}
	}
	level := m.logger.Debug
	if len(facts) > 0 || len(decision.Warnings()) > 0 || decision.Active() {
		level = m.logger.Info
	}
	level("agent policy decision", "session", id, "record", string(encoded))
}
