package lifecycle

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestPRObservation_CIFailingNudgesAgentWithLogs(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	o := ports.PRObservation{Fetched: true, URL: "pr1", CI: domain.CIFailing, Checks: []ports.PRCheckObservation{
		{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, URL: "https://ci.example/build", LogTail: "boom"},
		{Name: "lint", CommitHash: "c1", Status: domain.PRCheckCancelled, URL: "https://ci.example/lint"},
	}}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one CI nudge with log tail, got %v", msg.msgs)
	}
	for _, want := range []string{
		"CI is failing on your PR.",
		"Failed: build (failed)",
		"Failure URL: https://ci.example/build",
		"Log tail (last 1 line):",
		"boom",
		"fetch full CI logs only if you need additional context",
	} {
		if !strings.Contains(msg.msgs[0], want) {
			t.Fatalf("CI nudge missing %q:\n%s", want, msg.msgs[0])
		}
	}
	if strings.Contains(msg.msgs[0], "lint") || strings.Contains(msg.msgs[0], "cancelled") {
		t.Fatalf("cancelled checks must not be included in CI nudge:\n%s", msg.msgs[0])
	}
}

func TestPRObservation_CancelledChecksDoNotNudge(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	o := ports.PRObservation{Fetched: true, URL: "pr1", CI: domain.CIFailing, Checks: []ports.PRCheckObservation{
		{Name: "lint", CommitHash: "c1", Status: domain.PRCheckCancelled, URL: "https://ci.example/lint"},
	}}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("cancelled-only checks must not nudge, got %v", msg.msgs)
	}
}

func TestPRObservation_CIFailureNotInjectedWhenPRPolicyDisabled(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.prPolicies["pr1"] = false
	o := ports.PRObservation{Fetched: true, URL: "pr1", CI: domain.CIFailing, Checks: []ports.PRCheckObservation{
		{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"},
	}}

	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("CI failure was injected with PR policy disabled: %v", msg.msgs)
	}
}

func TestPRObservation_ReviewCommentsNudgeAgent(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = idleWorker("mer-1")
	st.comments["pr1"] = []domain.PullRequestComment{
		{ID: "1", ThreadID: "T1", Author: "alice", File: "foo.go", Line: 12, Body: "fix this", URL: "https://github.com/o/r/pull/1#discussion_r1", AutoInjectReview: true},
		{ID: "2", Author: "bob", Body: "already handled", Resolved: true, AutoInjectReview: true},
	}
	o := ports.PRObservation{Fetched: true, URL: "pr1", Review: domain.ReviewChangesRequest}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want review nudge, got %v", msg.msgs)
	}
	for _, want := range []string{
		"The following 1 unresolved review comment(s)",
		"foo.go:12 (@alice):",
		"fix this",
		"https://github.com/o/r/pull/1#discussion_r1",
		"Thread ID: T1",
		"re-fetch review data unless you need additional context",
	} {
		if !strings.Contains(msg.msgs[0], want) {
			t.Fatalf("review nudge missing %q:\n%s", want, msg.msgs[0])
		}
	}
	if strings.Contains(msg.msgs[0], "already handled") {
		t.Fatalf("review nudge included resolved comment:\n%s", msg.msgs[0])
	}
}

func TestPRObservation_AnchoredBotReviewNudgesAgent(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = idleWorker("mer-1")
	st.comments["pr1"] = []domain.PullRequestComment{
		{ID: "bot-1", ThreadID: "thread-1", Author: "react-doctor[bot]", IsBot: true, File: "src/App.tsx", Line: 42, Body: "avoid this pattern", AutoInjectReview: true},
		{ID: "bot-2", ThreadID: "thread-2", Author: "react-doctor[bot]", IsBot: true, Body: "summary chatter", AutoInjectReview: true},
	}

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1"}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one actionable bot nudge, got %v", msg.msgs)
	}
	if !strings.Contains(msg.msgs[0], "src/App.tsx:42 (@react-doctor[bot]):") || !strings.Contains(msg.msgs[0], "avoid this pattern") {
		t.Fatalf("anchored bot feedback missing from nudge: %q", msg.msgs[0])
	}
	if strings.Contains(msg.msgs[0], "summary chatter") {
		t.Fatalf("unanchored bot chatter was injected: %q", msg.msgs[0])
	}
}

func TestPRObservation_HumanReplyInBotThreadStillNudgesAgent(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = idleWorker("mer-1")
	st.comments["pr1"] = []domain.PullRequestComment{
		{ID: "bot-1", ThreadID: "thread-1", Author: "review-bot[bot]", IsBot: true, Body: "automated summary", AutoInjectReview: true},
		{ID: "human-1", ThreadID: "thread-1", Author: "alice", Body: "i agree, please fix this", AutoInjectReview: true},
	}

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1"}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "i agree, please fix this") {
		t.Fatalf("human reply was not delivered: %v", msg.msgs)
	}
	if strings.Contains(msg.msgs[0], "automated summary") {
		t.Fatalf("unanchored bot comment was injected with human reply: %q", msg.msgs[0])
	}
}

