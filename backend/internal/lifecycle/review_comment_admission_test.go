package lifecycle

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// blockingThreadReader holds its first provider read until release closes, so
// a test can keep one delivery between its idle check and its send.
type blockingThreadReader struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	review  ports.SCMReviewObservation
}

func (b *blockingThreadReader) ReadReviewThreads(context.Context, domain.PullRequest) (ports.SCMReviewObservation, error) {
	if b.calls.Add(1) == 1 {
		close(b.entered)
		<-b.release
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	review := b.review
	review.Threads = append([]ports.SCMReviewThreadObservation(nil), b.review.Threads...)
	return review, nil
}

func (b *blockingThreadReader) resolve(thread string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.review.Threads {
		if b.review.Threads[i].ID == thread {
			b.review.Threads[i].Resolved = true
		}
	}
}

// Two delivery paths (startup restore and an observation) both see the worker
// idle. Only one may write a comment into the next turn; the other's comments
// wait for that turn to end and are re-checked then, so a thread the worker
// resolves during the turn is not replayed through the chat queue.
func TestReviewCommentDelivery_ConcurrentPathsAdmitOneCommentPerTurn(t *testing.T) {
	m, st, _, _ := newDeliveryFixture(domain.ActivityActive)
	st.comments["pr1"] = []domain.PullRequestComment{
		reviewComment("c1", "T1", "first finding"),
		reviewComment("c2", "T2", "second finding"),
	}
	observePR1(t, m) // mid-turn: both deferred and persisted
	rec := st.sessions["mer-1"]
	rec.Activity.State = domain.ActivityIdle // the turn ended while the daemon was down
	st.sessions["mer-1"] = rec

	messenger := &fakeMessenger{}
	restarted := New(st, messenger)
	reader := &blockingThreadReader{
		entered: make(chan struct{}), release: make(chan struct{}),
		review: ports.SCMReviewObservation{Threads: []ports.SCMReviewThreadObservation{openThread("T1", "c1"), openThread("T2", "c2")}},
	}
	restarted.SetReviewThreadReader(reader)
	releaseOnce := sync.OnceFunc(func() { close(reader.release) })
	t.Cleanup(releaseOnce)

	restored := make(chan struct{})
	go func() {
		defer close(restored)
		restarted.RestoreDeferredReviewComments(ctx, []domain.SessionID{"mer-1"})
	}()
	select {
	case <-reader.entered: // restore has claimed the turn and is re-reading C1's thread
	case <-time.After(5 * time.Second):
		t.Fatal("restore never reached the provider read")
	}

	// An observation of the same idle worker meanwhile must not send.
	if err := restarted.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Number: 7}); err != nil {
		t.Fatal(err)
	}
	if len(messenger.msgs) != 0 {
		t.Fatalf("observation wrote while another delivery held the turn: %v", messenger.msgs)
	}

	releaseOnce()
	select {
	case <-restored:
	case <-time.After(5 * time.Second):
		t.Fatal("restore did not finish")
	}
	if len(messenger.msgs) != 1 || !strings.Contains(messenger.msgs[0], "first finding") {
		t.Fatalf("msgs = %v, want exactly one comment to start the turn", messenger.msgs)
	}

	reader.resolve("T2") // the worker resolves the second thread during that turn
	endTurn(t, restarted, st, "mer-1")
	if len(messenger.msgs) != 1 {
		t.Fatalf("resolved thread replayed after the turn: %v", messenger.msgs)
	}
	if strings.Contains(st.signatures["pr1"], `"deferred"`) {
		t.Fatalf("deferrals left behind: %q", st.signatures["pr1"])
	}
}

