package domain

// SessionLane is a session's lane binding. A lane is a backlog parent item an
// orchestrator chat owns; workers inherit the lane of the session that spawned
// them. A session without a binding is un-laned.
type SessionLane struct {
	// LaneItemID is the backlog item id of the lane; empty means un-laned.
	LaneItemID string
	// ParentSessionID is the session that spawned this one, when known.
	ParentSessionID SessionID
}

// PickOrchestrator chooses the live orchestrator that should receive work of
// the given lane ("" = un-laned). Preference: the newest orchestrator bound to
// that lane; else the newest un-laned one (the project's default
// orchestrator). Laned work never falls through to another lane's
// orchestrator: with neither, it is held. Un-laned work (lane "") with no
// default orchestrator goes to the newest of any lane, so it never parks while
// some orchestrator is running. lanes maps session ids to their lane item id.
func PickOrchestrator(recs []SessionRecord, lanes map[SessionID]string, lane string) (SessionRecord, bool) {
	var exact, unlaned, newest SessionRecord
	for _, rec := range recs {
		if rec.Kind != KindOrchestrator || rec.IsTerminated || rec.Activity.State == ActivityExited {
			continue
		}
		newest = newerRecord(newest, rec)
		switch lanes[rec.ID] {
		case lane:
			exact = newerRecord(exact, rec)
		case "":
			unlaned = newerRecord(unlaned, rec)
		}
	}
	picks := []SessionRecord{exact, unlaned}
	if lane == "" {
		picks = append(picks, newest)
	}
	for _, pick := range picks {
		if pick.ID != "" {
			return pick, true
		}
	}
	return SessionRecord{}, false
}

func newerRecord(cur, rec SessionRecord) SessionRecord {
	if cur.ID == "" || rec.CreatedAt.After(cur.CreatedAt) {
		return rec
	}
	return cur
}