func TestPRObservation_ReviewFeedbackNotInjectedWhenDisabled(t *testing.T) {
	m, st, msg := newManager()
	rec := working("mer-1")
	st.sessions[rec.ID] = rec
	st.comments["pr1"] = []domain.PullRequestComment{{ID: "1", Author: "alice", Body: "fix this", AutoInjectReview: false}}
	st.reviews["pr1"] = []domain.PullRequestReview{{ID: "r1", Author: "alice", State: domain.ReviewChangesRequest, Body: "change this too", AutoInjectReview: false}}
	o := ports.PRObservation{
		Fetched: true,
		URL:     "pr1",
		CI:      domain.CIFailing,
		Checks:  []ports.PRCheckObservation{{Name: "build", Status: domain.PRCheckFailed, LogTail: "boom"}},
		Review:  domain.ReviewChangesRequest,
	}
	if err := m.ApplyPRObservation(ctx, rec.ID, o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "boom") || strings.Contains(msg.msgs[0], "fix this") {
		t.Fatalf("messages = %v, want CI only while review feedback is not injected", msg.msgs)
	}
}

func TestPRObservation_MixedPersistedCommentDecisions(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = idleWorker("mer-1")
	st.comments["pr1"] = []domain.PullRequestComment{
		{ID: "1", Author: "alice", Body: "captured while disabled", AutoInjectReview: false},
		{ID: "2", Author: "alice", Body: "captured while enabled", AutoInjectReview: true},
	}
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Review: domain.ReviewChangesRequest}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want only the injectable comment to nudge, got %v", msg.msgs)
	}
	if !strings.Contains(msg.msgs[0], "captured while enabled") || strings.Contains(msg.msgs[0], "captured while disabled") {
		t.Fatalf("nudge did not honor per-comment persisted decisions: %q", msg.msgs[0])
	}
}

func TestPRObservation_CIFailingAndReviewBothNudge(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = idleWorker("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", SessionID: "mer-1"}}
	st.comments["pr1"] = []domain.PullRequestComment{{ID: "1", Author: "alice", Body: "fix this", AutoInjectReview: true}}
	o := ports.PRObservation{
		Fetched: true,
		URL:     "pr1",
		CI:      domain.CIFailing,
		Checks:  []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
		Review:  domain.ReviewChangesRequest,
	}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	// Both actionable items fire — neither is suppressed by the other — in queue
	// order (CI first, then review). The CI nudge starts a turn, so the review
	// comment follows when that turn ends rather than queueing behind it.
	if len(msg.msgs) != 1 {
		t.Fatalf("want only the CI nudge before its turn ends, got %d: %v", len(msg.msgs), msg.msgs)
	}
	endTurn(t, m, st, "mer-1")
	if len(msg.msgs) != 2 {
		t.Fatalf("want CI and review nudges, got %d: %v", len(msg.msgs), msg.msgs)
	}
	if !strings.Contains(msg.msgs[0], "boom") {
		t.Fatalf("first nudge should carry the CI failure, got %q", msg.msgs[0])
	}
	if !strings.Contains(msg.msgs[1], "fix this") {
		t.Fatalf("second nudge should carry the review feedback, got %q", msg.msgs[1])
	}
	// Re-observing the identical state re-nudges nothing: per-item dedup is intact.
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("re-observation should not re-nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// A failed parent-stack lookup (the store read behind the merge-conflict
// exemption) must not discard the CI/review nudges already queued for the same
// PR. Only the merge-conflict nudge is skipped this cycle; CI and review still
// send, the read error still surfaces so the observer logs and re-polls, and the
// merge-conflict nudge fires once the read recovers.
func TestPRObservation_MergeConflictReadErrorStillSendsCIAndReview(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = idleWorker("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", SessionID: "mer-1"}}
	st.comments["pr1"] = []domain.PullRequestComment{{ID: "1", Author: "alice", Body: "fix this", AutoInjectReview: true}}
	st.listPRsErr = errors.New("transient store read failure")
	o := ports.PRObservation{
		Fetched:      true,
		URL:          "pr1",
		CI:           domain.CIFailing,
		Checks:       []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
		Review:       domain.ReviewChangesRequest,
		Mergeability: domain.MergeConflicting,
	}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err == nil {
		t.Fatal("want the deferred parent-stack read error surfaced")
	}
	// CI and review still fired despite the merge-conflict lookup failing; the
	// merge-conflict nudge itself is skipped this cycle. The review comment
	// waits for the CI nudge's turn to end.
	st.listPRsErr = nil
	endTurn(t, m, st, "mer-1")
	st.listPRsErr = errors.New("transient store read failure")
	if len(msg.msgs) != 2 {
		t.Fatalf("want CI and review nudges sent, got %d: %v", len(msg.msgs), msg.msgs)
	}
	if !strings.Contains(msg.msgs[0], "boom") {
		t.Fatalf("first nudge should carry the CI failure, got %q", msg.msgs[0])
	}
	if !strings.Contains(msg.msgs[1], "fix this") {
		t.Fatalf("second nudge should carry the review feedback, got %q", msg.msgs[1])
	}
	for _, sent := range msg.msgs {
		if strings.Contains(sent, "merge conflicts") {
			t.Fatalf("merge-conflict nudge must be skipped on read error, got %q", sent)
		}
	}

	// Next poll, the read recovers: the merge-conflict nudge now fires while
	// CI/review stay deduped (their signatures persisted before the error).
	st.listPRsErr = nil
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 3 {
		t.Fatalf("want merge-conflict nudge on recovery with CI/review deduped, got %d: %v", len(msg.msgs), msg.msgs)
	}
	if !strings.Contains(msg.msgs[2], "merge conflicts") {
		t.Fatalf("third nudge should be the merge-conflict nudge, got %q", msg.msgs[2])
	}
}

