package controllers_test

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/previewserver"
)

type fakeManagedPreviewServer struct {
	status         previewserver.Status
	startEnv       map[string]string
	startErr       error
	startName      string
	startWorkspace string
	startFence     previewserver.Fence
	stopCalls      int
	onStop         func()
}

func (f *fakeManagedPreviewServer) Start(
	_ context.Context,
	sessionID domain.SessionID,
	workspacePath string,
	configurationName string,
	projectEnv map[string]string,
	fence previewserver.Fence,
) (previewserver.Status, error) {
	f.startFence = fence
	f.startName = configurationName
	f.startWorkspace = workspacePath
	f.startEnv = projectEnv
	if f.startErr != nil {
		return previewserver.Status{}, f.startErr
	}
	f.status.SessionID = sessionID
	return f.status, nil
}

func (f *fakeManagedPreviewServer) Stop(
	_ context.Context,
	sessionID domain.SessionID,
) (previewserver.Status, error) {
	f.stopCalls++
	if f.onStop != nil {
		f.onStop()
	}
	f.status.SessionID = sessionID
	f.status.State = previewserver.StateStopped
	return f.status, nil
}

func (f *fakeManagedPreviewServer) Status(sessionID domain.SessionID) previewserver.Status {
	status := f.status
	status.SessionID = sessionID
	if status.State == "" {
		status.State = previewserver.StateStopped
	}
	if status.Logs == nil {
		status.Logs = []string{}
	}
	return status
}
