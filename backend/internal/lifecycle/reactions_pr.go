package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ApplyPRObservation reacts to a fetched PR observation after the PR service has
// persisted it. It does not write PR rows; it owns PR-driven lifecycle effects
// and sends actionable agent nudges such as rebase, fix-CI, and
// address-review-feedback prompts.
func (m *Manager) ApplyPRObservation(ctx context.Context, id domain.SessionID, o ports.PRObservation) error {
	if !o.Fetched {
		return nil
	}
	// A PR observation is this session's earliest opportunity to durably record
	// "this session produced a PR" — reconcile immediately rather than waiting
	// for the artifact-output poller's next tick. A failure here must not
	// swallow the nudge delivery below, so it is logged, not returned.
	if err := m.ReconcileSessionOutputType(ctx, id); err != nil {
		slog.Default().Warn("lifecycle: reconcile session output type", "session", id, "err", err)
	}
	// A PR reaching a terminal state (merged or closed) no longer ends the
	// session on its own: a session may own several PRs. Terminate only when no
	// open PR remains and at least one of them merged. The observer persists the
	// PR row before calling lifecycle, so the store already reflects this
	// transition when sessionComplete reads it.
	if o.Merged {
		m.announceMerge(ctx, id, o.URL)
	}
	if o.Merged || o.Closed {
		return m.applyPRTerminalState(ctx, id)
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	// Re-arm the merge-conflict dedup on a definitively-cleared observation
	// BEFORE the dead-session delivery gate below. Re-arming is non-delivery
	// bookkeeping — it only drops this PR's dedup entry in the store, it writes
	// nothing to a pane — so it must run even for a session that has exited or is
	// terminated. Otherwise conflicting -> agent exits -> mergeable -> agent
	// restored -> conflicting keeps the durable "conflicting" signature and
	// sendOnce swallows the recurrence: exited sessions are still polled, and a
	// terminated session that is later restored resumes polling, so the cleared
	// state must land regardless of current liveness. The error is deferred past
	// the nudge send loop (returned as rearmErr) so a persist failure here cannot
	// discard CI/review nudges an unstable PR still queues below.
	var rearmErr error
	if mergeabilityClearsConflict(o) {
		rearmErr = m.rearmMergeConflict(ctx, o.URL)
	}
	// A genuinely dead session — terminated, or its pane already exited to a
	// shell — has nowhere to deliver any nudge, merge-conflict included, so
	// return once the non-delivery re-arm above has run.
	if rec.IsTerminated || rec.Activity.State == domain.ActivityExited {
		return rearmErr
	}
	// cannotNudge's remaining condition, needs-input, defers CI-failure and
	// review-feedback nudges until the agent is active again: both are
	// actionable only by the agent, so re-surfacing them once it resumes loses
	// nothing. A merge conflict is different — the human parked at the
	// needs-input prompt may be exactly who needs to act (rebase it themselves,
	// or redirect the agent), so the merge-conflict nudge below is deliberately
	// exempted from this gate instead of loosening it for every nudge type. It
	// still funnels through sendOnce's urgent path (sessionguard.NudgeUrgent),
	// which refuses while the session is blocked on a live permission dialog and
	// while it sits at a waiting_input prompt on a harness that cannot prove that
	// prompt is a genuine idle composer rather than a masked permission decision
	// (the urgentNudgeWaitingInputSafe gate) — so only a provably-idle needs-input
	// prompt is pasted into unsolicited.
	needsInput := rec.Activity.State.NeedsInput()
	// A single PR can trip several actionable conditions at once (failing CI,
	// unresolved review comments, a merge conflict). Queue every applicable nudge
	// and send them together, so each surfaces independently instead of one
	// returning early and hiding the rest — the bug this reducer had, where a CI
	// failure suppressed review feedback on the same PR. Each nudge self-dedups
	// via sendOnce; a send error short-circuits, and nudges already sent have
	// persisted their own dedup signature so the next poll retries only the rest.
	ident := prIdentity(o)
	var nudges []pendingNudge
	var ciPolicyErr error
	if !needsInput {
		var err error
		nudges, ciPolicyErr, err = m.queueAgentNudges(ctx, &o, ident)
		if err != nil {
			return err
		}
	}

	// Only the merge-conflict nudge needs a store read (the parent-stack check).
	// A read error there must NOT discard the CI/review nudges already queued
	// above — returning early here would re-introduce the "one condition
	// suppresses the others" coupling this queue was built to remove. So on error
	// we skip only the merge-conflict nudge (it self-heals on the next poll) and
	// defer the error past the send loop, still surfacing it so the observer logs
	// and re-polls.
	nudges, blockedCheckErr := m.queueMergeConflictNudge(ctx, id, o, ident, nudges)
	// The definitively-cleared re-arm (#4528) runs above the dead-session gate,
	// not here, so an exited/restored session still drops its stale "conflicting"
	// signature; rearmErr is surfaced at the end of this function alongside the
	// other deferred read errors.

	// Review comments are not handed to a worker mid-turn: they wait for the
	// turn to end and are re-checked against the provider then. A nudge this
	// loop sends starts a turn, so every comment after it waits for that turn.
	busy := rec.Activity.State == domain.ActivityActive
	fresh := newThreadSnapshot(m, o.URL)
	deferredAny, sentAny := false, false
	for _, n := range nudges {
		if n.comment != nil {
			deferred, sent, err := m.deliverReviewComment(ctx, rec, busy || sentAny, o.URL, n, fresh)
			if err != nil {
				return err
			}
			deferredAny = deferredAny || deferred
			sentAny = sentAny || sent
			continue
		}
		// Never deferred, but review comments must wait for the turn it starts.
		outcome, err := m.sendOnce(ctx, id, o.URL, n.key, n.sig, n.msg, n.maxAttempts, policyFor(n.urgent), func() func() { return m.admitNudgeWrite(rec) })
		if err != nil {
			return err
		}
		sentAny = sentAny || outcome == sendOnceSent
	}
	// The turn may have ended after the session was read above, in which case
	// its turn-end delivery already ran without these deferrals. A turn this
	// loop started delivers them when it ends instead.
	if deferredAny && !sentAny {
		if err := m.DeliverDeferredReviewComments(ctx, id); err != nil {
			return err
		}
	}
	// Surface deferred policy/parent-stack read errors only after independent
	// nudges have been sent, so one failed lookup cannot hide another reaction.
	if ciPolicyErr != nil {
		return ciPolicyErr
	}
	if blockedCheckErr != nil {
		return blockedCheckErr
	}
	return rearmErr
}

// applyPRTerminalState handles a merged or closed PR observation.
func (m *Manager) applyPRTerminalState(ctx context.Context, id domain.SessionID) error {
	return m.maybeAutoClose(ctx, id)
}

// queueAgentNudges queues the CI-failure and review nudges for an active agent.
// A CI-policy lookup failure is returned separately so it can be deferred past
// the send loop.
func (m *Manager) queueAgentNudges(ctx context.Context, o *ports.PRObservation, ident string) (nudges []pendingNudge, ciPolicyErr, err error) {
	reviews, err := m.store.ListPRReviews(ctx, o.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("list persisted reviews for %s: %w", o.URL, err)
	}
	comments, err := m.store.ListPRComments(ctx, o.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("list persisted comments for %s: %w", o.URL, err)
	}
	o.Comments = prCommentObservations(comments)

	if o.CI == domain.CIFailing {
		pr, ok, err := m.store.GetPR(ctx, o.URL)
		switch {
		case err != nil:
			ciPolicyErr = fmt.Errorf("load CI injection policy for %s: %w", o.URL, err)
		case !ok:
			ciPolicyErr = fmt.Errorf("load CI injection policy for %s: PR not found", o.URL)
		case pr.AutoInjectCI:
			checks := failedPRChecks(o.Checks)
			if len(checks) > 0 {
				msg := formatCIFailureMessage(checks)
				if ident != "your PR" {
					msg = strings.Replace(msg, "your PR", ident, 1)
				}
				if o.URL != "" {
					msg += "\nPR: " + domain.SanitizeControlChars(o.URL)
				}
				nudges = append(nudges, pendingNudge{key: "ci:" + o.URL, sig: ciFailureSignature(checks), msg: msg, maxAttempts: 0})
			}
		}
	}

	if hasUnresolvedComments(o.Comments) {
		comments := unresolvedReviewComments(o.Comments)
		for _, comment := range comments {
			if !comment.AutoInjectReview {
				continue
			}
			nudges = append(nudges, reviewCommentNudge(ident, o.URL, comment, string(o.Review)))
		}
	}

	if o.Review == domain.ReviewChangesRequest {
		for _, review := range reviews {
			if review.State != domain.ReviewChangesRequest || !review.AutoInjectReview {
				continue
			}
			msg := formatReviewChangesRequestedMessage(review)
			if ident != "your PR" {
				msg = strings.Replace(msg, "your PR", ident, 1)
			}
			if o.URL != "" && review.URL == "" {
				msg += "\nPR: " + domain.SanitizeControlChars(o.URL)
			}
			nudges = append(nudges, pendingNudge{key: "review:" + o.URL + ":" + review.ID, sig: string(review.State), msg: msg, maxAttempts: reviewMaxNudge})
		}
	}
	return nudges, ciPolicyErr, nil
}

// queueMergeConflictNudge appends the rebase nudge when the PR conflicts and is
// the bottom of its stack. A parent-stack read error is returned for deferral.
func (m *Manager) queueMergeConflictNudge(ctx context.Context, id domain.SessionID, o ports.PRObservation, ident string, nudges []pendingNudge) ([]pendingNudge, error) {
	var blockedCheckErr error
	if o.Mergeability == domain.MergeConflicting {
		// Only the bottom of a stack is eligible for the rebase nudge. A PR
		// stacked on an open parent is expected to report conflicts against its
		// parent branch until the parent merges and it retargets, so nudging the
		// agent to rebase it now would be noise. Mergeability UNKNOWN (the brief
		// post-retarget recompute window) never reaches here.
		blocked, err := m.prBlockedByOpenParent(ctx, id, o.URL)
		switch {
		case err != nil:
			blockedCheckErr = err
		case !blocked:
			msg := "There are merge conflicts on " + ident + ". Rebase onto the base branch and resolve them."
			if o.URL != "" {
				msg += "\nPR: " + domain.SanitizeControlChars(o.URL)
			}
			nudges = append(nudges, pendingNudge{key: mergeConflictKey(o.URL), sig: string(o.Mergeability), msg: msg, maxAttempts: 0, urgent: true})
		}
	}
	return nudges, blockedCheckErr
}

// terminateCompletedSession reports whether it actually killed the session; it
// does not while a session mutation is in progress.
func (m *Manager) terminateCompletedSession(ctx context.Context, id domain.SessionID) (bool, error) {
	if m.sessionMutationInProgress(id) {
		return false, nil
	}
	m.mu.Lock()
	terminator := m.completionTerminator
	m.mu.Unlock()
	if terminator == nil {
		return false, fmt.Errorf("terminate completed session %s: session terminator is not configured", id)
	}
	// Auto-close must not claim a session another caller (an orchestrator's
	// explicit kill) ended first: that would send a false "ended on its own"
	// notice (B554).
	kill := terminator.Kill
	if lo, ok := terminator.(liveOnlyTerminator); ok {
		kill = lo.KillIfLive
	}
	freed, err := kill(ctx, id)
	if err != nil {
		return false, fmt.Errorf("terminate completed session %s: %w", id, err)
	}
	if _, ok := terminator.(liveOnlyTerminator); ok {
		return freed, nil
	}
	return true, nil
}

// sessionComplete reports whether the session has reached the multi-PR
// completion bar: at least one PR merged and no PR still open. A session with no
// PRs, or with any open PR, is not complete.
func (m *Manager) sessionComplete(ctx context.Context, id domain.SessionID) (bool, error) {
	prs, err := m.store.ListPRsBySession(ctx, id)
	if err != nil {
		return false, err
	}
	merged := false
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed {
			return false, nil
		}
		if pr.Merged {
			merged = true
		}
	}
	return merged, nil
}