func TestPRObservation_CINudgeSanitizesLogTailControlChars(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	// A CI log tail with an embedded ANSI escape sequence and a NUL byte; the
	// agent's pane must receive the visible text without the control bytes.
	o := ports.PRObservation{Fetched: true, URL: "pr1", CI: domain.CIFailing, Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "line1\x1b[2Jline2\x00\ttabbed"}}}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one CI nudge, got %v", msg.msgs)
	}
	got := msg.msgs[0]
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x00') {
		t.Fatalf("nudge still carries control bytes: %q", got)
	}
	if !strings.Contains(got, "line1") || !strings.Contains(got, "line2") || !strings.Contains(got, "\ttabbed") {
		t.Fatalf("nudge dropped visible text or tab: %q", got)
	}
}

func TestPRObservation_ReviewNudgeSanitizesCommentControlChars(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = idleWorker("mer-1")
	st.comments["pr1"] = []domain.PullRequestComment{{ID: "1", Body: "please\x1b]0;pwned\afix this", AutoInjectReview: true}}
	o := ports.PRObservation{Fetched: true, URL: "pr1", Review: domain.ReviewChangesRequest}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one review nudge, got %v", msg.msgs)
	}
	got := msg.msgs[0]
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\a') {
		t.Fatalf("review nudge still carries control bytes: %q", got)
	}
	if !strings.Contains(got, "please") || !strings.Contains(got, "fix this") {
		t.Fatalf("review nudge dropped visible text: %q", got)
	}
}

func TestPRObservation_ChangesRequestedReviewUsesPersistedBodyAndDecision(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.reviews["pr1"] = []domain.PullRequestReview{
		{ID: "r1", Author: "alice\x1b]0;pwned\a", State: domain.ReviewChangesRequest, URL: "https://github.com/o/r/pull/1#pullrequestreview-1", Body: "please\x1b[2Jfix this", AutoInjectReview: true},
		{ID: "r2", Author: "bob", State: domain.ReviewApproved, Body: "approved", AutoInjectReview: true},
		{ID: "r3", Author: "carol", State: domain.ReviewChangesRequest, Body: "do not inject", AutoInjectReview: false},
	}
	o := ports.PRObservation{Fetched: true, URL: "pr1", Review: domain.ReviewChangesRequest}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one persisted review nudge, got %v", msg.msgs)
	}
	got := msg.msgs[0]
	for _, want := range []string{"@alice", "please", "fix this", "https://github.com/o/r/pull/1#pullrequestreview-1", "Review ID: r1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("review nudge missing %q:\n%s", want, got)
		}
	}
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\a') {
		t.Fatalf("review nudge still carries control bytes: %q", got)
	}
	if strings.Contains(got, "approved") || strings.Contains(got, "do not inject") {
		t.Fatalf("review nudge included an ineligible persisted review: %q", got)
	}
}

func TestPRObservation_MergeConflictNudgesAgent(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	o := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "merge conflicts") {
		t.Fatalf("want merge-conflict nudge, got %v", msg.msgs)
	}
}

// TestPRObservation_MergeConflictReArmsAfterConflictClears is the regression
// test for #4528: Tallpine notified on the first conflict but never again, because
// the "merge-conflict:<url>" = "conflicting" signature sendOnce persists was
// never cleared. A PR rebased clean and then made conflicting again by a
// base-branch advance was silently swallowed as a duplicate.
func TestPRObservation_MergeConflictReArmsAfterConflictClears(t *testing.T) {
	// Every state that definitively rules a conflict out must re-arm; `blocked`
	// is deliberately excluded because the observer synthesizes it from
	// draft/CI/review facts even while provider mergeability is unknown.
	for _, state := range []domain.Mergeability{domain.MergeMergeable, domain.MergeUnstable} {
		t.Run(string(state), func(t *testing.T) {
			m, st, msg := newManager()
			st.sessions["mer-1"] = working("mer-1")
			apply := func(state domain.Mergeability) {
				t.Helper()
				if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: state}); err != nil {
					t.Fatal(err)
				}
			}

			apply(state)
			apply(domain.MergeConflicting)
			if len(msg.msgs) != 1 {
				t.Fatalf("first conflict should nudge once, got %v", msg.msgs)
			}
			apply(state)
			apply(domain.MergeConflicting)
			if len(msg.msgs) != 2 {
				t.Fatalf("conflict returning after %s should nudge again, got %d nudges: %v", state, len(msg.msgs), msg.msgs)
			}
		})
	}
}

// TestPRObservation_ConsecutiveMergeConflictsStayDeduplicated pins the other
// half of #4528: the re-arm must not weaken the dedup that stops an unchanged
// conflict from re-nudging on every poll.
func TestPRObservation_ConsecutiveMergeConflictsStayDeduplicated(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	for range 3 {
		if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}); err != nil {
			t.Fatal(err)
		}
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("identical consecutive conflicts should nudge once, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// TestPRObservation_UnknownMergeabilityDoesNotReArm keeps the re-arm off the
// transient GitHub reports while it recomputes mergeability after a push or a
// retarget. Treating unknown as "conflict resolved" would re-nudge on every
// unknown → conflicting flap of a conflict that never went away.
func TestPRObservation_UnknownMergeabilityDoesNotReArm(t *testing.T) {
	for _, transient := range []domain.Mergeability{domain.MergeUnknown, domain.MergeBlocked, ""} {
		t.Run(string(transient), func(t *testing.T) {
			m, st, msg := newManager()
			st.sessions["mer-1"] = working("mer-1")
			for _, state := range []domain.Mergeability{domain.MergeConflicting, transient, domain.MergeConflicting} {
				if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: state}); err != nil {
					t.Fatal(err)
				}
			}
			if len(msg.msgs) != 1 {
				t.Fatalf("%q must not re-arm the conflict nudge, got %d nudges: %v", transient, len(msg.msgs), msg.msgs)
			}
		})
	}
}

