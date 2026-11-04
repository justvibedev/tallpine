package sessionmanager

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionguard"
)

// waitOutcome is one poll round's verdict on whether confirmActive should
// nudge again.
type waitOutcome int

const (
	// waitTimedOut: the deadline elapsed without the session going active —
	// the previous Enter likely did not land, another may help.
	waitTimedOut waitOutcome = iota
	// waitActive: the session went active — the prompt was accepted, done.
	waitActive
	// waitBlocked: the session is paused on a user decision (a pending
	// permission/approval dialog) — an automated Enter could answer the dialog
	// on the user's behalf, so confirmation must stop and never nudge.
	waitBlocked
)

// confirmActive re-sends Enter until the session reports ActivityActive or the
// attempt budget is exhausted. The initial delivery already submitted one
// Enter; each additional attempt sends Enter again (an empty message is an
// Enter-only nudge, see ports.AgentMessenger) after waiting for Activity.State
// to flip. When the confirmation budget is exhausted without the session becoming
// active, it returns ErrDeliveryUnconfirmed. Callers must capability-gate this
// loop because harnesses without trustworthy submit and blocked signals cannot
// use it safely.
//
// Decision safety: a session observed in ActivityBlocked stops confirmation
// immediately with no nudge — an Enter into a pending permission dialog would
// answer it for the user. Sticky ActivityWaitingInput does NOT stop the loop:
// an idle-prompt session with an unsubmitted pasted draft is exactly the case
// the nudge exists for.
// deliveryEvidence is the durable proof that a delivery was accepted, honored
// by the confirmation loop when Active is never observed because the turn
// completed between activity polls. A send leaves exactly one kind of marker;
// an empty value (neither kind) means confirmation relies solely on observed
// Active/Blocked state.
type deliveryEvidence struct {
	// recordsPrompt is set for a user-authored send; its acceptance is an
	// advance of LatestUserPromptAt past baselinePromptAt (the delivery instant).
	recordsPrompt    bool
	baselinePromptAt time.Time
	// coordinationID is this delivery's ID for a coordination (tallpine send/report)
	// delivery; its acceptance is a matching coordination checkpoint scoped to
	// the current runtime launch (semanticMessageAccepted).
	coordinationID string
}

// accepted reports whether rec carries this delivery's durable acceptance.
func (e deliveryEvidence) accepted(rec domain.SessionRecord) bool {
	if e.recordsPrompt {
		return rec.Metadata.LatestUserPromptAt.After(e.baselinePromptAt)
	}
	if e.coordinationID != "" {
		return semanticMessageAccepted(rec, e.coordinationID)
	}
	return false
}

// confirmActive fences each retry's input timestamp check through its Enter
// write. Typing since inputBaseline suppresses the retry, so Tallpine cannot submit
// the operator's new draft while trying to confirm its own prompt.
func (m *Manager) confirmActive(ctx context.Context, guard *sessionguard.Guard, id domain.SessionID, evidence deliveryEvidence, inputBaseline time.Time) error {
	return m.confirmActiveWithNudge(ctx, id, evidence, nil, func(nudgeCtx context.Context) (sessionguard.Outcome, error) {
		accepted := false
		outcome, err := guard.DeliverWithComposerCheck(nudgeCtx, id, "", func(_ context.Context, rec domain.SessionRecord) (bool, func()) {
			lastInputAt, release := m.fenceTerminalInput(rec)
			if release == nil {
				lastInputAt = m.lastTerminalInputAt(rec)
			}
			accepted = evidence.accepted(rec)
			return lastInputAt.After(inputBaseline), release
		}, nil)
		if err == nil && outcome == sessionguard.SuppressedComposerBusy {
			if accepted {
				return sessionguard.SuppressedBusy, nil
			}
			m.logger.Info("send: operator typed since delivery; skipping Enter nudge", "sessionID", id)
			return sessionguard.SuppressedComposerBusy, ErrDeliveryUnconfirmed
		}
		return outcome, err
	})
}

type confirmationStopCheck func(context.Context) (bool, error)

