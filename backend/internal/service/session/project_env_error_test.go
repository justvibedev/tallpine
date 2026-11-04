package session

import (
	"errors"
	"fmt"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A launch of a project whose encrypted env cannot be opened (the store's
// GetProject fails, wrapped by the session manager's loadProject) reaches the
// API as 409 PROJECT_ENV_LOCKED on spawn and restore.
func TestProjectEnvLockedMapsToConflict(t *testing.T) {
	wrapped := fmt.Errorf("spawn: load project: project p1: %w: key", ports.ErrProjectEnvLocked)
	for name, mapped := range map[string]error{"restore": toAPIError(wrapped), "spawn": toSpawnAPIError(wrapped)} {
		var e *apierr.Error
		if !errors.As(mapped, &e) || e.Kind != apierr.KindConflict || e.Code != ports.CodeProjectEnvLocked {
			t.Fatalf("%s: mapped = %#v", name, mapped)
		}
	}
}
