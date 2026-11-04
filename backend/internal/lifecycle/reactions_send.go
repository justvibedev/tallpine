package lifecycle

import (
	"context"
	"encoding/json"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionguard"
)

// sendOnceOutcome tells a caller whether a nudge is accounted for (actually
// sent, or already covered by dedup state) versus suppressed by the just-in-time
// session guard. It matters for review delivery: a suppressed nudge must NOT be
// stamped delivered, or the feedback is lost when the session later unblocks.
type sendOnceOutcome int

// nudgePolicy selects the guard policy sendOnce writes under.
type nudgePolicy int

func policyFor(urgent bool) nudgePolicy {
	if urgent {
		return nudgeUrgent
	}
	return nudgeRoutine
}

// admit, when set, runs only once the write is due (past dedup and the attempt
// budget) and returns an undo for a write the guard then refuses. It must take
// no lock but the leaf deferMu: react.mu is held here.
func (m *Manager) sendOnce(ctx context.Context, id domain.SessionID, prURL, key, sig, msg string, maxAttempts int, policy nudgePolicy, admit func() (undo func())) (sendOnceOutcome, error) {
	if m.guard == nil {
		return sendOnceAccounted, nil
	}
	m.react.mu.Lock()
	defer m.react.mu.Unlock()

	if prURL != "" && !m.react.loaded[prURL] {
		if err := m.loadPRSignaturesLocked(ctx, prURL); err != nil {
			return sendOnceAccounted, err
		}
		m.react.loaded[prURL] = true
	}

	if m.react.seen[key] == sig {
		return sendOnceAccounted, nil
	}
	attempts := m.react.attempts[key]
	if maxAttempts > 0 && attempts >= maxAttempts {
		return sendOnceAccounted, nil
	}
	// The guard re-reads the session immediately before pasting: the caller's
	// NeedsInput() entry check ran before this function's dedup/persist I/O, so
	// a permission hook could have stored blocked (or the session could have
	// exited/terminated) in the meantime. A suppressed write returns SUPPRESSED (not
	// accounted), so a review caller won't stamp it delivered and it re-fires
	// once the session is workable again. A store failure inside the guard also
	// suppresses (fail closed, nothing was written); a messenger failure means
	// the write was attempted and stays accounted, matching the pre-guard
	// behavior.
	nudge := m.guard.Nudge
	switch policy {
	case nudgeIdleOnly:
		nudge = func(ctx context.Context, id domain.SessionID, msg string) (sessionguard.Outcome, error) {
			return m.guard.NudgeCoordination(ctx, id, msg, nil)
		}
	case nudgeUrgent:
		// Merge-conflict nudges (the only urgent nudge today) must still reach
		// a session idle at a needs-input prompt; NudgeUrgent keeps refusing
		// while a live permission dialog is on screen — including the
		// waiting_input prompts that harnesses without a blocked signal use to
		// represent a masked permission decision (see the urgentNudgeGate
		// predicate). See ApplyPRObservation's needsInput carve-out.
		nudge = func(ctx context.Context, id domain.SessionID, msg string) (sessionguard.Outcome, error) {
			return m.guard.NudgeUrgent(ctx, id, msg, m.urgentNudgeWaitingInputSafe)
		}
	}
	undo := func() {}
	if admit != nil {
		undo = admit()
	}
	outcome, err := nudge(ctx, id, msg)
	if outcome != sessionguard.Sent {
		undo()
	}
	if err != nil {
		if outcome != sessionguard.Sent {
			return sendOnceSuppressed, err
		}
		return sendOnceSent, err
	}
	if outcome == sessionguard.SuppressedBusy {
		return sendOnceBusy, nil
	}
	if outcome != sessionguard.Sent {
		return sendOnceSuppressed, nil
	}
	// Order: Send → in-memory mutation → durable persist. Sending first means a
	// transient persist failure does NOT swallow a real send (the agent saw the
	// message; subsequent polls in this process suppress re-sends via the
	// in-memory dedup). A persist failure that survives until a daemon restart
	// degrades to one extra nudge — preferred over the inverse (persist before
	// send, then crash mid-call) which would silently lose a real nudge.
	m.react.seen[key] = sig
	m.react.attempts[key] = attempts + 1
	if prURL != "" {
		if err := m.persistPRSignaturesLocked(ctx, prURL); err != nil {
			return sendOnceSent, err
		}
	}
	return sendOnceSent, nil
}

// loadPRSignaturesLocked merges any previously persisted reaction-dedup state
// for prURL into the in-memory maps. Caller must hold m.react.mu.
func (m *Manager) loadPRSignaturesLocked(ctx context.Context, prURL string) error {
	raw, err := m.store.GetPRLastNudgeSignature(ctx, prURL)
	if err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	// A corrupt persisted payload must not crash the lifecycle write path;
	// the worst case from a swallow is re-firing a nudge once.
	var p reactionPayload
	_ = json.Unmarshal([]byte(raw), &p)
	for k, v := range p.Seen {
		if _, ok := m.react.seen[k]; !ok {
			m.react.seen[k] = v
		}
	}
	for k, v := range p.Attempts {
		if cur, ok := m.react.attempts[k]; !ok || v > cur {
			m.react.attempts[k] = v
		}
	}
	for k, v := range p.Deferred {
		if _, ok := m.react.deferred[k]; !ok && m.react.seen[k] != v {
			m.react.deferred[k] = v
		}
	}
	return nil
}

// persistPRSignaturesLocked serialises every reaction-dedup entry whose key
// references prURL and writes the JSON payload back via the store. Caller must
// hold m.react.mu. A failed persist surfaces upward so the in-memory mutation
// (which the messenger already acted on) is not silently divergent from disk.
func (m *Manager) persistPRSignaturesLocked(ctx context.Context, prURL string) error {
	payload := reactionPayload{Seen: map[string]string{}, Attempts: map[string]int{}}
	for k, v := range m.react.seen {
		if reactionKeyTargetsPR(k, prURL) {
			payload.Seen[k] = v
		}
	}
	for k, v := range m.react.attempts {
		if reactionKeyTargetsPR(k, prURL) {
			payload.Attempts[k] = v
		}
	}
	for k, v := range m.react.deferred {
		if !reactionKeyTargetsPR(k, prURL) {
			continue
		}
		// A deferral ends once that comment is recorded as delivered.
		if m.react.seen[k] == v {
			delete(m.react.deferred, k)
			continue
		}
		if payload.Deferred == nil {
			payload.Deferred = map[string]string{}
		}
		payload.Deferred[k] = v
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return m.store.UpdatePRLastNudgeSignature(ctx, prURL, string(raw))
}

// reactionKeyTargetsPR matches the "<type>:<url>[:<extra>]" reaction keys used
// by ApplyPRObservation. Anchoring on the second colon-delimited segment keeps
// PR-specific keys grouped with the row that survives a restart.
func reactionKeyTargetsPR(key, prURL string) bool {
	if prURL == "" {
		return false
	}
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return false
	}
	rest := parts[1]
	return rest == prURL || strings.HasPrefix(rest, prURL+":")
}