// A write made while the worker is mid-turn needs the turn after the current
// one, so the current turn's end must not admit the next comment.
func TestReviewCommentDelivery_NudgeQueuedBehindARunningTurnHoldsCommentsForItsOwnTurn(t *testing.T) {
	m, st, msg, reader := newDeliveryFixture(domain.ActivityActive, openThread("T1", "c1"))
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{
		Fetched: true, URL: "pr1", Number: 7, CI: domain.CIFailing,
		Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "boom") {
		t.Fatalf("msgs = %v, want only the CI nudge", msg.msgs)
	}
	// The running turn ends; the CI nudge's own turn has not run yet.
	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	m.deferredDeliveries.Wait()
	if len(msg.msgs) != 1 {
		t.Fatalf("comment admitted before the CI nudge's turn: %v", msg.msgs)
	}
	// That turn runs; the worker resolves the thread in it.
	reader.review.Threads[0].Resolved = true
	endTurn(t, m, st, "mer-1")
	if len(msg.msgs) != 1 {
		t.Fatalf("thread resolved during the CI turn was delivered: %v", msg.msgs)
	}
}

// A CI nudge already delivered is rebuilt on every poll while CI stays red.
// sendOnce dedups it, and a deduped write must not touch the turn claim, or
// review comments would wait past the turn that consumed the original nudge
// for as long as CI stays red. (Adopted from the reviewer's repro.)
func TestReviewCommentDelivery_DedupedNudgeDoesNotStarveComments(t *testing.T) {
	m, st, msg, _ := newDeliveryFixture(domain.ActivityIdle, openThread("T1", "c1"))
	st.comments["pr1"] = nil
	ciRed := ports.PRObservation{
		Fetched: true, URL: "pr1", Number: 7, CI: domain.CIFailing,
		Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
	}
	if err := m.ApplyPRObservation(ctx, "mer-1", ciRed); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want the CI nudge, got %v", msg.msgs)
	}
	for turn := 1; turn <= 3; turn++ {
		// The CI nudge's turn (and later ones) runs; a poll lands mid-turn with
		// CI still red and a review comment now open.
		if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityActive}); err != nil {
			t.Fatal(err)
		}
		st.comments["pr1"] = []domain.PullRequestComment{reviewComment("c1", "T1", "fix this")}
		if err := m.ApplyPRObservation(ctx, "mer-1", ciRed); err != nil {
			t.Fatal(err)
		}
		if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
			t.Fatal(err)
		}
		m.deferredDeliveries.Wait()
		// An idle poll between turns.
		if err := m.ApplyPRObservation(ctx, "mer-1", ciRed); err != nil {
			t.Fatal(err)
		}
		for _, s := range msg.msgs {
			if strings.Contains(s, "fix this") {
				if turn != 1 {
					t.Fatalf("comment delivered only after turn %d, want after the CI nudge's turn", turn)
				}
				return
			}
		}
	}
	t.Fatalf("review comment still held after 3 turns: %d msgs %v", len(msg.msgs), msg.msgs)
}

// A write queued behind a running turn waits for its own turn however long the
// running one takes: the 15-minute lapse counts only from when that turn ends.
func TestReviewCommentDelivery_LongRunningTurnDoesNotLapseAQueuedClaim(t *testing.T) {
	m, st, msg, _ := newDeliveryFixture(domain.ActivityActive, openThread("T1", "c1"))
	now := time.Now()
	m.clock = func() time.Time { return now }
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{
		Fetched: true, URL: "pr1", Number: 7, CI: domain.CIFailing,
		Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("msgs = %v, want only the CI nudge", msg.msgs)
	}
	now = now.Add(40 * time.Minute) // the running turn takes 40 minutes
	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	m.deferredDeliveries.Wait()
	if len(msg.msgs) != 1 {
		t.Fatalf("a long running turn lapsed the CI nudge's claim: %v", msg.msgs)
	}
	endTurn(t, m, st, "mer-1") // the CI nudge's own turn
	if len(msg.msgs) != 2 || !strings.Contains(msg.msgs[1], "fix this") {
		t.Fatalf("msgs = %v, want the comment after the CI nudge's turn", msg.msgs)
	}
}

