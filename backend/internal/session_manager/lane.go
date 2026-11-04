package sessionmanager

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// laneStore is the optional store capability behind orchestrator lanes. A store
// without it leaves every session un-laned.
type laneStore interface {
	ports.LaneReader
	SetSessionLane(context.Context, domain.SessionID, domain.SessionLane) error
}

// recordLane persists the new session's lane and spawning parent. An
// orchestrator takes the lane it was created for. A worker inherits the lane
// of the session that spawned it, else the lane named on the request (the
// backlog item's parent). Nothing is written for an un-laned, parentless spawn.
func (m *Manager) recordLane(ctx context.Context, id domain.SessionID, project domain.ProjectID, cfg ports.SpawnConfig) error {
	ls, ok := m.store.(laneStore)
	if !ok {
		return nil
	}
	sl, err := m.resolveLane(ctx, project, cfg)
	if err != nil || sl == (domain.SessionLane{}) {
		return err
	}
	return ls.SetSessionLane(ctx, id, sl)
}

// resolveLane computes the lane binding a spawn will get, without writing it.
func (m *Manager) resolveLane(ctx context.Context, project domain.ProjectID, cfg ports.SpawnConfig) (domain.SessionLane, error) {
	ls, ok := m.store.(laneStore)
	if !ok {
		return domain.SessionLane{}, nil
	}
	lane := cfg.LaneItemID
	parent := cfg.ParentSessionID
	if cfg.Kind != domain.KindOrchestrator && parent != "" {
		rec, found, err := m.store.GetSession(ctx, parent)
		if err != nil {
			return domain.SessionLane{}, fmt.Errorf("load parent session %s: %w", parent, err)
		}
		if !found || rec.ProjectID != project {
			// TALLPINE_SESSION_ID may be stale or from another project.
			parent = ""
		} else {
			pl, err := ls.SessionLane(ctx, parent)
			if err != nil {
				return domain.SessionLane{}, err
			}
			if pl.LaneItemID != "" {
				lane = pl.LaneItemID
			}
		}
	}
	return domain.SessionLane{LaneItemID: lane, ParentSessionID: parent}, nil
}

// laneOf returns the session's lane item id, "" when un-laned.
func (m *Manager) laneOf(ctx context.Context, id domain.SessionID) (string, error) {
	ls, ok := m.store.(laneStore)
	if !ok {
		return "", nil
	}
	sl, err := ls.SessionLane(ctx, id)
	return sl.LaneItemID, err
}

// laneSlug is the git/path-safe form of a lane orchestrator's lane item id,
// "" for any other session. It gives a lane orchestrator its own branch and
// worktree so it never shares the default orchestrator's workspace.
func laneSlug(kind domain.SessionKind, laneItemID string) string {
	if kind != domain.KindOrchestrator || laneItemID == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToLower(laneItemID) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// laneWorkspacePrefix names the managed orchestrator worktree directory.
func laneWorkspacePrefix(project domain.ProjectRecord, cfg ports.SpawnConfig) string {
	prefix := sessionPrefix(project)
	if slug := laneSlug(cfg.Kind, cfg.LaneItemID); slug != "" {
		return prefix + "-" + slug
	}
	return prefix
}

// LaneBranch appends a lane orchestrator's lane slug to its default branch.
// Any other session, or an empty branch, is returned unchanged. Every place
// that computes an orchestrator branch goes through here so they cannot drift.
func LaneBranch(branch string, kind domain.SessionKind, laneItemID string) string {
	if slug := laneSlug(kind, laneItemID); slug != "" && branch != "" {
		return branch + "-" + slug
	}
	return branch
}
