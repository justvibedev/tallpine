package daemon

import (
	"tallpine.eu/tallpine/backend/internal/contextwindow"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	agentsvc "tallpine.eu/tallpine/backend/internal/service/agent"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

// The services the daemon passes to startSession as its defaults and agent
// readiness also give reviewers their compaction window; these fail the build
// if either stops doing so.
var (
	_ reviewcore.CompactDefaults  = (*settingssvc.Service)(nil)
	_ contextwindow.CatalogReader = (*agentsvc.Service)(nil)
)

// compactDefaultsOf is the Settings compaction default reader behind a
// startSession dependency, nil when it has none.
func compactDefaultsOf(v any) reviewcore.CompactDefaults {
	d, _ := v.(reviewcore.CompactDefaults)
	return d
}

// modelCatalogOf is the saved model catalog behind a startSession dependency,
// nil when it has none.
func modelCatalogOf(v any) contextwindow.CatalogReader {
	c, _ := v.(contextwindow.CatalogReader)
	return c
}
