package session

import "tallpine.eu/tallpine/backend/internal/domain"

// markCurrentOrchestrators sets CurrentOrchestrator on one session per
// (project, lane): the oldest live orchestrator, else the newest ended one.
// A lane exists only while its orchestrator is live, so an ended laned
// orchestrator is never current. Several live orchestrators can share a
// project's default lane; the oldest is the main one and the rest stay unmarked.
// This is the one place the rule lives; clients read the field.
func markCurrentOrchestrators(sessions []domain.Session) {
	type key struct {
		project domain.ProjectID
		lane    string
	}
	best := map[key]int{}
	for i, s := range sessions {
		if s.Kind != domain.KindOrchestrator || s.ProjectID == "" {
			continue
		}
		if s.Lane != "" && s.IsTerminated {
			continue
		}
		k := key{s.ProjectID, s.Lane}
		cur, ok := best[k]
		switch {
		case !ok:
			best[k] = i
		case sessions[cur].IsTerminated && !s.IsTerminated:
			best[k] = i
		case sessions[cur].IsTerminated && s.IsTerminated && !s.CreatedAt.Before(sessions[cur].CreatedAt):
			best[k] = i
		case !sessions[cur].IsTerminated && !s.IsTerminated && s.CreatedAt.Before(sessions[cur].CreatedAt):
			best[k] = i
		}
	}
	for _, i := range best {
		sessions[i].CurrentOrchestrator = true
	}
}
