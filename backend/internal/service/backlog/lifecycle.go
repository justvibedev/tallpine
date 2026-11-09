package backlog

import (
	"context"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// Reconcile applies the lifecycle rules to one item from the facts in the
// database. It is idempotent and holds no state, so any trigger (a CDC event, a
// report, the sweep) may call it any number of times.
//
//   - Every linked PR merged: done, "merged #N". Not for scope local. An item
//     with a done-when also needs a merged PR titled "closes <ref>".
//   - A PR closed unmerged, or a linked session ended without merging: the item
//     stays open and gets one note line.
func (s *Service) Reconcile(ctx context.Context, itemID string) error {
	item, ok, err := s.store.GetBacklogItem(ctx, itemID)
	if err != nil {
		return err
	}
	if !ok || item.State.Closed() {
		return nil
	}
	facts, err := s.store.BacklogFactsFor(ctx, item.ID)
	if err != nil {
		return err
	}
	decision := DecideAutoClose(item, facts)
	if decision.Close {
		_, changed, err := s.store.CloseMergedBacklogItem(ctx, item.ID, decision.Reason, s.now().UTC())
		if err != nil {
			return err
		}
		if changed {
			s.plans.ItemClosed(ctx, item.ID)
			s.log.Info("backlog auto-close", "item", item.Ref(), "prs", decision.Merged, "decision", "closed", "reason", decision.Reason)
		} else {
			// Closed by hand, or scope set to local, between the read and the write.
			s.logDecision(item, decision, "kept open", "item changed while closing: closed already or scope is local")
		}
		return nil
	}
	if len(decision.Merged) > 0 {
		s.logDecision(item, decision, "kept open", decision.Reason)
	}
	var notes []string
	for _, pr := range facts.PRs {
		if pr.State == "closed" {
			notes = append(notes, fmt.Sprintf("PR #%d closed without merge", pr.Number))
		}
	}
	for _, sess := range facts.Sessions {
		if !sess.Terminated || sessionHasPR(facts, sess.SessionID, "merged", "open", "draft") {
			continue
		}
		// A planner ending is the plan's business, not an abandoned worker.
		if s.plans.SessionEnded(ctx, domain.SessionID(sess.SessionID)) {
			continue
		}
		notes = append(notes, fmt.Sprintf("worker %s ended without merge", sess.SessionID))
	}
	for _, text := range notes {
		if err := s.appendAutoNote(ctx, item.ID, text); err != nil {
			return err
		}
	}
	return nil
}

// sessionHasPR reports whether the session owns a PR in one of the states. A
// session that ended while its PR is still open is waiting for review or a
// merge, not abandoned, so it gets no "ended without merge" line.
func sessionHasPR(f store.BacklogFacts, sessionID string, states ...string) bool {
	for _, pr := range append(append([]store.BacklogPRFact{}, f.PRs...), f.AckedPRs...) {
		if pr.SessionID != sessionID {
			continue
		}
		for _, st := range states {
			if pr.State == st {
				return true
			}
		}
	}
	return false
}

func (s *Service) appendAutoNote(ctx context.Context, itemID, text string) error {
	_, err := s.store.UpdateBacklogItem(ctx, itemID, s.now().UTC(), func(i *domain.BacklogItem) error {
		next, err := withAutoNote(i.AgentNotes, s.now(), text)
		if err != nil {
			return err
		}
		i.AgentNotes = next
		return nil
	})
	if errors.Is(err, errNoNoteChange) {
		return nil
	}
	return err
}

// ReconcileLinked reconciles every live item linked to a session or PR.
func (s *Service) ReconcileLinked(ctx context.Context, kind domain.BacklogLinkKind, ref string) error {
	items, err := s.store.ListLiveBacklogItemsLinkedTo(ctx, kind, ref)
	if err != nil {
		return err
	}
	var errs []error
	for _, it := range items {
		if err := s.Reconcile(ctx, it.ID); err != nil {
			errs = append(errs, err)
		}
	}
	if kind == domain.BacklogLinkPR {
		if err := s.closeClaimedItems(ctx, ref); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// closeClaimedItems closes the live items a merged PR claims with "closes B<n>"
// in its title although the PR is not linked to them (a worker spawned for one
// item that also finishes another). Items the PR is linked to go through
// Reconcile; an item whose scope is local stays open. The close records a pr
// link on the item so a manual reopen acknowledges this PR and later events for
// it do not close the item again.
func (s *Service) closeClaimedItems(ctx context.Context, prURL string) error {
	pr, ok, err := s.store.GetPR(ctx, prURL)
	if err != nil || !ok || !pr.Merged {
		return err
	}
	refs := ClaimedRefs(pr.Title)
	if len(refs) == 0 || pr.SessionID == "" {
		return nil
	}
	sess, ok, err := s.store.GetSession(ctx, pr.SessionID)
	if err != nil || !ok {
		return err
	}
	var errs []error
	for _, ref := range refs {
		seq, _ := domain.ParseBacklogRef(ref)
		item, ok, err := s.store.GetBacklogItemByRef(ctx, sess.ProjectID, seq)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok || item.State.Closed() {
			continue
		}
		facts, err := s.store.BacklogFactsFor(ctx, item.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if factsHavePR(facts, prURL) {
			continue // linked: Reconcile decides (earlier PRs of a reused worker do not count there, so they fall through)
		}
		reason := domain.BacklogReasonMerged([]int64{int64(pr.Number)})
		_, changed, err := s.store.CloseMergedBacklogItem(ctx, item.ID, reason, s.now().UTC())
		if err != nil {
			errs = append(errs, err)
		} else if changed {
			link := domain.BacklogLink{Kind: domain.BacklogLinkPR, Ref: prURL, CreatedBy: "auto-close", CreatedAt: s.now().UTC()}
			if _, err := s.store.AddBacklogLink(ctx, item.ID, link, s.now().UTC()); err != nil {
				errs = append(errs, err)
			}
			s.plans.ItemClosed(ctx, item.ID)
			s.log.Info("backlog auto-close", "item", item.Ref(), "prs", []int{pr.Number}, "decision", "closed", "reason", reason+" (claimed by an unlinked PR)")
		}
	}
	return errors.Join(errs...)
}

func factsHavePR(f store.BacklogFacts, url string) bool {
	for _, group := range [][]store.BacklogPRFact{f.PRs, f.AckedPRs} {
		for _, pr := range group {
			if pr.URL == url {
				return true
			}
		}
	}
	return false
}

// Sweep reconciles every live item that has a session or PR link. It repairs
// whatever live events missed while the daemon was down.
func (s *Service) Sweep(ctx context.Context) error {
	items, err := s.store.ListLiveBacklogItemsWithRuntimeLinks(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, it := range items {
		if err := s.Reconcile(ctx, it.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ReportFiled applies the worker-done rule: a done report closes the items
// linked to its session that have no PR at all. unverified is the report's
// soft-verification result; a non-empty one means the work looks undelivered.
func (s *Service) ReportFiled(ctx context.Context, r domain.ReportRecord, unverified func(context.Context, domain.ReportRecord) string) error {
	if r.State != domain.ReportDone || r.Synthesized {
		return nil
	}
	for _, o := range r.Outputs {
		if o.Kind == domain.ReportOutputPRCreated || o.Kind == domain.ReportOutputPRReviewed {
			return nil
		}
	}
	// A planner's done report means the plan is submitted, not that the work is
	// done: the item it planned stays open.
	if s.plans.isPlanner(ctx, r.SessionID) {
		return nil
	}
	items, err := s.store.ListLiveBacklogItemsLinkedTo(ctx, domain.BacklogLinkSession, string(r.SessionID))
	if err != nil || len(items) == 0 {
		return err
	}
	if unverified != nil && unverified(ctx, r) != "" {
		return nil
	}
	var errs []error
	for _, it := range items {
		if it.Scope == domain.BacklogScopeLocal {
			continue
		}
		facts, err := s.store.BacklogFactsFor(ctx, it.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(facts.PRs) > 0 || len(facts.AckedPRs) > 0 || len(facts.UntrackedPRs) > 0 || hasUnsettledPR(facts.EarlierPRs) {
			continue
		}
		if _, _, err := s.store.CloseBacklogItemIfLive(ctx, it.ID, domain.BacklogDone, domain.BacklogReasonWorkerDone, s.now().UTC()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// logDecision logs a decision to leave an item open although a linked PR
// merged. It repeats on every session update, so it logs only when the verdict
// for the item changes.
func (s *Service) logDecision(item domain.BacklogItem, d AutoClose, decision, reason string) {
	key := decision + ": " + reason
	s.logMu.Lock()
	seen := s.lastLogged[item.ID] == key
	s.lastLogged[item.ID] = key
	s.logMu.Unlock()
	if !seen {
		s.log.Info("backlog auto-close", "item", item.Ref(), "prs", d.Merged, "decision", decision, "reason", reason)
	}
}

// hasUnsettledPR reports whether any PR is still open or draft. A PR the
// session opened before it was linked does not count toward the item, but it
// must still keep a done report from closing the item as having no PR.
func hasUnsettledPR(prs []store.BacklogPRFact) bool {
	for _, pr := range prs {
		if pr.State != "merged" && pr.State != "closed" {
			return true
		}
	}
	return false
}