// TestPRObservation_MergeConflictDedupSurvivesRestart covers both persistence
// directions of #4528 across a daemon restart, simulated by a fresh Manager
// over the same store: an unchanged conflict must stay quiet, and a conflict
// that cleared before the restart must be free to nudge again.
func TestPRObservation_MergeConflictDedupSurvivesRestart(t *testing.T) {
	conflicting := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}

	t.Run("unchanged conflict stays deduplicated", func(t *testing.T) {
		st := newFakeStore()
		st.sessions["mer-1"] = working("mer-1")
		first := New(st, &fakeMessenger{})
		if err := first.ApplyPRObservation(ctx, "mer-1", conflicting); err != nil {
			t.Fatal(err)
		}

		restarted := &fakeMessenger{}
		if err := New(st, restarted).ApplyPRObservation(ctx, "mer-1", conflicting); err != nil {
			t.Fatal(err)
		}
		if len(restarted.msgs) != 0 {
			t.Fatalf("restart must not replay an already-sent conflict nudge, got %v", restarted.msgs)
		}
	})

	t.Run("cleared conflict re-arms across restart", func(t *testing.T) {
		st := newFakeStore()
		st.sessions["mer-1"] = working("mer-1")
		first := New(st, &fakeMessenger{})
		if err := first.ApplyPRObservation(ctx, "mer-1", conflicting); err != nil {
			t.Fatal(err)
		}
		if err := first.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeMergeable}); err != nil {
			t.Fatal(err)
		}

		restarted := &fakeMessenger{}
		if err := New(st, restarted).ApplyPRObservation(ctx, "mer-1", conflicting); err != nil {
			t.Fatal(err)
		}
		if len(restarted.msgs) != 1 {
			t.Fatalf("a conflict returning after a restart should nudge, got %v", restarted.msgs)
		}
	})
}

// TestPRObservation_ReArmLeavesOtherDedupEntriesIntact guards the blast radius
// of the re-arm: it must delete only this PR's merge-conflict key, so a
// resolved conflict cannot replay the CI-failure nudge the same PR row holds.
func TestPRObservation_ReArmLeavesOtherDedupEntriesIntact(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	o := ports.PRObservation{
		Fetched:      true,
		URL:          "pr1",
		CI:           domain.CIFailing,
		Checks:       []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
		Mergeability: domain.MergeConflicting,
	}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("want a CI and a merge-conflict nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}

	o.Mergeability = domain.MergeMergeable
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("re-arming the conflict must not replay the unchanged CI nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// TestPRObservation_ReArmSkipsStoreWriteWhenNothingArmed keeps a steadily
// mergeable PR off the write path: the re-arm persists only when it actually
// cleared an entry, so routine polls do not rewrite an unchanged payload.
func TestPRObservation_ReArmSkipsStoreWriteWhenNothingArmed(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	for range 2 {
		if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeMergeable}); err != nil {
			t.Fatal(err)
		}
	}
	if st.signatureWrites != 0 {
		t.Fatalf("mergeable polls with nothing armed wrote the signature payload %d times, want 0", st.signatureWrites)
	}
}

// TestPRObservation_ReArmRetriesAfterFailedPersist covers the failure ->
// successful observation -> restart -> conflict path. The re-arm's in-memory
// delete stands even when the durable write fails, so a naive implementation
// would see nothing armed on the next mergeable observation, take the early
// return, and never retry — leaving the stale "conflicting" signature on disk
// to suppress the nudge after a restart, recreating #4528.
func TestPRObservation_ReArmRetriesAfterFailedPersist(t *testing.T) {
	conflicting := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}
	mergeable := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeMergeable}

	st := newFakeStore()
	st.sessions["mer-1"] = working("mer-1")
	m := New(st, &fakeMessenger{})
	if err := m.ApplyPRObservation(ctx, "mer-1", conflicting); err != nil {
		t.Fatal(err)
	}

	st.signatureWriteErr = errors.New("db locked")
	if err := m.ApplyPRObservation(ctx, "mer-1", mergeable); err == nil {
		t.Fatal("a failed re-arm persist should surface to the observer")
	}

	// Persistence recovers. The next non-conflicting observation must retry the
	// durable write rather than short-circuit on the now-empty in-memory maps.
	st.signatureWriteErr = nil
	if err := m.ApplyPRObservation(ctx, "mer-1", mergeable); err != nil {
		t.Fatal(err)
	}

	restarted := &fakeMessenger{}
	if err := New(st, restarted).ApplyPRObservation(ctx, "mer-1", conflicting); err != nil {
		t.Fatal(err)
	}
	if len(restarted.msgs) != 1 {
		t.Fatalf("a re-arm that retried its persist must survive a restart, got %v", restarted.msgs)
	}
}

