package store_test

import (
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// Report routing type-asserts for these capabilities; a store that stops
// satisfying one silently turns lanes off, so fail the build instead.
var (
	_ ports.LaneReader = (*sqlite.Store)(nil)
	_ report.LaneStore = (*sqlite.Store)(nil)
)
