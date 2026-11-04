package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/orchestratorlanes"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

const laneOffReason = "starting or ending a lane orchestrator is off for this project (agent policy orchestratorLanes, Settings > Agent policy)"

// laneStoreForScope is the optional lane lookup of the scope store.
type laneStoreForScope interface {
	SessionLane(ctx context.Context, id domain.SessionID) (domain.SessionLane, error)
}

// orchestratorLanesEnabled reads the orchestratorLanes policy of a project from
// the live engine on every request. No engine means off.
func orchestratorLanesEnabled(decider controllers.PolicyDecider) func(context.Context, domain.ProjectID) bool {
	if decider == nil {
		return nil
	}
	return func(ctx context.Context, project domain.ProjectID) bool {
		return orchestratorlanes.Of(decider.Decide(ctx, agentpolicy.SessionRef{Project: project})).Enabled()
	}
}

// isLaneOrchestrator reports whether the agent is itself a lane orchestrator.
// Unknown counts as a lane: lane orchestrators may never start lanes.
func (s *agentScopes) isLaneOrchestrator(ctx context.Context, id domain.SessionID) bool {
	ls, ok := s.store.(laneStoreForScope)
	if !ok {
		return true
	}
	l, err := ls.SessionLane(ctx, id)
	return err != nil || l.LaneItemID != ""
}

// laneGrant is the shared check for starting and ending a lane: the agent is
// the project's main orchestrator, the policy is on and the target is its own
// project.
func (s *agentScopes) laneGrant(ctx context.Context, agent requestscope.Agent, project domain.ProjectID) string {
	if agent.Role != requestscope.RoleOrchestrator || s.lanesEnabled == nil || !s.lanesEnabled(ctx, agent.ProjectID) {
		return laneOffReason
	}
	if project != agent.ProjectID {
		return "a lane can be started or ended only in the agent's own project"
	}
	if s.isLaneOrchestrator(ctx, agent.SessionID) {
		return "a lane orchestrator cannot start or end lanes; only the project's main orchestrator can"
	}
	return ""
}

// checkStartLane authorizes POST /orchestrators with a lane: a brief is
// required, clean replacement and approval overrides stay with the user.
func (s *agentScopes) checkStartLane(r *http.Request, agent requestscope.Agent, raw []byte) (reason, cross string) {
	var body struct {
		ProjectID    domain.ProjectID `json:"projectId"`
		Clean        bool             `json:"clean"`
		Prompt       string           `json:"prompt"`
		ApprovalMode string           `json:"approvalMode"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.ProjectID == "" {
		return "its body does not name a project", ""
	}
	if why := s.laneGrant(r.Context(), agent, body.ProjectID); why != "" {
		return why, ""
	}
	if body.Clean || body.ApprovalMode != "" {
		return "agents may start a lane without clean or approvalMode", ""
	}
	if strings.TrimSpace(body.Prompt) == "" {
		return "starting a lane needs a first-message brief (prompt)", ""
	}
	return "", ""
}

// checkEndLane authorizes POST /orchestrators/end for the main orchestrator
// of the project that owns the lane.
func (s *agentScopes) checkEndLane(r *http.Request, agent requestscope.Agent) (reason, cross string) {
	raw, ok := peekBody(r)
	if !ok {
		return "its body is unreadable or too large", ""
	}
	var body struct {
		SessionID domain.SessionID `json:"sessionId"`
		ProjectID domain.ProjectID `json:"projectId"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "its body is not valid JSON", ""
	}
	project := body.ProjectID
	if body.SessionID != "" {
		rec, found, err := s.store.GetSession(r.Context(), body.SessionID)
		if err != nil || !found {
			return "it names an unknown session", ""
		}
		project = rec.ProjectID
	}
	if project == "" {
		return "its body does not name a lane", ""
	}
	return s.laneGrant(r.Context(), agent, project), ""
}
