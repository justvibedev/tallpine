package session

import (
	"context"
	"errors"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// toSpawnAPIError maps spawn failures to structured API errors so
// clients never land in the unclassified internal bucket when a stage sentinel
// is present. Known inner sentinels (branch state, agent binary, chat
// preflight) still win via toAPIError. Already-mapped *apierr.Error values are
// returned as-is so emitSpawnFailed can classify raw or pre-mapped errors.
func toSpawnAPIError(err error) error {
	if err == nil {
		return nil
	}
	var already *apierr.Error
	if errors.As(err, &already) {
		return already
	}
	if mapped := toAPIError(err); !errors.Is(mapped, err) {
		return mapped
	}
	// A failed Setup step already explains itself; the spawn stage prefix is noise.
	var setupErr *sessionmanager.SetupStepError
	if errors.As(err, &setupErr) {
		return apierr.Conflict("WORKSPACE_PROVISION_FAILED", setupErr.Error(), nil)
	}
	switch {
	case errors.Is(err, sessionmanager.ErrClientRequestConflict):
		return apierr.Conflict("CLIENT_REQUEST_CONFLICT", "clientRequestId belongs to a different task", nil)
	case errors.Is(err, sessionmanager.ErrClientRequestIncomplete):
		return apierr.Conflict("CLIENT_REQUEST_INCOMPLETE", "This task is still starting or its prior launch did not finish; check the session before trying again", nil)
	case errors.Is(err, context.DeadlineExceeded):
		return apierr.Conflict("SPAWN_TIMEOUT", "Session spawn timed out before the agent could start. The machine may be under heavy load: check for leftover sessions with `tallpine session ls`, then retry. To allow more time, raise TALLPINE_SPAWN_TIMEOUT (default: TALLPINE_REQUEST_TIMEOUT, 60s) or TALLPINE_TMUX_COMMAND_TIMEOUT (default 5s) in the daemon's environment and restart it.", nil)
	case errors.Is(err, context.Canceled):
		return apierr.Conflict("SPAWN_CANCELLED", "Session spawn was cancelled", nil)
	case errors.Is(err, sessionmanager.ErrSpawnPrompt):
		return apierr.Internal("SPAWN_PROMPT_FAILED", err.Error())
	case errors.Is(err, sessionmanager.ErrSpawnCreate):
		return apierr.Internal("SPAWN_CREATE_FAILED", err.Error())
	case errors.Is(err, sessionmanager.ErrSpawnSystemPrompt):
		return apierr.Internal("SPAWN_SYSTEM_PROMPT_FAILED", err.Error())
	case errors.Is(err, sessionmanager.ErrWorkspaceCreate):
		return apierr.Conflict("WORKSPACE_CREATE_FAILED", err.Error(), nil)
	case errors.Is(err, sessionmanager.ErrWorkspaceProvision):
		return apierr.Conflict("WORKSPACE_PROVISION_FAILED", err.Error(), nil)
	case errors.Is(err, sessionmanager.ErrSpawnAttachments):
		return apierr.Invalid("SPAWN_ATTACHMENTS_FAILED", err.Error(), nil)
	case errors.Is(err, sessionmanager.ErrSpawnBrowser):
		return apierr.Internal("SPAWN_BROWSER_CAPABILITY_FAILED", err.Error())
	case errors.Is(err, sessionmanager.ErrSpawnPrepare):
		return apierr.Internal("SPAWN_PREPARE_FAILED", err.Error())
	case errors.Is(err, sessionmanager.ErrSpawnPromptDelivery):
		return apierr.Internal("SPAWN_PROMPT_DELIVERY_FAILED", err.Error())
	case errors.Is(err, sessionmanager.ErrSpawnLaunchCommand):
		return apierr.Internal("SPAWN_LAUNCH_COMMAND_FAILED", err.Error())
	case errors.Is(err, sessionmanager.ErrSpawnSupervisor):
		return apierr.Internal("SPAWN_SUPERVISOR_FAILED", err.Error())
	case errors.Is(err, sessionmanager.ErrSpawnPrepareLaunch):
		return apierr.Internal("SPAWN_PREPARE_LAUNCH_FAILED", err.Error())
	case errors.Is(err, sessionmanager.ErrRuntimeCreate):
		return apierr.Internal("RUNTIME_CREATE_FAILED", err.Error())
	case errors.Is(err, sessionmanager.ErrSpawnCommit):
		return apierr.Internal("SPAWN_COMMIT_FAILED", err.Error())
	case errors.Is(err, sessionmanager.ErrSpawnDeliverPrompt):
		return apierr.Conflict("SPAWN_DELIVER_PROMPT_FAILED", err.Error(), nil)
	case errors.Is(err, sessionmanager.ErrChatController):
		return apierr.Conflict("CHAT_CONTROLLER_FAILED", err.Error(), nil)
	default:
		return apierr.Internal("SPAWN_INTERNAL", err.Error())
	}
}
