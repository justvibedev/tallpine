package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// fakeThreadReader answers the provider's current thread state.
type fakeThreadReader struct {
	review ports.SCMReviewObservation
	err    error
	calls  int
}

func (f *fakeThreadReader) ReadReviewThreads(context.Context, domain.PullRequest) (ports.SCMReviewObservation, error) {
	f.calls++
	return f.review, f.err
}

func openThread(id string, comments ...string) ports.SCMReviewThreadObservation {
	th := ports.SCMReviewThreadObservation{ID: id, Path: "foo.go", Line: 12}
	for _, c := range comments {
		th.Comments = append(th.Comments, ports.SCMReviewCommentObservation{ID: c})
	}
	return th
}

func reviewComment(id, thread, body string) domain.PullRequestComment {
	return domain.PullRequestComment{ID: id, ThreadID: thread, Author: "alice", File: "foo.go", Line: 12, Body: body, AutoInjectReview: true}
}

// newDeliveryFixture seeds one worker that owns PR pr1 with comment c1 on
// thread T1, and a provider that reports threads as given.
func newDeliveryFixture(state domain.ActivityState, threads ...ports.SCMReviewThreadObservation) (*Manager, *fakeStore, *fakeMessenger, *fakeThreadReader) {
	m, st, msg := newManager()
	rec := working("mer-1")
	rec.Activity.State = state
	st.sessions["mer-1"] = rec
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", SessionID: "mer-1", Number: 7, Provider: "github", Host: "github.com", Repo: "o/r"}}
	st.comments["pr1"] = []domain.PullRequestComment{reviewComment("c1", "T1", "fix this")}
	reader := &fakeThreadReader{review: ports.SCMReviewObservation{Threads: threads}}
	m.SetReviewThreadReader(reader)
	return m, st, msg, reader
}

// endTurn runs one turn of the session to its end and waits for the review
// comments due at that turn end.
func endTurn(t *testing.T, m *Manager, st *fakeStore, id domain.SessionID) {
	t.Helper()
	if st.sessions[id].Activity.State != domain.ActivityActive {
		if err := m.ApplyActivitySignal(ctx, id, sessionSignal(st, id, domain.ActivityActive)); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ApplyActivitySignal(ctx, id, sessionSignal(st, id, domain.ActivityIdle)); err != nil {
		t.Fatal(err)
	}
	m.deferredDeliveries.Wait()
}

// sessionSignal is an activity signal the reducer accepts for the session's
// mode: a Chat session's signals must name its current controller generation.
func sessionSignal(st *fakeStore, id domain.SessionID, state domain.ActivityState) ports.ActivitySignal {
	s := ports.ActivitySignal{Valid: true, State: state}
	if rec := st.sessions[id]; rec.Mode == domain.SessionModeChat {
		s.ControllerGeneration = rec.Metadata.ControllerGeneration
	}
	return s
}

func observePR1(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Number: 7}); err != nil {
		t.Fatal(err)
	}
}

func TestReviewCommentDelivery_SkipsThreadsHandledOnTheProvider(t *testing.T) {
	resolved := openThread("T1", "c1")
	resolved.Resolved = true
	outdated := openThread("T1", "c1")
	outdated.Outdated = true
	for name, threads := range map[string][]ports.SCMReviewThreadObservation{
		"resolved": {resolved},
		"outdated": {outdated},
		"deleted":  {openThread("T2", "c9")},
	} {
		t.Run(name, func(t *testing.T) {
			m, _, msg, reader := newDeliveryFixture(domain.ActivityIdle, threads...)
			observePR1(t, m)
			if len(msg.msgs) != 0 {
				t.Fatalf("a %s thread was delivered: %v", name, msg.msgs)
			}
			// Recorded as handled: the stale stored row never comes back on a
			// later observation, without another provider read.
			observePR1(t, m)
			if len(msg.msgs) != 0 || reader.calls != 1 {
				t.Fatalf("re-observation: msgs=%v reads=%d", msg.msgs, reader.calls)
			}
		})
	}
}

func TestReviewCommentDelivery_OpenThreadIsDeliveredOnce(t *testing.T) {
	m, _, msg, _ := newDeliveryFixture(domain.ActivityIdle, openThread("T1", "c1"))
	observePR1(t, m)
	observePR1(t, m)
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "fix this") || !strings.Contains(msg.msgs[0], "PR #7") {
		t.Fatalf("msgs = %v, want the comment exactly once", msg.msgs)
	}
}

