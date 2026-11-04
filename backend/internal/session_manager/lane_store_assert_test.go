package sessionmanager

import "tallpine.eu/tallpine/backend/internal/storage/sqlite"

// recordLane and orchestratorForSession type-assert for laneStore; the real
// store must keep satisfying it or lanes silently turn off.
var _ laneStore = (*sqlite.Store)(nil)
