package sessionmanager

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// AutoCompactView is what a session's compaction resolves to by the path a
// relaunch takes (launchOptionsWithWindow): its window choice or default, the
// model window and the baseline measured now. A running chat reports its own
// launch values instead (chat.Service.AutoCompactView); for a chat that is not
// running this is what its next launch resolves unless it has a window of its
// own. ok is false for an agent Tallpine sets no window on, a standalone
// session or an unreadable project.
func (m *Manager) AutoCompactView(ctx context.Context, rec domain.SessionRecord) (domain.AutoCompactView, bool) {
	if contextwindow.AutoCompactEnvKey(rec.Harness) == "" || rec.ProjectID == "" {
		return domain.AutoCompactView{}, false
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return domain.AutoCompactView{}, false
	}
	options := m.launchOptionsWithWindow(ctx, rec, project.Config, rec.Metadata.Model)
	return domain.NewAutoCompactView(rec.Harness, options), true
}