func TestReviewCommentDelivery_PartialOrFailedReadFallsBackToStoredState(t *testing.T) {
	t.Run("partial window without the thread", func(t *testing.T) {
		m, _, msg, reader := newDeliveryFixture(domain.ActivityIdle, openThread("T2", "c9"))
		reader.review.Partial = true
		observePR1(t, m)
		if len(msg.msgs) != 1 {
			t.Fatalf("msgs = %v, want delivery when the thread may be outside the window", msg.msgs)
		}
	})
	t.Run("provider error", func(t *testing.T) {
		m, _, msg, reader := newDeliveryFixture(domain.ActivityIdle)
		reader.err = errors.New("rate limited")
		observePR1(t, m)
		if len(msg.msgs) != 1 {
			t.Fatalf("msgs = %v, want delivery from stored state", msg.msgs)
		}
	})
}

func TestReviewCommentDelivery_NewReplyOnReceivedThreadDeliversOnlyTheReply(t *testing.T) {
	m, st, msg, reader := newDeliveryFixture(domain.ActivityIdle, openThread("T1", "c1"))
	observePR1(t, m)
	if len(msg.msgs) != 1 {
		t.Fatalf("first delivery = %v", msg.msgs)
	}
	endTurn(t, m, st, "mer-1") // the worker answers the first comment
	st.comments["pr1"] = append(st.comments["pr1"], reviewComment("c2", "T1", "still wrong on line 14"))
	reader.review.Threads = []ports.SCMReviewThreadObservation{openThread("T1", "c1", "c2")}
	observePR1(t, m)
	observePR1(t, m)
	if len(msg.msgs) != 2 {
		t.Fatalf("msgs = %v, want the original once and the reply once", msg.msgs)
	}
	if !strings.Contains(msg.msgs[1], "still wrong on line 14") || strings.Contains(msg.msgs[1], "fix this") {
		t.Fatalf("reply delivery re-sent the received comment:\n%s", msg.msgs[1])
	}
}

// The #103 replay: the worker answers and resolves a thread during its turn,
// then the comment observed mid-turn must not be handed back to it.
func TestReviewCommentDelivery_MidTurnCommentWaitsForTurnEndAndSkipsResolvedThread(t *testing.T) {
	m, st, msg, reader := newDeliveryFixture(domain.ActivityActive, openThread("T1", "c1"))
	observePR1(t, m)
	if len(msg.msgs) != 0 || reader.calls != 0 {
		t.Fatalf("mid-turn delivery: msgs=%v reads=%d", msg.msgs, reader.calls)
	}
	if !strings.Contains(st.signatures["pr1"], `"deferred"`) {
		t.Fatalf("deferral not persisted: %q", st.signatures["pr1"])
	}
	reader.review.Threads[0].Resolved = true // worker resolved it during the turn

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	m.deferredDeliveries.Wait()
	if len(msg.msgs) != 0 {
		t.Fatalf("resolved thread delivered at turn end: %v", msg.msgs)
	}
	if strings.Contains(st.signatures["pr1"], `"deferred"`) {
		t.Fatalf("handled deferral kept: %q", st.signatures["pr1"])
	}
}

func TestReviewCommentDelivery_MidTurnCommentIsDeliveredWhenTheTurnEnds(t *testing.T) {
	m, st, msg, _ := newDeliveryFixture(domain.ActivityActive, openThread("T1", "c1"))
	observePR1(t, m)
	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	m.deferredDeliveries.Wait()
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "fix this") {
		t.Fatalf("msgs = %v, want the deferred comment", msg.msgs)
	}
	if strings.Contains(st.signatures["pr1"], `"deferred"`) {
		t.Fatalf("delivered deferral kept: %q", st.signatures["pr1"])
	}
	// The next turn end has nothing left to deliver.
	rec := st.sessions["mer-1"]
	rec.Activity.State = domain.ActivityActive
	st.sessions["mer-1"] = rec
	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	m.deferredDeliveries.Wait()
	if len(msg.msgs) != 1 {
		t.Fatalf("second turn end re-delivered: %v", msg.msgs)
	}
}