func (m *Manager) confirmActiveWithNudge(ctx context.Context, id domain.SessionID, evidence deliveryEvidence, stop confirmationStopCheck, nudge func(context.Context) (sessionguard.Outcome, error)) error {
	for attempt := 1; ; attempt++ {
		if m.confirmationStopRequested(ctx, id, attempt, stop) {
			return nil
		}
		outcome, err := m.waitForActive(ctx, id, evidence)
		if err != nil {
			return err
		}
		if outcome == waitActive {
			return nil
		}
		if outcome == waitBlocked {
			m.logger.Info("send: session awaiting a decision; skipping Enter nudge", "sessionID", id, "attempt", attempt)
			return nil
		}
		if attempt >= m.sendConfirm.maxAttempts {
			// A submission can land between the final poll and this decision,
			// returning the session to idle with Active never observed. Honor the
			// durable LatestUserPromptAt evidence before declaring failure, so an
			// accepted message is not reported as a false ErrDeliveryUnconfirmed.
			// The evidence is specific to this delivery (prompt baseline or
			// coordination delivery ID), so it cannot be satisfied by unrelated
			// activity.
			if rec, ok, readErr := m.store.GetSession(ctx, id); readErr == nil && ok && evidence.accepted(rec) {
				return nil
			}
			m.logger.Warn("send: activity confirmation budget exhausted", "sessionID", id, "attempts", attempt)
			return ErrDeliveryUnconfirmed
		}
		// Timed out with budget remaining: the previous Enter did not land.
		// Nudge again with an Enter-only send. Deliver re-reads state
		// immediately before pasting — a permission dialog can appear in the
		// gap between waitForActive's final poll and this send, and an Enter
		// into it would answer the decision. This closes the TOCTOU the
		// per-poll check inside waitForActive cannot cover; a store failure
		// inside the guard fails closed (no Enter on an unknown state).
		if m.confirmationStopRequested(ctx, id, attempt, stop) {
			return nil
		}
		nudgeOutcome, nudgeErr := nudge(ctx)
		if nudgeErr != nil {
			m.logger.Warn("send: confirm re-send failed", "sessionID", id, "attempt", attempt, "error", nudgeErr)
			return nudgeErr
		}
		if nudgeOutcome != sessionguard.Sent {
			// Not necessarily blocked: the session may also have become active,
			// terminated, or vanished since the poll — the outcome says which.
			// The mutation-safe switch path additionally suppresses active turns.
			m.logger.Info("send: session unavailable before nudge; skipping Enter nudge", "sessionID", id, "attempt", attempt, "outcome", nudgeOutcome.String())
			return nil
		}
	}
}

func (m *Manager) confirmationStopRequested(ctx context.Context, id domain.SessionID, attempt int, stop confirmationStopCheck) bool {
	if stop == nil {
		return false
	}
	requested, err := stop(ctx)
	if err != nil {
		// A failed acknowledgement read must fail closed: an extra Enter is more
		// dangerous than allowing the outer delivery wait to surface the error.
		m.logger.Warn("send: confirmation stop check failed; skipping Enter nudge", "sessionID", id, "attempt", attempt, "error", err)
		return true
	}
	return requested
}

// waitForActive polls Activity.State for up to attemptDeadline and reports
// whether another nudge could help (see waitOutcome). Blocked is checked every
// poll so a permission dialog appearing mid-wait aborts immediately instead of
// burning the deadline. A prompt accepted and completed between polls returns
// the session to idle with Active never observed, so this delivery's durable
// acceptance marker (deliveryEvidence.accepted: a user prompt's advanced
// LatestUserPromptAt, or a coordination delivery's matching checkpoint) is
// honored as acceptance. The marker is specific to this delivery, so unrelated
// activity cannot satisfy it. A non-nil error means polling cannot continue
// (ctx cancelled, store failure, session gone).
func (m *Manager) waitForActive(ctx context.Context, id domain.SessionID, evidence deliveryEvidence) (waitOutcome, error) {
	deadlineAt := m.clock().Add(m.sendConfirm.attemptDeadline)
	ticker := time.NewTicker(m.sendConfirm.pollInterval)
	defer ticker.Stop()
	for {
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil {
			return waitTimedOut, err
		}
		if !ok {
			return waitTimedOut, fmt.Errorf("session %s not found", id)
		}
		switch rec.Activity.State {
		case domain.ActivityActive:
			return waitActive, nil
		case domain.ActivityBlocked:
			return waitBlocked, nil
		}
		if evidence.accepted(rec) {
			return waitActive, nil
		}
		if !m.clock().Before(deadlineAt) {
			return waitTimedOut, nil
		}
		// The tick select respects ctx cancellation so a request timeout
		// unblocks promptly.
		select {
		case <-ctx.Done():
			return waitTimedOut, ctx.Err()
		case <-ticker.C:
		}
	}
}