// A write that never starts a turn stops holding comments back once the
// session has been idle for the lapse window.
func TestReviewCommentDelivery_ClaimThatNeverStartsATurnLapses(t *testing.T) {
	m, st, msg, _ := newDeliveryFixture(domain.ActivityIdle, openThread("T1", "c1"), openThread("T2", "c2"))
	st.comments["pr1"] = append(st.comments["pr1"], reviewComment("c2", "T2", "second finding"))
	now := time.Now()
	m.clock = func() time.Time { return now }
	observePR1(t, m)
	if len(msg.msgs) != 1 {
		t.Fatalf("msgs = %v, want the first comment", msg.msgs)
	}
	now = now.Add(turnClaimTTL + time.Minute) // its turn never began
	if err := m.DeliverDeferredReviewComments(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("msgs = %v, want the lapsed claim to admit the second comment", msg.msgs)
	}
}

func TestReviewCommentDelivery_TerminationDropsTheTurnClaim(t *testing.T) {
	m, _, msg, _ := newDeliveryFixture(domain.ActivityIdle, openThread("T1", "c1"))
	observePR1(t, m)
	if len(msg.msgs) != 1 {
		t.Fatalf("msgs = %v", msg.msgs)
	}
	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	m.deferMu.Lock()
	_, held := m.turnClaims["mer-1"]
	m.deferMu.Unlock()
	if held {
		t.Fatal("a terminated session kept its turn claim")
	}
}