// mergeConflictKey is the reaction-dedup key for a PR's merge-conflict nudge.
// The send path and the re-arm path share it so the two cannot drift.
func mergeConflictKey(prURL string) string { return "merge-conflict:" + prURL }

// mergeabilityClearsConflict reports whether an observation positively
// establishes that a PR is no longer conflicting, which is what re-arms its
// merge-conflict nudge (#4528).
//
// Only states the provider actually computed count. `unknown` is the transient
// GitHub reports while it recomputes mergeability after a push or a retarget;
// re-arming on it would defeat the dedup entirely, since a conflict that never
// went away flaps unknown → conflicting and would re-nudge on every poll.
// `blocked` is excluded on the same grounds even though it is not literally a
// conflict: the observer synthesizes it locally from draft / failing-CI /
// changes-requested facts (see mergeabilityFromProviderFacts), so it is reported
// even while provider mergeability is still unknown and cannot be read as proof
// the conflict is gone. `mergeable` and `unstable` both require a computed
// provider rollup that ruled conflicts out first, so both are definitive. So
// is a `blocked` that only the noCI review gate produced (MergeableHeldByNoCI):
// the provider verdict under that hold is `mergeable`.
func mergeabilityClearsConflict(o ports.PRObservation) bool {
	return o.Mergeability == domain.MergeMergeable || o.Mergeability == domain.MergeUnstable || o.MergeableHeldByNoCI
}

