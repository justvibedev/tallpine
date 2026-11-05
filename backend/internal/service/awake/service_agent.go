package awake

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type agentRecovery interface {
	AgentRecoverySupported() bool
	InterruptedAgents() []domain.AwakeAgentTarget
	ReconnectAgent(context.Context, domain.AwakeAgentTarget) error
}

func (s *Service) capabilities() []domain.AwakeCapability {
	result := s.host.Capabilities()
	revocationPending := s.host.RecoveryReason() == "low_power_revocation_pending"
	for i := range result {
		switch result[i].ID {
		case "audio", "keyboard", "display_restore", "pause_apps", "hotspot", "agent_reconnect", "browser_actions":
			result[i].Available = false
			result[i].Reason = "This control is deferred to a follow-up release."
		}
		if revocationPending && (result[i].ID == "low_power" || result[i].ID == "low_power_authorization") {
			result[i].Available = false
			result[i].Reason = "Complete Revoke authorization again before enabling or authorizing Low Power Mode."
		}
	}
	return result
}

type agentTask struct {
	target domain.AwakeAgentTarget
	ctx    context.Context
}

func (s *Service) agentTask(ctx context.Context) *agentTask {
	recovery, ok := s.bindings.(agentRecovery)
	if !ok || !recovery.AgentRecoverySupported() || ctx.Err() != nil || s.closed || s.agentBusy || !s.doc.Settings.AgentReconnect || !s.host.Active() || s.observation.NetworkConnected == nil || !*s.observation.NetworkConnected {
		return nil
	}
	if len(s.doc.AgentAttempts) >= 1000 {
		s.doc.AgentOutcome = "history_full"
		return nil
	}
	for _, target := range recovery.InterruptedAgents() {
		if target.SessionID == "" || target.Owner.ControllerGeneration == "" || target.Owner.ProviderConversationID == "" {
			continue
		}
		seen := false
		for _, attempt := range s.doc.AgentAttempts {
			if attempt.SessionID == target.SessionID && attempt.Generation == target.Owner.ControllerGeneration {
				seen = true
				break
			}
		}
		if seen {
			continue
		}
		previous := clone(s.doc)
		s.doc.AgentAttempts = append(s.doc.AgentAttempts, domain.AwakeAgentAttempt{SessionID: target.SessionID, Generation: target.Owner.ControllerGeneration, Outcome: "prepared"})
		s.doc.AgentOutcome = "pending"
		if err := s.save(ctx); err != nil {
			s.doc = previous
			return nil
		}
		actionCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		s.agentCancel = cancel
		s.agentBusy = true
		return &agentTask{target: target, ctx: actionCtx}
	}
	return nil
}
func (s *Service) runAgent(ctx context.Context, task agentTask) {
	recovery, ok := s.bindings.(agentRecovery)
	result := errors.New("agent recovery is unavailable")
	if ok {
		result = recovery.ReconnectAgent(task.ctx, task.target)
	}
	cancelled := task.ctx.Err() != nil
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentBusy = false
	if s.agentCancel != nil {
		s.agentCancel()
		s.agentCancel = nil
	}
	outcome := "reconnected"
	if result != nil {
		outcome = "failed"
		if cancelled {
			outcome = "cancelled"
		}
	}
	for i := range s.doc.AgentAttempts {
		if s.doc.AgentAttempts[i].SessionID == task.target.SessionID && s.doc.AgentAttempts[i].Generation == task.target.Owner.ControllerGeneration {
			s.doc.AgentAttempts[i].Outcome = outcome
		}
	}
	s.doc.AgentOutcome = outcome
	if err := s.save(ctx); err != nil {
		s.doc.AgentOutcome = "uncertain"
	}
}

// Detailed observations are requested only by General settings or active
// policy. An unused feature must not spawn a native process every five seconds.
func (s *Service) needsObservations() bool {
	if s.doc.StopReason != "" {
		return s.doc.StopReason == "low_battery"
	}
	for _, source := range s.doc.Sessions {
		if source.EndedAt == nil {
			return true
		}
	}
	if !s.doc.Paused {
		for _, rule := range s.doc.Rules {
			if rule.Enabled {
				return true
			}
		}
	}
	return false
}
