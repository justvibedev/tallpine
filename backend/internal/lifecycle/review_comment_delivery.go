package lifecycle

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ReviewThreadReader re-reads a PR's review threads from its provider. Review
// comments are delivered from rows the SCM observer stored up to a poll ago,
// and a worker often resolves a thread in the meantime, so each delivery checks
// the provider's current thread state first.
type ReviewThreadReader interface {
	ReadReviewThreads(ctx context.Context, pr domain.PullRequest) (ports.SCMReviewObservation, error)
}

// SetReviewThreadReader wires the provider read used to skip review threads
// that were resolved or went outdated before delivery. Without one, delivery
// trusts the stored thread state.
func (m *Manager) SetReviewThreadReader(r ReviewThreadReader) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.threadReader = r
}

const (
	reviewThreadReadTimeout       = 10 * time.Second
	deferredReviewDeliveryTimeout = time.Minute
)

// threadSnapshot is one PR's provider thread state, read at most once per
// delivery batch and only when a comment is actually about to be sent.
type threadSnapshot struct {
	m       *Manager
	prURL   string
	read    bool
	ok      bool
	partial bool
	threads map[string]ports.SCMReviewThreadObservation
}

func newThreadSnapshot(m *Manager, prURL string) *threadSnapshot {
	return &threadSnapshot{m: m, prURL: prURL}
}

// stale reports why a comment no longer needs the worker's attention: its
// thread is resolved, outdated, or gone. A failed or missing provider read
// answers "not stale", so delivery falls back to the stored state.
func (s *threadSnapshot) stale(ctx context.Context, c ports.PRCommentObservation) (string, bool) {
	if !s.read {
		s.read = true
		s.load(ctx)
	}
	threadID := strings.TrimSpace(c.ThreadID)
	if !s.ok || threadID == "" {
		return "", false
	}
	th, found := s.threads[threadID]
	switch {
	case !found && !s.partial:
		return "deleted", true
	case !found:
		return "", false
	case th.Resolved:
		return "resolved", true
	case th.Outdated:
		return "outdated", true
	}
	return "", false
}

func (s *threadSnapshot) load(ctx context.Context) {
	s.m.mu.Lock()
	reader := s.m.threadReader
	s.m.mu.Unlock()
	if reader == nil || s.prURL == "" {
		return
	}
	pr, ok, err := s.m.store.GetPR(ctx, s.prURL)
	if err != nil || !ok {
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, reviewThreadReadTimeout)
	defer cancel()
	review, err := reader.ReadReviewThreads(readCtx, pr)
	if err != nil {
		slog.Default().DebugContext(ctx, "lifecycle: review thread re-read failed; using stored state", "pr", s.prURL, "err", err)
		return
	}
	s.ok, s.partial = true, review.Partial
	s.threads = make(map[string]ports.SCMReviewThreadObservation, len(review.Threads))
	for _, th := range review.Threads {
		s.threads[th.ID] = th
	}
}

// reviewCommentNudge builds the nudge for one review comment. ident names the
// PR in the message; fallbackSig stands in for a comment with no ids at all.
func reviewCommentNudge(ident, prURL string, comment ports.PRCommentObservation, fallbackSig string) pendingNudge {
	commentSlice := []ports.PRCommentObservation{comment}
	msg := formatReviewCommentsMessage(commentSlice)
	if ident != "your PR" {
		msg = strings.Replace(msg, "your PR", ident, 1)
	}
	if prURL != "" {
		msg += "\nPR: " + domain.SanitizeControlChars(prURL)
	}
	sig := reviewCommentsSignature(commentSlice)
	if sig == "" {
		sig = fallbackSig
	}
	// Per comment, like the review loop: a shared key is a shared signature
	// slot and a shared attempt budget.
	return pendingNudge{key: commentNudgeKey(prURL, comment), sig: sig, msg: msg, maxAttempts: reviewMaxNudge, comment: &comment}
}

