package chat

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// compactOutcome is what moving a running agent to its chosen window came to.
type compactOutcome int

const (
	compactInSync    compactOutcome = iota // already at the window, or no live window to move
	compactApplied                         // the agent moved
	compactKept                            // the agent answered that it keeps its window
	compactNoSetter                        // the agent cannot change it live
	compactAskFailed                       // the request failed; the agent never answered
)

// tryLiveCompaction asks a running agent to move to its chosen window and
// reports the outcome without logging it: callers word it, because only they
// know whether the desired window is still wanted.
func (s *Service) tryLiveCompaction(ctx context.Context, c *Controller) (out compactOutcome, live, desired int, err error) {
	if contextwindow.AutoCompactEnvKey(c.harness) == "" {
		return compactInSync, 0, 0, nil
	}
	desired = s.desiredCompactTokens(ctx, c)
	c.mu.Lock()
	live = c.compactLive
	c.mu.Unlock()
	if live == desired {
		return compactInSync, live, desired, nil
	}
	setter, ok := c.conv.(ports.ChatCompactionWindowSetter)
	if !ok {
		return compactNoSetter, live, desired, nil
	}
	applied, err := setter.SetAutoCompactWindow(ctx, desired)
	if err != nil {
		return compactAskFailed, live, desired, err
	}
	c.mu.Lock()
	c.research.windowFixed = !applied
	if applied {
		c.compactLive = desired
	}
	c.mu.Unlock()
	if !applied {
		return compactKept, live, desired, nil
	}
	return compactApplied, live, desired, nil
}

// reconcileCompaction moves a running agent to its chosen window where the ACP
// bridge allows it, and reports whether the choice is still waiting for a
// restart. A bridge that cannot, or any failure, never fails the chat: the
// value is already saved and the next start uses it.
func (s *Service) reconcileCompaction(ctx context.Context, c *Controller) (pending bool) {
	out, live, desired, err := s.tryLiveCompaction(ctx, c)
	switch out {
	case compactNoSetter:
		s.log.Info("compaction window waits for a restart: agent cannot change it live", "session", c.sessionID, "live", live, "desired", desired)
	case compactAskFailed:
		s.log.Warn("could not change the live compaction window; it applies after a restart", "session", c.sessionID, "live", live, "desired", desired, "error", err)
	case compactKept:
		s.log.Info("compaction window waits for a restart: agent kept its window", "session", c.sessionID, "live", live, "desired", desired)
	case compactApplied:
		s.log.Info("compaction window applied live", "session", c.sessionID, "previous", live, "tokens", desired)
	}
	return out == compactNoSetter || out == compactAskFailed || out == compactKept
}
