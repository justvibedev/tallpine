package session

import "tallpine.eu/tallpine/backend/internal/storage/sqlite"

// The lane-label read type-asserts for laneLabelStore; the real store must keep
// satisfying it or lane labels silently disappear.
var _ laneLabelStore = (*sqlite.Store)(nil)
