package chat

import (
	"context"
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ErrAsyncTaskStopUnsupported reports a live conversation whose harness cannot
// stop background tasks.
var ErrAsyncTaskStopUnsupported = errors.New("this agent cannot stop background tasks")

// StopAsyncTask stops one background task of a session's live harness. False
// means the harness declined (the task already ended).
func (s *Service) StopAsyncTask(ctx context.Context, sessionID domain.SessionID, taskID string) (bool, error) {
	controller, err := s.Controller(sessionID)
	if err != nil {
		return false, err
	}
	stopper, ok := controller.conv.(ports.ChatAsyncTaskStopper)
	if !ok {
		return false, ErrAsyncTaskStopUnsupported
	}
	return stopper.StopAsyncTask(ctx, taskID)
}