// TestPRObservation_ReArmStillNudgesInProcessAfterFailedPersist pins the other
// half of the retry contract: the failed persist must not roll the in-memory
// re-arm back, or a conflict returning before the write succeeds would be
// deduplicated away inside the running daemon.
func TestPRObservation_ReArmStillNudgesInProcessAfterFailedPersist(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	conflicting := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}
	if err := m.ApplyPRObservation(ctx, "mer-1", conflicting); err != nil {
		t.Fatal(err)
	}

	st.signatureWriteErr = errors.New("db locked")
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeMergeable}); err == nil {
		t.Fatal("a failed re-arm persist should surface to the observer")
	}
	st.signatureWriteErr = nil

	if err := m.ApplyPRObservation(ctx, "mer-1", conflicting); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("a conflict returning after a failed re-arm persist should still nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// TestPRObservation_ReArmStoreFailureSurfacesWithoutLosingNudges checks the
// deferred-error contract: a failed re-arm persist is reported, but only after
// the independent CI nudge queued in the same observation has been sent.
func TestPRObservation_ReArmStoreFailureSurfacesWithoutLosingNudges(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}); err != nil {
		t.Fatal(err)
	}

	st.signatureWriteErr = errors.New("db locked")
	err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{
		Fetched:      true,
		URL:          "pr1",
		CI:           domain.CIFailing,
		Checks:       []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
		Mergeability: domain.MergeMergeable,
	})
	if err == nil {
		t.Fatal("a failed re-arm persist should surface to the observer")
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("the CI nudge must still be sent before the deferred re-arm error, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

func TestPRObservation_ExitedAgentIsNotNudged(t *testing.T) {
	m, st, msg := newManager()
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityExited
	st.sessions["mer-1"] = rec

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("exited agent should not receive reaction nudges, got %v", msg.msgs)
	}
}

// TestPRObservation_ReArmClearsConflictWhileSessionExited is the regression
// test for the delivery-gate finding: a definitively-cleared observation must
// re-arm the merge-conflict dedup even when it arrives while the session is
// exited. Exited sessions are still polled, so the sequence
// conflicting -> exit -> mergeable -> restore -> conflicting must nudge on the
// final conflict; a re-arm placed behind the dead-session return would skip the
// clear and let the stale "conflicting" signature swallow the recurrence.
func TestPRObservation_ReArmClearsConflictWhileSessionExited(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	conflicting := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}
	mergeable := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeMergeable}

	if err := m.ApplyPRObservation(ctx, "mer-1", conflicting); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("first conflict should nudge, got %v", msg.msgs)
	}

	// The agent exits, then the PR is observed mergeable while exited. No nudge
	// is delivered (nowhere to write), but the re-arm bookkeeping must still run.
	exited := working("mer-1")
	exited.Activity.State = domain.ActivityExited
	st.sessions["mer-1"] = exited
	if err := m.ApplyPRObservation(ctx, "mer-1", mergeable); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("mergeable-while-exited must deliver nothing, got %v", msg.msgs)
	}

	// The agent is restored and the base advances into a fresh conflict. The
	// re-arm during the exited window must let this recurrence nudge again.
	st.sessions["mer-1"] = working("mer-1")
	if err := m.ApplyPRObservation(ctx, "mer-1", conflicting); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("a conflict returning after a clear-while-exited must nudge again, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// TestPRObservation_ReArmClearsConflictSurvivesTerminatedRestore covers the
// terminated variant of the same hazard: a terminated session is excluded from
// polling, so the clear typically arrives on the first observation after
// restore. The re-arm still runs ahead of the dead-session return, so even a
// clear observed in the terminated window (before restore is reflected) drops
// the stale signature and a later conflict nudges again.
func TestPRObservation_ReArmClearsConflictSurvivesTerminatedRestore(t *testing.T) {
	conflicting := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}
	mergeable := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeMergeable}

	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	if err := m.ApplyPRObservation(ctx, "mer-1", conflicting); err != nil {
		t.Fatal(err)
	}

	// Session terminates; a mergeable observation lands before restore. It must
	// re-arm despite the terminated gate returning before any delivery.
	terminated := working("mer-1")
	terminated.IsTerminated = true
	st.sessions["mer-1"] = terminated
	if err := m.ApplyPRObservation(ctx, "mer-1", mergeable); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("mergeable-while-terminated must deliver nothing, got %v", msg.msgs)
	}

	// Restored, then a fresh conflict: the re-arm during the terminated window
	// must let it nudge again rather than dedup against the stale signature.
	st.sessions["mer-1"] = working("mer-1")
	if err := m.ApplyPRObservation(ctx, "mer-1", conflicting); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("a conflict after a clear-while-terminated must nudge again, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// TestPRObservation_MergeConflictReachesNeedsInputSession is the regression
// test for #2173: a session parked awaiting the human (waiting_input) must
// still receive a merge-conflict nudge, because the human sitting at that
// prompt may be exactly who needs to act (rebase it themselves, or redirect
// the agent) — unlike CI-failure/review-feedback nudges, which only the agent
// can act on and which correctly wait for it to resume (see the sibling test
// below). Two states are still refused: a session blocked on a live permission
// dialog, and a waiting_input session on a harness that cannot prove that
// prompt is a genuine idle composer rather than a masked permission decision
// (codex maps permission-request to waiting_input) — the harness-aware gate the
// urgent route consults, so an unsolicited paste never answers a hidden dialog.
func TestPRObservation_MergeConflictReachesNeedsInputSession(t *testing.T) {
	const safeHarness = domain.AgentHarness("claude-code")
	const ambiguousHarness = domain.AgentHarness("codex")
	urgentGate := func(h domain.AgentHarness) bool { return h == safeHarness }
	cases := []struct {
		name      string
		state     domain.ActivityState
		harness   domain.AgentHarness
		wantNudge bool
	}{
		{"waiting_input on a blocked-signalling harness reaches the agent", domain.ActivityWaitingInput, safeHarness, true},
		{"waiting_input on an ambiguous harness stays suppressed", domain.ActivityWaitingInput, ambiguousHarness, false},
		{"blocked stays suppressed", domain.ActivityBlocked, safeHarness, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, st, msg := newManager(WithUrgentNudgeGate(urgentGate))
			rec := working("mer-1")
			rec.Activity.State = tc.state
			rec.Harness = tc.harness
			st.sessions["mer-1"] = rec

			o := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}
			if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
				t.Fatal(err)
			}
			gotNudge := len(msg.msgs) == 1
			if gotNudge != tc.wantNudge {
				t.Fatalf("nudge sent = %v (msgs=%v), want %v", gotNudge, msg.msgs, tc.wantNudge)
			}
			if gotNudge && !strings.Contains(msg.msgs[0], "merge conflicts") {
				t.Fatalf("want merge-conflict nudge, got %q", msg.msgs[0])
			}
		})
	}
}

