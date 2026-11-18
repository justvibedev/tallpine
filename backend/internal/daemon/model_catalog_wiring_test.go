package daemon

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

type stubModelCatalog struct{}

func (stubModelCatalog) ModelCatalog(context.Context, string, string, bool) (ports.AgentModelCatalog, error) {
	return ports.AgentModelCatalog{}, nil
}

// B811: the daemon's late-binding of the model catalog must reach the real
// session manager; when it silently misses, every spawn --model fails with
// "model catalog is unavailable" and role defaults fall back unconfirmed.
func TestWireSessionModelCatalogReachesSessionManager(t *testing.T) {
	if !wireSessionModelCatalog(&sessionmanager.Manager{}, stubModelCatalog{}) {
		t.Fatal("session manager did not accept the model catalog")
	}
	if wireSessionModelCatalog(struct{}{}, stubModelCatalog{}) {
		t.Fatal("a manager without SetModelCatalog reported the catalog as wired")
	}
	if wireSessionModelCatalog(&sessionmanager.Manager{}, nil) {
		t.Fatal("a nil catalog reported as wired")
	}
}