// deliverReviewComment sends one review-comment nudge the worker has not
// received yet. A worker mid-turn would only read it after the turn, by which
// time it has often resolved that very thread, so the nudge is deferred to the
// turn's end instead (deferred=true) and delivered from fresh state then. The
// same holds while another write's turn is pending (see turnClaim), whichever
// delivery path made it. Before sending, the thread is re-read; a resolved,
// outdated, or deleted thread is recorded as handled and not sent. sent
// reports a write to the worker, which starts a turn.
func (m *Manager) deliverReviewComment(ctx context.Context, rec domain.SessionRecord, busy bool, prURL string, n pendingNudge, fresh *threadSnapshot) (deferred, sent bool, err error) {
	if m.guard == nil {
		return false, false, nil
	}
	id := rec.ID
	m.react.mu.Lock()
	if prURL != "" && !m.react.loaded[prURL] {
		if err := m.loadPRSignaturesLocked(ctx, prURL); err != nil {
			m.react.mu.Unlock()
			return false, false, err
		}
		m.react.loaded[prURL] = true
	}
	if m.react.seen[n.key] == n.sig || (n.maxAttempts > 0 && m.react.attempts[n.key] >= n.maxAttempts) {
		m.react.mu.Unlock()
		return false, false, nil
	}
	if busy {
		err := m.deferReviewCommentLocked(ctx, id, prURL, n)
		m.react.mu.Unlock()
		return true, false, err
	}
	m.react.mu.Unlock()
	token, admitted := m.claimTurn(rec)
	if !admitted {
		return true, false, m.deferReviewComment(ctx, id, prURL, n)
	}
	if n.comment != nil {
		if reason, stale := fresh.stale(ctx, *n.comment); stale {
			m.releaseTurn(id, token)
			slog.Default().InfoContext(ctx, "lifecycle: review comment not delivered", "session", id, "pr", prURL, "thread", n.comment.ThreadID, "comment", n.comment.ID, "reason", reason)
			return false, false, m.markReviewCommentHandled(ctx, prURL, n.key, n.sig)
		}
	}
	outcome, err := m.sendOnce(ctx, id, prURL, n.key, n.sig, n.msg, n.maxAttempts, nudgeIdleOnly, nil)
	if outcome != sendOnceSent {
		m.releaseTurn(id, token)
	}
	if outcome == sendOnceBusy {
		// A turn began while the thread was re-read: hold the comment for
		// that turn's end, like one observed mid-turn.
		return true, false, m.deferReviewComment(ctx, id, prURL, n)
	}
	return false, outcome == sendOnceSent, err
}

func (m *Manager) deferReviewComment(ctx context.Context, id domain.SessionID, prURL string, n pendingNudge) error {
	m.react.mu.Lock()
	defer m.react.mu.Unlock()
	if m.react.seen[n.key] == n.sig {
		return nil // delivered by another path meanwhile
	}
	return m.deferReviewCommentLocked(ctx, id, prURL, n)
}

// deferReviewCommentLocked records a deferral. Caller holds react.mu.
func (m *Manager) deferReviewCommentLocked(ctx context.Context, id domain.SessionID, prURL string, n pendingNudge) error {
	m.markSessionDeferred(id)
	if m.react.deferred[n.key] == n.sig {
		return nil
	}
	m.react.deferred[n.key] = n.sig
	if prURL == "" {
		return nil
	}
	return m.persistPRSignaturesLocked(ctx, prURL)
}

// markReviewCommentHandled records a comment as needing no delivery, exactly as
// if it had been sent: a thread that is reopened or answered again brings a
// new comment, which has its own key.
func (m *Manager) markReviewCommentHandled(ctx context.Context, prURL, key, sig string) error {
	m.react.mu.Lock()
	defer m.react.mu.Unlock()
	m.react.seen[key] = sig
	delete(m.react.deferred, key)
	if prURL == "" {
		return nil
	}
	return m.persistPRSignaturesLocked(ctx, prURL)
}

// deliverDeferredAfterTurn delivers, off the activity hook's path, the review
// comments held back while the session's turn was running.
//
// It runs on that hook path, which for a Chat worker holds the controller's
// send lock, while sendOnce holds react.mu across a send that needs that same
// lock. So nothing here may take react.mu or read the store: it consults only
// the deferMu-guarded flag, and the goroutine does the rest.
func (m *Manager) deliverDeferredAfterTurn(ctx context.Context, id domain.SessionID) {
	if m.guard == nil || !m.sessionHasDeferrals(id) {
		return
	}
	// Detached from the hook request, which ends long before the delivery.
	detached := context.WithoutCancel(ctx)
	m.deferredDeliveries.Add(1)
	go func() {
		defer m.deferredDeliveries.Done()
		ctx, cancel := context.WithTimeout(detached, deferredReviewDeliveryTimeout)
		defer cancel()
		if err := m.DeliverDeferredReviewComments(ctx, id); err != nil {
			slog.Default().Warn("lifecycle: deliver deferred review comments", "session", id, "err", err)
		}
	}()
}

// markSessionDeferred records that the session holds a deferred review
// comment. deferMu is a leaf lock: it is taken under react.mu, never the other
// way round, and held across nothing.
func (m *Manager) markSessionDeferred(id domain.SessionID) {
	m.deferMu.Lock()
	defer m.deferMu.Unlock()
	if m.deferredSessions == nil {
		m.deferredSessions = map[domain.SessionID]bool{}
	}
	m.deferredSessions[id] = true
}

func (m *Manager) sessionHasDeferrals(id domain.SessionID) bool {
	m.deferMu.Lock()
	defer m.deferMu.Unlock()
	return m.deferredSessions[id]
}

// clearSessionDeferralsIfDone drops the session's flag when none of its open
// PRs holds a deferral any more. It checks and clears under react.mu, the lock
// a deferral is recorded under, so a deferral made meanwhile cannot be lost.
func (m *Manager) clearSessionDeferralsIfDone(id domain.SessionID, prs []domain.PullRequest) {
	m.react.mu.Lock()
	defer m.react.mu.Unlock()
	for _, pr := range prs {
		if pr.Merged || pr.Closed || pr.URL == "" {
			continue
		}
		for k, v := range m.react.deferred {
			if reactionKeyTargetsPR(k, pr.URL) && m.react.seen[k] != v {
				return
			}
		}
	}
	m.clearSessionDeferred(id)
}