// A tracker-bot nudge starts a turn like any other write.
func TestReviewCommentDelivery_TrackerBotNudgeTakesTheTurn(t *testing.T) {
	m, st, msg, _ := newDeliveryFixture(domain.ActivityIdle, openThread("T1", "c1"))
	if err := m.ApplyTrackerFacts(ctx, "mer-1", ports.TrackerObservation{
		Fetched:  true,
		Issue:    ports.TrackerIssueObservation{URL: "https://github.com/o/r/issues/1", State: domain.IssueOpen},
		Changed:  ports.TrackerChanged{Comments: true},
		Comments: []ports.TrackerCommentObservation{{ID: "b1", Author: "lint-bot", Body: "lint failed", IsBot: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("msgs = %v, want the tracker-bot nudge", msg.msgs)
	}
	observePR1(t, m)
	if len(msg.msgs) != 1 {
		t.Fatalf("comment written into the tracker nudge's turn: %v", msg.msgs)
	}
	endTurn(t, m, st, "mer-1")
	if len(msg.msgs) != 2 || !strings.Contains(msg.msgs[1], "fix this") {
		t.Fatalf("msgs = %v, want the comment after the tracker nudge's turn", msg.msgs)
	}
}

// Steering is a terminal capability. A Chat worker's write is queued behind
// its running turn even on a harness that steers its TUI, so a mid-turn CI
// nudge still needs a turn of its own before comments are admitted.
func TestReviewCommentDelivery_ChatWorkerOnASteeringHarnessQueuesTheNudge(t *testing.T) {
	m, st, msg, _ := newDeliveryFixture(domain.ActivityActive, openThread("T1", "c1"))
	m.steerActive = func(domain.AgentHarness) bool { return true }
	rec := st.sessions["mer-1"]
	rec.Harness, rec.Mode = domain.AgentHarness("codex"), domain.SessionModeChat
	rec.Metadata.ControllerGeneration = "gen-1"
	st.sessions["mer-1"] = rec
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{
		Fetched: true, URL: "pr1", Number: 7, CI: domain.CIFailing,
		Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyActivitySignal(ctx, "mer-1", sessionSignal(st, "mer-1", domain.ActivityIdle)); err != nil {
		t.Fatal(err)
	}
	m.deferredDeliveries.Wait()
	if got := st.sessions["mer-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("the running turn's end was not applied: state %q", got)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("comment admitted before the queued CI nudge's turn: %v", msg.msgs)
	}
	endTurn(t, m, st, "mer-1")
	if len(msg.msgs) != 2 || !strings.Contains(msg.msgs[1], "fix this") {
		t.Fatalf("msgs = %v, want the comment after the CI nudge's turn", msg.msgs)
	}
}

// In terminal mode a steering harness takes the mid-turn nudge into the
// running turn, so that turn's end admits the next comment.
func TestReviewCommentDelivery_TerminalSteeringConsumesTheNudgeInTheRunningTurn(t *testing.T) {
	m, st, msg, _ := newDeliveryFixture(domain.ActivityActive, openThread("T1", "c1"))
	m.steerActive = func(domain.AgentHarness) bool { return true }
	rec := st.sessions["mer-1"]
	rec.Harness, rec.Mode = domain.AgentHarness("codex"), domain.SessionModeTUI
	st.sessions["mer-1"] = rec
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{
		Fetched: true, URL: "pr1", Number: 7, CI: domain.CIFailing,
		Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyActivitySignal(ctx, "mer-1", sessionSignal(st, "mer-1", domain.ActivityIdle)); err != nil {
		t.Fatal(err)
	}
	m.deferredDeliveries.Wait()
	if len(msg.msgs) != 2 || !strings.Contains(msg.msgs[1], "fix this") {
		t.Fatalf("msgs = %v, want the comment at the end of the turn that took the nudge", msg.msgs)
	}
}

func applyActivity(t *testing.T, m *Manager, st *fakeStore, states ...domain.ActivityState) {
	t.Helper()
	for _, s := range states {
		if err := m.ApplyActivitySignal(ctx, "mer-1", sessionSignal(st, "mer-1", s)); err != nil {
			t.Fatal(err)
		}
	}
	m.deferredDeliveries.Wait()
}

func ciRedObservation(commit string) ports.PRObservation {
	return ports.PRObservation{
		Fetched: true, URL: "pr1", Number: 7, CI: domain.CIFailing,
		Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: commit, Status: domain.PRCheckFailed, LogTail: "boom"}},
	}
}

func TestReviewCommentDelivery_ResumeFromAPromptIsNotTheQueuedTurn(t *testing.T) {
	m, st, msg, _ := newDeliveryFixture(domain.ActivityActive, openThread("T1", "c1"))
	if err := m.ApplyPRObservation(ctx, "mer-1", ciRedObservation("c1")); err != nil {
		t.Fatal(err)
	}
	// The running turn pauses on an approval and resumes once answered: still
	// the same turn, so its end must not admit a comment ahead of the CI turn.
	for _, s := range []domain.ActivityState{domain.ActivityWaitingInput, domain.ActivityActive, domain.ActivityBlocked, domain.ActivityActive, domain.ActivityIdle} {
		applyActivity(t, m, st, s)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("msgs = %v, want only the CI nudge until its turn ends", msg.msgs)
	}
	endTurn(t, m, st, "mer-1") // the CI nudge's own turn
	if len(msg.msgs) != 2 || !strings.Contains(msg.msgs[1], "fix this") {
		t.Fatalf("msgs = %v, want the comment after the CI nudge's turn", msg.msgs)
	}
}

func TestReviewCommentDelivery_QueuedTurnStartingOnAPromptTakesTheClaim(t *testing.T) {
	m, st, msg, _ := newDeliveryFixture(domain.ActivityActive, openThread("T1", "c1"))
	if err := m.ApplyPRObservation(ctx, "mer-1", ciRedObservation("c1")); err != nil {
		t.Fatal(err)
	}
	applyActivity(t, m, st, domain.ActivityIdle) // the running turn ends
	if len(msg.msgs) != 1 {
		t.Fatalf("msgs = %v, want only the CI nudge", msg.msgs)
	}
	// The CI nudge's turn opens straight on an approval, then finishes.
	applyActivity(t, m, st, domain.ActivityWaitingInput, domain.ActivityActive, domain.ActivityIdle)
	if len(msg.msgs) != 2 || !strings.Contains(msg.msgs[1], "fix this") {
		t.Fatalf("msgs = %v, want the comment after the CI nudge's turn", msg.msgs)
	}
}

func TestReviewCommentDelivery_LongPromptMidTurnDoesNotLapseAQueuedClaim(t *testing.T) {
	m, st, msg, _ := newDeliveryFixture(domain.ActivityActive, openThread("T1", "c1"))
	now := time.Now()
	m.clock = func() time.Time { return now }
	if err := m.ApplyPRObservation(ctx, "mer-1", ciRedObservation("c1")); err != nil {
		t.Fatal(err)
	}
	applyActivity(t, m, st, domain.ActivityWaitingInput)
	now = now.Add(2 * turnClaimTTL) // the user answers much later
	// A poll while the prompt waits must not lapse the claim and send.
	if err := m.ApplyPRObservation(ctx, "mer-1", ciRedObservation("c1")); err != nil {
		t.Fatal(err)
	}
	applyActivity(t, m, st, domain.ActivityActive, domain.ActivityIdle)
	if len(msg.msgs) != 1 {
		t.Fatalf("a long prompt lapsed the CI nudge's claim: %v", msg.msgs)
	}
	endTurn(t, m, st, "mer-1")
	if len(msg.msgs) != 2 || !strings.Contains(msg.msgs[1], "fix this") {
		t.Fatalf("msgs = %v, want the comment after the CI nudge's turn", msg.msgs)
	}
}

func TestReviewCommentDelivery_NudgeWrittenDuringAPromptNeedsTheNextTurn(t *testing.T) {
	m, st, msg, _ := newDeliveryFixture(domain.ActivityIdle, openThread("T1", "c1"))
	m.urgentNudgeWaitingInputSafe = func(domain.AgentHarness) bool { return true }
	st.comments["pr1"] = nil
	if err := m.ApplyPRObservation(ctx, "mer-1", ciRedObservation("c1")); err != nil {
		t.Fatal(err)
	}
	// The CI nudge's turn starts and pauses on a question; a merge conflict,
	// the one nudge that reaches a prompt, is written then and queues behind
	// this turn.
	applyActivity(t, m, st, domain.ActivityActive, domain.ActivityWaitingInput)
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Number: 7, Mergeability: domain.MergeConflicting}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("msgs = %v, want the CI and merge-conflict nudges", msg.msgs)
	}
	st.comments["pr1"] = []domain.PullRequestComment{reviewComment("c1", "T1", "fix this")}
	applyActivity(t, m, st, domain.ActivityActive, domain.ActivityIdle)
	if len(msg.msgs) != 2 {
		t.Fatalf("msgs = %v, want the comment held for the merge-conflict nudge's turn", msg.msgs)
	}
	// An idle poll sees the comment; the merge-conflict nudge's turn holds it.
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Number: 7, Mergeability: domain.MergeConflicting}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("msgs = %v, want the comment held for the merge-conflict nudge's turn", msg.msgs)
	}
	endTurn(t, m, st, "mer-1")
	if len(msg.msgs) != 3 || !strings.Contains(msg.msgs[2], "fix this") {
		t.Fatalf("msgs = %v, want the comment after the merge-conflict nudge's turn", msg.msgs)
	}
}