// TestPRObservation_NeedsInputSessionStillWithholdsOtherNudges is the
// regression guard: the needs-input gate must keep suppressing CI-failure and
// review-feedback nudges exactly as before. Only the merge-conflict nudge
// gets the carve-out in TestPRObservation_MergeConflictReachesNeedsInputSession.
func TestPRObservation_NeedsInputSessionStillWithholdsOtherNudges(t *testing.T) {
	for _, state := range []domain.ActivityState{domain.ActivityWaitingInput, domain.ActivityBlocked} {
		t.Run(string(state), func(t *testing.T) {
			m, st, msg := newManager()
			rec := working("mer-1")
			rec.Activity.State = state
			st.sessions["mer-1"] = rec
			st.comments["pr1"] = []domain.PullRequestComment{{ID: "1", Author: "alice", Body: "fix this", AutoInjectReview: true}}

			o := ports.PRObservation{
				Fetched: true,
				URL:     "pr1",
				CI:      domain.CIFailing,
				Checks:  []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
				Review:  domain.ReviewChangesRequest,
			}
			if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
				t.Fatal(err)
			}
			if len(msg.msgs) != 0 {
				t.Fatalf("needs-input session should not receive CI/review nudges, got %v", msg.msgs)
			}
		})
	}
}

// TestPRObservation_DeadSessionGetsNoNudgesOfAnyKind sanity-checks that the
// merge-conflict carve-out did not overreach: a genuinely dead session
// (terminated, or its pane already exited to a shell) still gets nothing at
// all, conflict included, since there is nowhere to deliver it.
func TestPRObservation_DeadSessionGetsNoNudgesOfAnyKind(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*domain.SessionRecord)
	}{
		{"terminated", func(r *domain.SessionRecord) { r.IsTerminated = true }},
		{"exited", func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityExited }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, st, msg := newManager()
			rec := working("mer-1")
			tc.mut(&rec)
			st.sessions["mer-1"] = rec
			st.comments["pr1"] = []domain.PullRequestComment{{ID: "1", Author: "alice", Body: "fix this", AutoInjectReview: true}}

			o := ports.PRObservation{
				Fetched:      true,
				URL:          "pr1",
				CI:           domain.CIFailing,
				Checks:       []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
				Review:       domain.ReviewChangesRequest,
				Mergeability: domain.MergeConflicting,
			}
			if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
				t.Fatal(err)
			}
			if len(msg.msgs) != 0 {
				t.Fatalf("%s session should receive no nudges at all, including for a conflict, got %v", tc.name, msg.msgs)
			}
		})
	}
}

func TestPRObservation_NudgeIncludesPRIdentity(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	o := ports.PRObservation{
		Fetched:      true,
		URL:          "https://github.com/o/r/pull/7",
		Number:       7,
		Title:        "Add auth",
		SourceBranch: "feat/x/auth",
		TargetBranch: "feat/x",
		CI:           domain.CIFailing,
		Checks:       []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
	}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one CI nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
	got := msg.msgs[0]
	if !strings.Contains(got, `PR #7 "Add auth" (feat/x/auth → feat/x)`) {
		t.Fatalf("nudge missing PR identity: %q", got)
	}
	if !strings.Contains(got, "PR: https://github.com/o/r/pull/7") {
		t.Fatalf("nudge missing PR URL: %q", got)
	}
}

func TestPRObservation_MergedStaysLiveWhenAutoTerminateDisabled(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Merged: true}}
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if got.IsTerminated || got.Activity.State == domain.ActivityExited {
		t.Fatalf("merged PR should stay live when auto-terminate is disabled, got %+v", got)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("merged PR should not send nudge, got %v", msg.msgs)
	}
}

func TestPRObservation_MergedUsesConfiguredTerminator(t *testing.T) {
	m, st, _ := newManager()
	terminator := &fakeCompletionTerminator{}
	m.SetCompletionTerminator(terminator)
	rec := exited("mer-1")
	rec.TerminateOnPRMerge = true
	st.sessions["mer-1"] = rec
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Merged: true}}

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
	if terminator.calls != 1 {
		t.Fatalf("terminator calls = %d, want 1", terminator.calls)
	}
}

// TestPRObservation_MergedOnStillWorkingAgentDoesNotTerminate is the RED test
// for #2879: an agent STILL CLIMBING (ActivityActive / `working`) with one PR
// merged must NOT be flag-terminated. The merge may be PR #1 of several and
// the agent is mid-traversal — likely about to push PR #2 in the same session.
// Flag-terminating here (is_terminated=true, #2811) drops the session from the
// SCM observer roster, so that follow-up PR is never attributed, never
// enriched, never nudged. Termination must wait until the agent has actually
// quiesced (ActivityExited or provably-idle). This test intentionally fails on
// current code, which terminates on PR-state alone (≥1 merged ∧ none open).
func TestPRObservation_MergedOnStillWorkingAgentDoesNotTerminate(t *testing.T) {
	m, st, _ := newManager()
	terminator := &fakeCompletionTerminator{}
	m.SetCompletionTerminator(terminator)
	rec := working("mer-1") // ActivityActive: agent is STILL climbing (#2879)
	rec.TerminateOnPRMerge = true
	st.sessions["mer-1"] = rec
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Merged: true}}

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatalf("merged PR must NOT terminate a session whose agent is still ActivityActive (working), got %+v", st.sessions["mer-1"])
	}
	if terminator.calls != 0 {
		t.Fatalf("terminator calls = %d, want 0 while the agent is still working", terminator.calls)
	}
}