func (m *Manager) clearSessionDeferred(id domain.SessionID) {
	m.deferMu.Lock()
	defer m.deferMu.Unlock()
	delete(m.deferredSessions, id)
}

// RestoreDeferredReviewComments re-arms, after a restart, the sessions whose
// persisted reaction state holds deferred review comments, and delivers to the
// ones already idle. It reads the store and takes react.mu, so it must run off
// any activity-hook path; the daemon calls it once at startup.
func (m *Manager) RestoreDeferredReviewComments(ctx context.Context, ids []domain.SessionID) {
	if m.guard == nil {
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if !m.hasDeferredReviewComments(ctx, id) {
			continue
		}
		m.markSessionDeferred(id)
		if err := m.DeliverDeferredReviewComments(ctx, id); err != nil {
			slog.Default().Warn("lifecycle: deliver restored review comments", "session", id, "err", err)
		}
	}
}

// hasDeferredReviewComments reports whether any open PR of the session holds a
// deferred review comment. A read failure answers false. It reads the store and
// takes react.mu: never call it from the activity-hook path.
func (m *Manager) hasDeferredReviewComments(ctx context.Context, id domain.SessionID) bool {
	prs, err := m.store.ListPRsBySession(ctx, id)
	if err != nil {
		slog.Default().DebugContext(ctx, "lifecycle: list PRs for deferred review comments", "session", id, "err", err)
		return false
	}
	for _, pr := range prs {
		if pr.Merged || pr.Closed || pr.URL == "" {
			continue
		}
		if pending, err := m.deferredReviewKeys(ctx, pr.URL); err == nil && len(pending) > 0 {
			return true
		}
	}
	return false
}

// DeliverDeferredReviewComments sends the review comments deferred while the
// session was mid-turn, once it is idle. Each is re-checked against the stored
// rows and the provider first, so a thread the worker resolved during the turn
// is never handed back to it. Deferrals are durable; after a restart,
// RestoreDeferredReviewComments picks them up again.
func (m *Manager) DeliverDeferredReviewComments(ctx context.Context, id domain.SessionID) error {
	if m.guard == nil {
		return nil
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	if rec.IsTerminated {
		m.clearSessionDeferred(id) // nothing left to deliver to
		return nil
	}
	if cannotNudge(rec) || rec.Activity.State == domain.ActivityActive {
		return nil
	}
	prs, err := m.store.ListPRsBySession(ctx, id)
	if err != nil {
		return err
	}
	// One comment per turn: the first send starts a turn, and every later one
	// stays deferred until that turn ends, then is re-checked in its turn.
	sentAny := false
	for _, pr := range prs {
		if pr.Merged || pr.Closed || pr.URL == "" {
			continue
		}
		pending, err := m.deferredReviewKeys(ctx, pr.URL)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			continue
		}
		comments, err := m.store.ListPRComments(ctx, pr.URL)
		if err != nil {
			return err
		}
		ident := prIdentity(ports.PRObservation{Number: pr.Number, Title: pr.Title, SourceBranch: pr.SourceBranch, TargetBranch: pr.TargetBranch})
		fresh := newThreadSnapshot(m, pr.URL)
		for _, c := range prCommentObservations(comments) {
			if !c.AutoInjectReview {
				continue
			}
			n := reviewCommentNudge(ident, pr.URL, c, string(pr.Review))
			if _, ok := pending[n.key]; !ok {
				continue
			}
			delete(pending, n.key)
			_, sent, err := m.deliverReviewComment(ctx, rec, sentAny, pr.URL, n, fresh)
			if err != nil {
				return err
			}
			sentAny = sentAny || sent
		}
		// What is left was resolved, deleted, or opted out of injection since it
		// was deferred: nothing to deliver.
		if len(pending) > 0 {
			if err := m.dropDeferred(ctx, pr.URL, pending); err != nil {
				return err
			}
		}
	}
	m.clearSessionDeferralsIfDone(id, prs)
	return nil
}

func (m *Manager) deferredReviewKeys(ctx context.Context, prURL string) (map[string]string, error) {
	m.react.mu.Lock()
	defer m.react.mu.Unlock()
	if !m.react.loaded[prURL] {
		if err := m.loadPRSignaturesLocked(ctx, prURL); err != nil {
			return nil, err
		}
		m.react.loaded[prURL] = true
	}
	out := map[string]string{}
	for k, v := range m.react.deferred {
		if reactionKeyTargetsPR(k, prURL) && m.react.seen[k] != v {
			out[k] = v
		}
	}
	return out, nil
}

func (m *Manager) dropDeferred(ctx context.Context, prURL string, keys map[string]string) error {
	m.react.mu.Lock()
	defer m.react.mu.Unlock()
	for k := range keys {
		delete(m.react.deferred, k)
	}
	return m.persistPRSignaturesLocked(ctx, prURL)
}