// rearmMergeConflict clears the merge-conflict dedup entry for prURL so a later
// conflicting observation notifies again. sendOnce otherwise keeps the
// "merge-conflict:<url>" = "conflicting" signature forever: a PR rebased clean
// and then made conflicting again by a base-branch advance stayed silent, and
// because the signature is persisted in pr.last_nudge_signature the suppression
// survived daemon restarts (#4528).
//
// Only this PR's merge-conflict key is touched, so the CI and review-feedback
// entries for the same PR keep their signatures and resolving a conflict cannot
// replay unrelated nudges. The persisted payload is loaded first: after a
// restart the in-memory maps are empty, and persisting from them alone would
// erase every other key the PR row still holds.
func (m *Manager) rearmMergeConflict(ctx context.Context, prURL string) error {
	if m.guard == nil || prURL == "" {
		return nil
	}
	m.react.mu.Lock()
	defer m.react.mu.Unlock()

	if !m.react.loaded[prURL] {
		if err := m.loadPRSignaturesLocked(ctx, prURL); err != nil {
			return err
		}
		m.react.loaded[prURL] = true
	}
	key := mergeConflictKey(prURL)
	_, seen := m.react.seen[key]
	_, attempted := m.react.attempts[key]
	if !seen && !attempted && !m.react.pendingRearm[prURL] {
		// Nothing is armed and no earlier re-arm is still owed a durable write,
		// so skip the store write: a steadily mergeable PR must not re-persist
		// an unchanged payload on every poll.
		return nil
	}
	delete(m.react.seen, key)
	delete(m.react.attempts, key)
	if err := m.persistPRSignaturesLocked(ctx, prURL); err != nil {
		// The in-memory delete deliberately stands: a conflict returning later
		// in this process must still nudge. But disk still holds the stale
		// "conflicting" signature, which a restart would reload and use to
		// suppress that nudge — the exact bug this re-arm exists to fix. Mark
		// the PR so every later non-conflicting observation retries the write
		// until it lands, instead of taking the early return above.
		m.react.pendingRearm[prURL] = true
		return err
	}
	delete(m.react.pendingRearm, prURL)
	return nil
}

