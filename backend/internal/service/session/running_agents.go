package session

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// runningAgentsCommander keeps the stop-everything lifecycle optional for
// focused service fakes while production delegates to Session Manager.
type runningAgentsCommander interface {
	RunningAgents(context.Context) ([]domain.SessionID, error)
	ExitAllAgents(context.Context) ([]domain.SessionID, []error, error)
}

var _ runningAgentsCommander = (*sessionmanager.Manager)(nil)

// StopAllAgentsOutcome reports which agents a stop-everything request ended
// and why the others did not stop.
type StopAllAgentsOutcome struct {
	Stopped []domain.SessionID
	// Failures is one message per agent that is still running.
	Failures []string
}

func (s *Service) runningAgentsCommander() (runningAgentsCommander, error) {
	manager, ok := s.manager.(runningAgentsCommander)
	if !ok {
		return nil, apierr.Conflict("AGENT_EXIT_UNSUPPORTED", "This build cannot stop agents independently", nil)
	}
	return manager, nil
}

// RunningAgents lists the sessions whose agent is still running.
func (s *Service) RunningAgents(ctx context.Context) ([]domain.SessionID, error) {
	manager, err := s.runningAgentsCommander()
	if err != nil {
		return nil, err
	}
	ids, err := manager.RunningAgents(ctx)
	if err != nil {
		return nil, toAPIError(err)
	}
	return ids, nil
}

// StopAllAgents stops every running agent's process tree. Each session stays
// and resumes when its agent is resumed.
func (s *Service) StopAllAgents(ctx context.Context) (StopAllAgentsOutcome, error) {
	manager, err := s.runningAgentsCommander()
	if err != nil {
		return StopAllAgentsOutcome{}, err
	}
	stopped, failed, err := manager.ExitAllAgents(ctx)
	if err != nil {
		return StopAllAgentsOutcome{}, toAPIError(err)
	}
	out := StopAllAgentsOutcome{Stopped: stopped}
	for _, one := range failed {
		out.Failures = append(out.Failures, one.Error())
	}
	return out, nil
}
