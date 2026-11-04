package lifecycle

import (
	"context"
	"log/slog"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ApplyTrackerFacts reacts to a fetched Tracker issue observation. It owns the
// issue-driven side of session lifecycle and the initial bot-mention nudge;
// it does NOT persist tracker rows (the future Tracker observer in #35 owns
// the read-side persistence path).
//
// Reactions today:
//   - Issue terminal (state == done or cancelled) → MarkTerminated. The
//     reducer is idempotent — repeat observations on an already-terminated
//     session are no-ops because MarkTerminated skips when IsTerminated.
//   - Assignee changed → log only. No session-state reaction yet; the policy
//     for "assignee changed away from Tallpine" is reserved for the write-side work
//     tracked by #40.
//   - New bot comment → one-time nudge using the same sendOnce + dedup
//     signature pattern as the SCM lane. Dedup is in-memory only for now;
//     cross-restart persistence lands with the Tracker observer (issue #35)
//     when issue-row signature storage is on the table.
func (m *Manager) ApplyTrackerFacts(ctx context.Context, id domain.SessionID, o ports.TrackerObservation) error {
	if !o.Fetched {
		return nil
	}
	if isTerminalTrackerState(o.Issue.State) {
		if m.sessionMutationInProgress(id) {
			return nil
		}
		return m.MarkTerminated(ctx, id)
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	if cannotNudge(rec) {
		return nil
	}
	if o.Changed.Assignee {
		slog.Default().Info("lifecycle: tracker issue assignee changed",
			"session", id, "issue", o.Issue.URL, "assignee", o.Issue.Assignee)
	}
	if o.Changed.Comments {
		bodies, ids := newBotCommentContent(o.Comments)
		if len(ids) > 0 {
			msg := "A bot left a new comment on your tracker issue. Address it and update the session."
			if joined := strings.Join(bodies, "\n\n"); strings.TrimSpace(joined) != "" {
				msg += "\n\n" + joined
			}
			// Empty prURL routes sendOnce through its in-memory-only branch:
			// the PR-row signature load/persist is skipped, so the dedup
			// survives only for the lifetime of this Manager. Cross-restart
			// persistence ships with #35.
			_, err := m.sendOnce(ctx, id, "", "tracker-bot:"+o.Issue.URL, strings.Join(ids, ","), msg, 0, nudgeRoutine, func() func() { return m.admitNudgeWrite(rec) })
			return err
		}
	}
	return nil
}

// cannotNudge is the lifecycle entry guard for automated pane delivery. The
// just-in-time sessionguard remains authoritative, but refusing exited agents
// here avoids repeated dedup/store work for a pane that now contains a shell.
//
// ApplyPRObservation does NOT call this helper directly: it needs a narrower
// carve-out so the merge-conflict nudge alone can bypass the needs-input
// condition below (see its needsInput comment), so it inlines the
// terminated/exited half of this check and evaluates needs-input separately.
// Every other nudge path in this package (ApplyTrackerFacts)
// still gates on the full condition here, unchanged.
func cannotNudge(rec domain.SessionRecord) bool {
	return rec.IsTerminated || rec.Activity.State.NeedsInput() || rec.Activity.State == domain.ActivityExited
}

func isTerminalTrackerState(state domain.NormalizedIssueState) bool {
	return state == domain.IssueDone || state == domain.IssueCancelled
}

func newBotCommentContent(comments []ports.TrackerCommentObservation) ([]string, []string) {
	bodies := make([]string, 0, len(comments))
	ids := make([]string, 0, len(comments))
	for _, c := range comments {
		if !c.IsBot {
			continue
		}
		// Both an ID and a body are required: ID anchors the dedup
		// signature (an empty ID collapses to "" which collides with
		// the zero value of m.react.seen[key] and silently suppresses
		// the nudge), and a body is what we actually need to surface
		// to the agent.
		if c.ID == "" || strings.TrimSpace(c.Body) == "" {
			continue
		}
		bodies = append(bodies, c.Body)
		ids = append(ids, c.ID)
	}
	return bodies, ids
}