// prBlockedByOpenParent reports whether the PR at prURL is stacked on top of
// another still-open PR in the same session — i.e. its target branch is the
// source branch of a sibling open PR. Such a PR is not the bottom of its stack
// and is exempt from merge-conflict nudges. Branch facts are read from the
// store, which the observer has already updated for this observation.
func (m *Manager) prBlockedByOpenParent(ctx context.Context, id domain.SessionID, prURL string) (bool, error) {
	prs, err := m.store.ListPRsBySession(ctx, id)
	if err != nil {
		return false, err
	}
	openSources := make(map[string]bool, len(prs))
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed && pr.SourceBranch != "" {
			openSources[pr.SourceBranch] = true
		}
	}
	for _, pr := range prs {
		if pr.URL == prURL {
			return pr.TargetBranch != "" && openSources[pr.TargetBranch], nil
		}
	}
	return false, nil
}

func firstSCMNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// prIdentity renders a short, sanitized PR identity ("PR #123 \"Title\"
// (feat/x → main)") for nudge messages so an agent in a multi-PR session can
// tell which PR — and where in a stack — a nudge refers to. Title and branch
// names are provider-controlled and reach the agent's live pane, so both are
// control-char sanitized. Falls back to "your PR" when the number is unknown.
func prIdentity(o ports.PRObservation) string {
	if o.Number <= 0 {
		return "your PR"
	}
	id := fmt.Sprintf("PR #%d", o.Number)
	if o.Title != "" {
		id += fmt.Sprintf(" %q", domain.SanitizeControlChars(o.Title))
	}
	if o.SourceBranch != "" && o.TargetBranch != "" {
		id += fmt.Sprintf(" (%s → %s)", domain.SanitizeControlChars(o.SourceBranch), domain.SanitizeControlChars(o.TargetBranch))
	}
	return id
}

// announceMerge tells the orchestrators a merge concerns (no-op when unwired).
func (m *Manager) announceMerge(ctx context.Context, id domain.SessionID, prURL string) {
	m.mu.Lock()
	n := m.mergeNotifier
	m.mu.Unlock()
	if n == nil {
		return
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return
	}
	n.NotifyMerge(ctx, rec.ProjectID, prURL)
}