func TestPRObservation_MergedTerminationIsSuppressedDuringSessionMutation(t *testing.T) {
	m, st, _ := newManager()
	terminator := &fakeCompletionTerminator{}
	m.SetCompletionTerminator(terminator)
	m.SetSessionOperationGate(fixedSessionOperationGate(true))
	rec := working("mer-1")
	rec.TerminateOnPRMerge = true
	st.sessions["mer-1"] = rec
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Merged: true}}

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
	if terminator.calls != 0 {
		t.Fatalf("terminator calls = %d, want 0 during session mutation", terminator.calls)
	}
}

func TestPRObservation_MergedTeardownFailureStaysLiveForRetry(t *testing.T) {
	m, st, _ := newManager()
	terminator := &fakeCompletionTerminator{err: errors.New("transient teardown failure")}
	m.SetCompletionTerminator(terminator)
	rec := exited("mer-1")
	rec.TerminateOnPRMerge = true
	st.sessions["mer-1"] = rec
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Merged: true}}

	err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true})
	if err == nil || !strings.Contains(err.Error(), "transient teardown failure") {
		t.Fatalf("ApplyPRObservation err = %v, want teardown failure", err)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatalf("failed teardown must not hide a live session: %+v", st.sessions["mer-1"])
	}
}

func TestPRObservation_MergedRequiresConfiguredTerminator(t *testing.T) {
	m, st, _ := newManager()
	rec := exited("mer-1")
	rec.TerminateOnPRMerge = true
	st.sessions["mer-1"] = rec
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Merged: true}}

	err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("ApplyPRObservation err = %v, want configuration error", err)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatalf("missing terminator must not mark the session terminated: %+v", st.sessions["mer-1"])
	}
}

// A session with one merged PR and one still-open PR must NOT terminate: the
// completion bar is "no open PR remains AND at least one merged".
func TestPRObservation_MergedWithOpenSiblingDoesNotTerminate(t *testing.T) {
	m, st, _ := newManager()
	terminator := &fakeCompletionTerminator{}
	m.SetCompletionTerminator(terminator)
	rec := working("mer-1")
	rec.TerminateOnPRMerge = true
	st.sessions["mer-1"] = rec
	st.prs["mer-1"] = []domain.PullRequest{
		{URL: "pr1", Merged: true},
		{URL: "pr2"},
	}
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"]; got.IsTerminated {
		t.Fatalf("session with an open sibling PR must stay alive, got %+v", got)
	}
	if terminator.calls != 0 {
		t.Fatalf("terminator calls = %d, want 0", terminator.calls)
	}
}

// Once the last open PR merges (all PRs now merged), the session terminates.
func TestPRObservation_LastMergeTerminatesSession(t *testing.T) {
	m, st, _ := newManager()
	terminator := &fakeCompletionTerminator{}
	m.SetCompletionTerminator(terminator)
	rec := exited("mer-1")
	rec.TerminateOnPRMerge = true
	st.sessions["mer-1"] = rec
	st.prs["mer-1"] = []domain.PullRequest{
		{URL: "pr1", Merged: true},
		{URL: "pr2", Merged: true},
	}
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr2", Merged: true}); err != nil {
		t.Fatal(err)
	}
	if terminator.calls != 1 {
		t.Fatalf("terminator calls = %d, want 1", terminator.calls)
	}
}

// A closed PR that leaves the session with an open sibling and no merge does not
// terminate; closing the last PR with no merge also does not terminate (nothing
// shipped).
func TestPRObservation_ClosedWithoutMergeDoesNotTerminate(t *testing.T) {
	m, st, _ := newManager()
	terminator := &fakeCompletionTerminator{}
	m.SetCompletionTerminator(terminator)
	rec := working("mer-1")
	rec.TerminateOnPRMerge = true
	st.sessions["mer-1"] = rec
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Closed: true}}
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Closed: true}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"]; got.IsTerminated {
		t.Fatalf("a closed-without-merge PR must not terminate the session, got %+v", got)
	}
	if terminator.calls != 0 {
		t.Fatalf("terminator calls = %d, want 0", terminator.calls)
	}
}

// A PR stacked on an open parent (its target branch is the parent's source
// branch) is exempt from the merge-conflict nudge: conflicts there are expected
// until the parent merges.
func TestPRObservation_StackedChildConflictSuppressed(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{
		{URL: "parent", SourceBranch: "tp/x", TargetBranch: "main"},
		{URL: "child", SourceBranch: "tp/x/auth", TargetBranch: "tp/x"},
	}
	o := ports.PRObservation{Fetched: true, URL: "child", Mergeability: domain.MergeConflicting}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("stacked child conflict should be suppressed, got %v", msg.msgs)
	}
}

// The bottom-of-stack PR (not stacked on any open parent) still gets the
// merge-conflict nudge even when it has open stacked children.
func TestPRObservation_BottomOfStackConflictNudges(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{
		{URL: "parent", SourceBranch: "tp/x", TargetBranch: "main"},
		{URL: "child", SourceBranch: "tp/x/auth", TargetBranch: "tp/x"},
	}
	o := ports.PRObservation{Fetched: true, URL: "parent", Mergeability: domain.MergeConflicting}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "merge conflicts") {
		t.Fatalf("bottom-of-stack conflict should nudge, got %v", msg.msgs)
	}
}