func TestReviewCommentDelivery_TurnStartingDuringTheThreadReadDefersTheComment(t *testing.T) {
	m, st, msg, _ := newDeliveryFixture(domain.ActivityIdle)
	st.comments["pr1"] = []domain.PullRequestComment{reviewComment("c1", "T1", "fix this")}
	reader := &blockingThreadReader{
		entered: make(chan struct{}), release: make(chan struct{}),
		review: ports.SCMReviewObservation{Threads: []ports.SCMReviewThreadObservation{openThread("T1", "c1")}},
	}
	m.SetReviewThreadReader(reader)
	releaseOnce := sync.OnceFunc(func() { close(reader.release) })
	t.Cleanup(releaseOnce)

	observed := make(chan error, 1)
	go func() {
		observed <- m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Number: 7})
	}()
	select {
	case <-reader.entered: // the worker read idle; the thread re-read is in flight
	case <-time.After(5 * time.Second):
		t.Fatal("delivery never reached the provider read")
	}
	// The user starts a turn before the write.
	applyActivity(t, m, st, domain.ActivityActive)
	releaseOnce()
	select {
	case err := <-observed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("observation did not finish")
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("comment written into a turn that began during the re-read: %v", msg.msgs)
	}
	endTurn(t, m, st, "mer-1")
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "fix this") {
		t.Fatalf("msgs = %v, want the comment at that turn's end", msg.msgs)
	}
}
