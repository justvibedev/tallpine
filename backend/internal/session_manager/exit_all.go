package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// exitAllAgentsParallel bounds how many agents ExitAllAgents stops at once, so
// stopping a dozen sessions on a hot machine does not fork a dozen teardowns.
const exitAllAgentsParallel = 4

// agentRunning reports whether rec's agent has processes that may be running:
// the session is live, its agent has not exited, and an idle Chat agent has not
// been put to sleep.
func agentRunning(rec domain.SessionRecord) bool {
	return !rec.IsTerminated && rec.Activity.State != domain.ActivityExited && rec.HibernatedAt == nil
}

// RunningAgents lists the sessions whose agent is still running. The desktop
// asks before quitting, because these agents and the processes they started
// (test runs, builds, dev servers) can outlive the app.
func (m *Manager) RunningAgents(ctx context.Context) ([]domain.SessionID, error) {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list running agents: %w", err)
	}
	var ids []domain.SessionID
	for _, rec := range recs {
		if agentRunning(rec) {
			ids = append(ids, rec.ID)
		}
	}
	return ids, nil
}

// ExitAllAgents stops every running agent's process tree, as ExitAgent does for
// one: each session, its worktree and its conversation stay, and the agent stays
// stopped until it is resumed. It returns the sessions it stopped and one
// error per session it could not stop; the others stop regardless. err is set
// only when the running agents could not be listed.
func (m *Manager) ExitAllAgents(ctx context.Context) (stopped []domain.SessionID, failed []error, err error) {
	ids, err := m.RunningAgents(ctx)
	if err != nil {
		return nil, nil, err
	}
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	parallel := m.exitAllParallel
	if parallel <= 0 {
		parallel = exitAllAgentsParallel
	}
	slots := make(chan struct{}, parallel)
	for _, id := range ids {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			_, err := m.ExitAgent(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				stopped = append(stopped, id)
			case errors.Is(err, ErrAgentExited), errors.Is(err, ErrTerminated):
				// It stopped on its own between the listing and the exit.
			default:
				failed = append(failed, err)
			}
		})
	}
	wg.Wait()
	return stopped, failed, nil
}