// TestPRObservation_DedupSurvivesManagerRestart simulates a daemon restart by
// constructing a second Manager over the same store and asserts that an
// identical PR observation does not re-fire the nudge — the dedup signature
// must survive process restart, not just live in the Manager's maps.
func TestPRObservation_DedupSurvivesManagerRestart(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = working("mer-1")

	o := ports.PRObservation{
		Fetched: true,
		URL:     "https://github.com/o/r/pull/1",
		CI:      domain.CIFailing,
		Checks:  []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
	}

	first := &fakeMessenger{}
	m1 := New(st, first)
	if err := m1.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatalf("first ApplyPRObservation: %v", err)
	}
	if len(first.msgs) != 1 {
		t.Fatalf("first manager: want 1 nudge, got %d", len(first.msgs))
	}
	if got := st.signatures[o.URL]; got == "" {
		t.Fatalf("signature was not persisted; want a non-empty JSON payload for %q", o.URL)
	}

	// Simulate daemon restart: the second Manager has no in-memory state but
	// shares the same store, so it should hydrate seen/attempts from the
	// persisted payload and suppress the re-send.
	second := &fakeMessenger{}
	m2 := New(st, second)
	if err := m2.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatalf("second ApplyPRObservation: %v", err)
	}
	if len(second.msgs) != 0 {
		t.Fatalf("post-restart manager re-nudged on identical observation, got %d msgs: %v", len(second.msgs), second.msgs)
	}

	// And a genuinely new signature (different log tail) still fires — proving
	// the persisted state is per-signature, not a blanket "this PR was nudged".
	o2 := o
	o2.Checks = []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "different boom"}}
	if err := m2.ApplyPRObservation(ctx, "mer-1", o2); err != nil {
		t.Fatalf("third ApplyPRObservation: %v", err)
	}
	if len(second.msgs) != 1 {
		t.Fatalf("new signature should send, got %d msgs", len(second.msgs))
	}
}

func TestPRObservation_DedupPersistsAcrossPRs(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = working("mer-1")
	msg := &fakeMessenger{}
	m := New(st, msg)

	for _, url := range []string{"https://github.com/o/r/pull/1", "https://github.com/o/r/pull/2"} {
		o := ports.PRObservation{
			Fetched: true, URL: url, CI: domain.CIFailing,
			Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
		}
		if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
			t.Fatalf("ApplyPRObservation for %s: %v", url, err)
		}
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("distinct PRs should each get one nudge, got %d", len(msg.msgs))
	}
	if _, ok := st.signatures["https://github.com/o/r/pull/1"]; !ok {
		t.Fatal("missing persisted signature for PR 1")
	}
	if _, ok := st.signatures["https://github.com/o/r/pull/2"]; !ok {
		t.Fatal("missing persisted signature for PR 2")
	}
}

func TestPRObservation_RetriesAfterMessengerFailure(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	o := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}
	msg.err = errors.New("temporary send failure")
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err == nil {
		t.Fatal("want send error")
	}
	msg.err = nil
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want retry to send once, got %v", msg.msgs)
	}
}

func TestPRObservation_NudgesSuppressedWhileBlocked(t *testing.T) {
	// A blocked session must not receive automated CI/review nudges: injected
	// text could interact with the pending permission dialog.
	m, st, msg := newManager()
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityBlocked
	st.sessions["mer-1"] = rec
	o := ports.PRObservation{Fetched: true, URL: "pr1", CI: domain.CIFailing, Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}}}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("blocked session got nudged: %v", msg.msgs)
	}
}

// Each unresolved comment gets its own dedup slot. Sharing one key per PR made
// every poll re-send whichever comments were not the most recent signature
// written, and made them share the reviewMaxNudge budget so a PR with more
// comments than that could never deliver the last of them.
func TestPRObservation_ReviewCommentNudgesDedupPerComment(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = idleWorker("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", SessionID: "mer-1"}}
	comments := make([]domain.PullRequestComment, 0, reviewMaxNudge+2)
	for i := range reviewMaxNudge + 2 {
		id := fmt.Sprintf("%d", i+1)
		// Every comment shares one thread: the observer expands a thread into
		// one row per comment, so this is the routine shape whenever a worker
		// replies to a review comment without resolving it. Keying on the
		// thread would collapse them all back into one dedup slot.
		comments = append(comments, domain.PullRequestComment{
			ID: id, ThreadID: "T1", Author: "alice", File: "foo.go", Line: i + 1,
			Body: "finding " + id, AutoInjectReview: true,
		})
	}
	st.comments["pr1"] = comments
	o := ports.PRObservation{Fetched: true, URL: "pr1", Review: domain.ReviewChangesRequest}

	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	// One comment per turn: each later one waits for the turn the previous
	// delivery started, then is re-checked.
	for range comments {
		endTurn(t, m, st, "mer-1")
	}
	if len(msg.msgs) != len(comments) {
		t.Fatalf("sent %d nudges, want one per comment (%d)", len(msg.msgs), len(comments))
	}
	for _, c := range comments {
		if !slices.ContainsFunc(msg.msgs, func(m string) bool { return strings.Contains(m, "finding "+c.ID) }) {
			t.Fatalf("comment %s never nudged; the attempt budget is shared", c.ID)
		}
	}

	sent := len(msg.msgs)
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	endTurn(t, m, st, "mer-1")
	if len(msg.msgs) != sent {
		t.Fatalf("second poll re-sent %d nudges for unchanged comments:\n%v",
			len(msg.msgs)-sent, msg.msgs[sent:])
	}
}
