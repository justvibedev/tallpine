package chat

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// AutoCompactView is what a running chat's compaction resolves to: its launch
// baseline, the window it compacts at now (research mode included) and the
// model's window. ok is false without a running controller or for an agent
// Tallpine sets no window on. It never waits for the controller gate.
func (s *Service) AutoCompactView(ctx context.Context, id domain.SessionID) (domain.AutoCompactView, bool) {
	controller, release, err := s.tryReadingController(ctx, id)
	if err != nil {
		return domain.AutoCompactView{}, false
	}
	defer release()
	if contextwindow.AutoCompactEnvKey(controller.harness) == "" {
		return domain.AutoCompactView{}, false
	}
	modelWindow, _ := s.modelCapacity(ctx, controller.harness, controller.conversation.ProjectID, controller.Settings().Model)
	window := s.desiredCompactTokens(ctx, controller)
	return domain.AutoCompactView{
		Baseline:    controller.startBaseline,
		Window:      window,
		Trigger:     domain.AutoCompactTrigger(controller.harness, window),
		ModelWindow: modelWindow,
		RoomTight:   domain.AutoCompactRoomTight(modelWindow, controller.startBaseline),
	}, true
}