func TestReviewCommentDelivery_DeferralSurvivesRestart(t *testing.T) {
	m, st, _, _ := newDeliveryFixture(domain.ActivityActive, openThread("T1", "c1"))
	observePR1(t, m)

	// A new process knows nothing in memory; startup restore re-arms the
	// worker, which is still mid-turn, so delivery waits for its turn end.
	messenger := &fakeMessenger{}
	restarted := New(st, messenger)
	restarted.SetReviewThreadReader(&fakeThreadReader{review: ports.SCMReviewObservation{Threads: []ports.SCMReviewThreadObservation{openThread("T1", "c1")}}})
	restarted.RestoreDeferredReviewComments(ctx, []domain.SessionID{"mer-1"})
	if len(messenger.msgs) != 0 {
		t.Fatalf("restore delivered to a worker mid-turn: %v", messenger.msgs)
	}
	endTurn(t, restarted, st, "mer-1")
	if len(messenger.msgs) != 1 {
		t.Fatalf("msgs after restart = %v, want the persisted deferral delivered", messenger.msgs)
	}
}

func TestReviewCommentDelivery_RestoreDeliversToAnIdleWorker(t *testing.T) {
	m, st, _, _ := newDeliveryFixture(domain.ActivityActive, openThread("T1", "c1"))
	observePR1(t, m)
	rec := st.sessions["mer-1"]
	rec.Activity.State = domain.ActivityIdle
	st.sessions["mer-1"] = rec

	messenger := &fakeMessenger{}
	restarted := New(st, messenger)
	restarted.RestoreDeferredReviewComments(ctx, []domain.SessionID{"mer-1"})
	if len(messenger.msgs) != 1 {
		t.Fatalf("msgs = %v, want the deferral delivered at restore", messenger.msgs)
	}
}

func TestReviewCommentDelivery_DeferralDroppedWhenStoredCommentResolves(t *testing.T) {
	m, st, msg, reader := newDeliveryFixture(domain.ActivityActive, openThread("T1", "c1"))
	observePR1(t, m)
	st.comments["pr1"][0].Resolved = true // the observer caught the resolution first
	rec := st.sessions["mer-1"]
	rec.Activity.State = domain.ActivityIdle
	st.sessions["mer-1"] = rec
	if err := m.DeliverDeferredReviewComments(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 || reader.calls != 0 {
		t.Fatalf("msgs=%v reads=%d, want nothing", msg.msgs, reader.calls)
	}
	if strings.Contains(st.signatures["pr1"], `"deferred"`) {
		t.Fatalf("stale deferral kept: %q", st.signatures["pr1"])
	}
}

// A send starts a turn, so the rest of the batch must wait for it: the chat
// queue would otherwise hold them unchecked behind it and replay a thread the
// worker resolves during that turn.
func TestReviewCommentDelivery_LaterCommentsWaitForTheTurnTheFirstSendStarts(t *testing.T) {
	m, st, msg, reader := newDeliveryFixture(domain.ActivityIdle, openThread("T1", "c1"), openThread("T2", "c2"), openThread("T3", "c3"))
	st.comments["pr1"] = []domain.PullRequestComment{
		reviewComment("c1", "T1", "first finding"),
		reviewComment("c2", "T2", "second finding"),
		reviewComment("c3", "T3", "third finding"),
	}
	observePR1(t, m)
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "first finding") {
		t.Fatalf("msgs = %v, want only the first comment before its turn ends", msg.msgs)
	}

	// During that turn the worker resolves T2; T3 stays open.
	reader.review.Threads[1].Resolved = true
	endTurn(t, m, st, "mer-1")
	if len(msg.msgs) != 2 || !strings.Contains(msg.msgs[1], "third finding") {
		t.Fatalf("msgs = %v, want the resolved T2 skipped and T3 delivered", msg.msgs)
	}
	endTurn(t, m, st, "mer-1")
	if len(msg.msgs) != 2 {
		t.Fatalf("msgs = %v, want nothing left after the last comment's turn", msg.msgs)
	}
	if strings.Contains(st.signatures["pr1"], `"deferred"`) {
		t.Fatalf("deferrals left behind: %q", st.signatures["pr1"])
	}
}

// A CI nudge in the same observation starts a turn too.
func TestReviewCommentDelivery_CommentWaitsForATurnACINudgeStarts(t *testing.T) {
	m, st, msg, reader := newDeliveryFixture(domain.ActivityIdle, openThread("T1", "c1"))
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{
		Fetched: true, URL: "pr1", Number: 7, CI: domain.CIFailing,
		Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "boom") {
		t.Fatalf("msgs = %v, want only the CI nudge", msg.msgs)
	}
	reader.review.Threads[0].Resolved = true
	endTurn(t, m, st, "mer-1")
	if len(msg.msgs) != 1 {
		t.Fatalf("comment resolved during the CI turn was delivered: %v", msg.msgs)
	}
}
