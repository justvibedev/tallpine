package session

import (
	"context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// AgentLaneRequest carries what only an agent's lane start or end sets
// (policy orchestratorLanes). The zero value is the owner's request.
type AgentLaneRequest struct {
	// StartedBy is the orchestrator that started the lane; the spawn records it
	// as the new session's parent, which is how a lane shows who started it.
	StartedBy domain.SessionID
	// Model is the lane orchestrator's model override ("" = project default).
	Model string
	// RefuseOpenPR makes EndLane refuse, under the project lock, while any
	// worker of the lane (live or terminated) owns an open PR.
	RefuseOpenPR bool
}

type agentLaneKey struct{}

// WithAgentLane attaches an agent's lane request to ctx.
func WithAgentLane(ctx context.Context, r AgentLaneRequest) context.Context {
	return context.WithValue(ctx, agentLaneKey{}, r)
}

// AgentLaneOf returns the agent lane request on ctx (zero for the owner).
func AgentLaneOf(ctx context.Context) AgentLaneRequest {
	r, _ := ctx.Value(agentLaneKey{}).(AgentLaneRequest)
	return r
}

// laneWorkersWithOpenPR lists the workers of the lane that own a PR that is
// neither merged nor closed, terminated workers included.
func (s *Service) laneWorkersWithOpenPR(ctx context.Context, ls laneEndStore, project domain.ProjectID, orch domain.SessionID, lane string) ([]domain.SessionID, error) {
	lanes, err := ls.ListSessionLanes(ctx, project)
	if err != nil {
		return nil, toAPIError(err)
	}
	var out []domain.SessionID
	for id, l := range lanes {
		if l != lane || id == orch {
			continue
		}
		rec, ok, err := s.store.GetSession(ctx, id)
		if err != nil {
			return nil, toAPIError(err)
		}
		if !ok || rec.Kind == domain.KindOrchestrator {
			continue
		}
		prs, err := s.store.ListPRsBySession(ctx, id)
		if err != nil {
			return nil, toAPIError(err)
		}
		for _, pr := range prs {
			if !pr.Merged && !pr.Closed {
				out = append(out, id)
				break
			}
		}
	}
	return out, nil
}

// checkLaneOpenPRs is EndLane's agent guard.
func (s *Service) checkLaneOpenPRs(ctx context.Context, ls laneEndStore, project domain.ProjectID, orch domain.SessionID, lane string) error {
	if !AgentLaneOf(ctx).RefuseOpenPR {
		return nil
	}
	open, err := s.laneWorkersWithOpenPR(ctx, ls, project, orch, lane)
	if err != nil || len(open) == 0 {
		return err
	}
	ids := make([]string, len(open))
	for i, id := range open {
		ids[i] = string(id)
	}
	return apierr.Conflict("LANE_HAS_OPEN_PR",
		"The lane still has worker(s) with an open PR: "+strings.Join(ids, ", ")+". Merge or close them first.", nil)
}
