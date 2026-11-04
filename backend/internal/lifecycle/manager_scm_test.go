package lifecycle

import (
	"reflect"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestReviewCommentsSignatureUsesStableIDs(t *testing.T) {
	original := []ports.PRCommentObservation{
		{ID: "c1", ThreadID: "t1", Author: "alice", File: "old.go", Line: 10, Body: "old", URL: "https://old"},
		{ID: "c2", ThreadID: "t2", Author: "bob", File: "old.go", Line: 20, Body: "old", URL: "https://old"},
	}
	editedAndReordered := []ports.PRCommentObservation{
		{ID: "c2", ThreadID: "t2", Author: "bob", File: "new.go", Line: 99, Body: "edited", URL: "https://new"},
		{ID: "c1", ThreadID: "t1", Author: "alice", File: "new.go", Line: 42, Body: "edited", URL: "https://new"},
	}
	if got, want := reviewCommentsSignature(editedAndReordered), reviewCommentsSignature(original); got != want {
		t.Fatalf("signature changed after edit/reorder\n got %q\nwant %q", got, want)
	}

	withNewComment := append([]ports.PRCommentObservation(nil), original...)
	withNewComment = append(withNewComment, ports.PRCommentObservation{ID: "c3", ThreadID: "t2", Body: "new comment in same thread"})
	if got, old := reviewCommentsSignature(withNewComment), reviewCommentsSignature(original); got == old {
		t.Fatalf("new comment id should change signature, got %q", got)
	}
}

func TestFormatCIFailureMessageUsesNonMutatingFence(t *testing.T) {
	logTail := "start\n```\ninner\n````\nend"
	msg := formatCIFailureMessage([]ports.PRCheckObservation{{
		Name: "build", Status: domain.PRCheckFailed, LogTail: logTail,
	}})
	if !strings.Contains(msg, logTail) {
		t.Fatalf("message should preserve log text without zero-width mutation:\n%s", msg)
	}
	if strings.Contains(msg, "\u200b") {
		t.Fatalf("message must not insert zero-width characters:\n%s", msg)
	}
	if !strings.Contains(msg, "`````\n"+logTail+"\n`````") {
		t.Fatalf("message should wrap log in a fence longer than embedded runs:\n%s", msg)
	}
}

func TestSCMObservationProjectsToExistingPRReactions(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	o := ports.SCMObservation{
		Fetched: true,
		PR:      ports.SCMPRObservation{URL: "pr1", Number: 1},
		CI: ports.SCMCIObservation{
			Summary: string(domain.CIFailing),
			HeadSHA: "c1",
			FailedChecks: []ports.SCMCheckObservation{{
				Name: "build", Status: string(domain.PRCheckFailed), LogTail: "boom",
			}},
		},
	}
	if err := m.ApplySCMObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "boom") {
		t.Fatalf("want SCM CI nudge with log tail, got %v", msg.msgs)
	}
}

func TestSCMObservation_MissingSessionIsIgnored(t *testing.T) {
	st := newFakeStore()
	m := New(st, nil)
	o := ports.SCMObservation{
		Fetched: true,
		PR:      ports.SCMPRObservation{URL: "pr1", Number: 1},
	}
	if err := m.ApplySCMObservation(ctx, "missing-1", o); err != nil {
		t.Fatalf("ApplySCMObservation missing session: %v", err)
	}
}

func TestSCMObservationUsesPRHeadWhenCIHeadMissing(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	o := ports.SCMObservation{
		Fetched: true,
		PR:      ports.SCMPRObservation{URL: "pr1", HeadSHA: "c1"},
		CI: ports.SCMCIObservation{
			Summary: string(domain.CIFailing),
			FailedChecks: []ports.SCMCheckObservation{{
				Name: "build", Status: string(domain.PRCheckFailed),
			}},
		},
	}
	if err := m.ApplySCMObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	o.PR.HeadSHA = "c2"
	if err := m.ApplySCMObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("want separate CI nudges for distinct PR heads when CI head is absent, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// A noCI project's PR without Tallpine approval is held as "blocked" even when the
// provider reports it mergeable. That hold must still re-arm the
// merge-conflict nudge, or a conflict that returns after a clean rebase is
// swallowed as a duplicate (#4528). A plain synthesized "blocked" still must
// not re-arm.
func TestSCMObservation_NoCIHeldMergeableReArmsMergeConflict(t *testing.T) {
	for _, tt := range []struct {
		name       string
		clear      func() ports.SCMMergeabilityObservation
		wantNudges int
	}{
		{"noCI hold over provider mergeable", func() ports.SCMMergeabilityObservation {
			obs := ports.ApplyNoCIPolicy(ports.SCMObservation{
				Fetched:      true,
				CI:           ports.SCMCIObservation{Summary: string(domain.CIUnknown), NoChecks: true},
				Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
			}, true, domain.TallpineReviewGateMissing)
			return obs.Mergeability
		}, 2},
		{"synthesized blocked without the hold", func() ports.SCMMergeabilityObservation {
			return ports.SCMMergeabilityObservation{State: string(domain.MergeBlocked), Blockers: []string{"ci_failing"}}
		}, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, st, msg := newManager()
			st.sessions["mer-1"] = working("mer-1")
			apply := func(mergeability ports.SCMMergeabilityObservation) {
				t.Helper()
				obs := ports.SCMObservation{
					Fetched:      true,
					PR:           ports.SCMPRObservation{URL: "pr1", Number: 1},
					CI:           ports.SCMCIObservation{Summary: string(domain.CIUnknown)},
					Mergeability: mergeability,
				}
				if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
					t.Fatal(err)
				}
			}
			conflicting := ports.SCMMergeabilityObservation{State: string(domain.MergeConflicting), Conflict: true}
			apply(conflicting)
			apply(tt.clear())
			apply(conflicting)
			if len(msg.msgs) != tt.wantNudges {
				t.Fatalf("merge-conflict nudges = %d, want %d: %v", len(msg.msgs), tt.wantNudges, msg.msgs)
			}
		})
	}
}

func TestApplyTrackerFacts_TerminalStateMarksTerminated(t *testing.T) {
	for _, state := range []domain.NormalizedIssueState{domain.IssueDone, domain.IssueCancelled} {
		t.Run(string(state), func(t *testing.T) {
			m, st, msg := newManager()
			st.sessions["mer-1"] = working("mer-1")
			o := ports.TrackerObservation{
				Fetched: true,
				Issue:   ports.TrackerIssueObservation{URL: "https://github.com/o/r/issues/1", State: state},
			}
			if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
				t.Fatalf("ApplyTrackerFacts: %v", err)
			}
			got := st.sessions["mer-1"]
			if !got.IsTerminated || got.Activity.State != domain.ActivityExited {
				t.Fatalf("want terminated/exited for state %q, got %+v", state, got)
			}
			if len(msg.msgs) != 0 {
				t.Fatalf("terminal state should not nudge, got %v", msg.msgs)
			}
		})
	}
}

func TestApplyTrackerFacts_TerminalStateIsSuppressedDuringSessionMutation(t *testing.T) {
	m, st, _ := newManager()
	m.SetSessionOperationGate(fixedSessionOperationGate(true))
	rec := working("mer-1")
	st.sessions["mer-1"] = rec
	o := ports.TrackerObservation{
		Fetched: true,
		Issue:   ports.TrackerIssueObservation{URL: "https://github.com/o/r/issues/1", State: domain.IssueDone},
	}

	if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
		t.Fatalf("ApplyTrackerFacts: %v", err)
	}
	if got := st.sessions["mer-1"]; !reflect.DeepEqual(got, rec) {
		t.Fatalf("tracker observation mutated session during exclusive operation: got %+v, want %+v", got, rec)
	}
}

func TestLifecycleNudgeUsesLateBoundSessionInputLease(t *testing.T) {
	m, st, msg := newManager()
	m.SetSessionInputLease(fixedLifecycleInputLease(false))
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", Activity: domain.Activity{State: domain.ActivityIdle}}

	outcome, err := m.sendOnce(ctx, "mer-1", "", "tracker-comment:1", "1", "review this", 0, nudgeRoutine, nil)
	if err != nil {
		t.Fatalf("sendOnce: %v", err)
	}
	if outcome != sendOnceSuppressed {
		t.Fatalf("sendOnce outcome = %v, want suppressed", outcome)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("lifecycle nudge bypassed closed input lease: %v", msg.msgs)
	}
}

func TestLifecycleNudgeStartupGateUsesAdapterCapability(t *testing.T) {
	tests := []struct {
		name     string
		harness  domain.AgentHarness
		gate     bool
		wantSent bool
	}{
		{name: "startup-signaling adapter is gated", harness: domain.AgentHarness("cursor"), gate: true, wantSent: false},
		{name: "hookless adapter remains deliverable", harness: domain.AgentHarness("aider"), gate: false, wantSent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			msg := &fakeMessenger{}
			m := New(st, msg, WithStartupSignalGate(func(harness domain.AgentHarness) bool {
				return harness == tt.harness && tt.gate
			}))
			st.sessions["mer-1"] = domain.SessionRecord{
				ID: "mer-1", Harness: tt.harness, Mode: domain.SessionModeTUI,
				Activity: domain.Activity{State: domain.ActivityIdle},
			}

			outcome, err := m.sendOnce(ctx, "mer-1", "", "tracker-comment:1", "1", "review this", 0, nudgeRoutine, nil)
			if err != nil {
				t.Fatalf("sendOnce: %v", err)
			}
			if got := len(msg.msgs) == 1; got != tt.wantSent {
				t.Fatalf("sent = %v, want %v (outcome %v)", got, tt.wantSent, outcome)
			}
		})
	}
}

func TestApplyTrackerFacts_AssigneeChangedIsLogOnly(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	before := st.sessions["mer-1"]
	o := ports.TrackerObservation{
		Fetched: true,
		Issue:   ports.TrackerIssueObservation{URL: "https://github.com/o/r/issues/1", State: domain.IssueOpen, Assignee: "someone-else"},
		Changed: ports.TrackerChanged{Assignee: true},
	}
	if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
		t.Fatalf("ApplyTrackerFacts: %v", err)
	}
	if !reflect.DeepEqual(st.sessions["mer-1"], before) {
		t.Fatalf("assignee-only change must not mutate the session row, got %+v", st.sessions["mer-1"])
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("assignee-only change must not nudge, got %v", msg.msgs)
	}
}

func TestApplyTrackerFacts_NewBotCommentNudges(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	o := ports.TrackerObservation{
		Fetched: true,
		Issue:   ports.TrackerIssueObservation{URL: "https://github.com/o/r/issues/1", State: domain.IssueOpen},
		Comments: []ports.TrackerCommentObservation{
			{ID: "human-1", Author: "alice", Body: "human chime-in, must NOT nudge", IsBot: false},
			{ID: "bot-1", Author: "ci-bot[bot]", Body: "please rerun the migration", IsBot: true},
		},
		Changed: ports.TrackerChanged{Comments: true},
	}
	if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
		t.Fatalf("ApplyTrackerFacts: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one bot-mention nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
	if !strings.Contains(msg.msgs[0], "please rerun the migration") {
		t.Fatalf("nudge should include the bot comment body, got %q", msg.msgs[0])
	}
	if strings.Contains(msg.msgs[0], "human chime-in") {
		t.Fatalf("nudge must not include human comments, got %q", msg.msgs[0])
	}
}

func TestApplyTrackerFacts_NudgeSuppressedOnRepeat(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	o := ports.TrackerObservation{
		Fetched: true,
		Issue:   ports.TrackerIssueObservation{URL: "https://github.com/o/r/issues/1", State: domain.IssueOpen},
		Comments: []ports.TrackerCommentObservation{
			{ID: "bot-1", Author: "ci-bot[bot]", Body: "please rerun the migration", IsBot: true},
		},
		Changed: ports.TrackerChanged{Comments: true},
	}
	if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
		t.Fatalf("first ApplyTrackerFacts: %v", err)
	}
	if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
		t.Fatalf("second ApplyTrackerFacts: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("repeat observation must dedup; got %d nudges: %v", len(msg.msgs), msg.msgs)
	}

	// A genuinely new bot comment still fires.
	o.Comments = append(o.Comments, ports.TrackerCommentObservation{ID: "bot-2", Author: "ci-bot[bot]", Body: "now check the seed", IsBot: true})
	if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
		t.Fatalf("third ApplyTrackerFacts: %v", err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("new bot comment id should re-fire, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

func TestApplyTrackerFacts_BotCommentWithEmptyIDIsIgnored(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	// Bot comment lacks an ID — without one we cannot dedup, and the
	// zero-value signature collides with m.react.seen's empty default and
	// would silently suppress every future nudge for this issue. The
	// reducer must skip it entirely.
	o := ports.TrackerObservation{
		Fetched: true,
		Issue:   ports.TrackerIssueObservation{URL: "https://github.com/o/r/issues/1", State: domain.IssueOpen},
		Comments: []ports.TrackerCommentObservation{
			{ID: "", Author: "ci-bot[bot]", Body: "no id, must be skipped", IsBot: true},
		},
		Changed: ports.TrackerChanged{Comments: true},
	}
	if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
		t.Fatalf("ApplyTrackerFacts: %v", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("bot comment with empty ID must not nudge, got %v", msg.msgs)
	}
	// A subsequent, properly-formed bot comment must still nudge — the
	// earlier empty-ID entry must not have polluted the dedup signature.
	o.Comments = []ports.TrackerCommentObservation{
		{ID: "bot-1", Author: "ci-bot[bot]", Body: "now with an id", IsBot: true},
	}
	if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
		t.Fatalf("second ApplyTrackerFacts: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("follow-up bot comment with real ID should nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

func TestApplyTrackerFacts_NotFetchedIsNoop(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	before := st.sessions["mer-1"]
	if err := m.ApplyTrackerFacts(ctx, "mer-1", ports.TrackerObservation{Fetched: false}); err != nil {
		t.Fatalf("ApplyTrackerFacts: %v", err)
	}
	if !reflect.DeepEqual(st.sessions["mer-1"], before) {
		t.Fatalf("not-fetched observation must not mutate state")
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("not-fetched observation must not nudge")
	}
}

func TestApplyTrackerFacts_TerminatedSessionDoesNotRefireOrNudge(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true, Activity: domain.Activity{State: domain.ActivityExited}}
	o := ports.TrackerObservation{
		Fetched: true,
		Issue:   ports.TrackerIssueObservation{URL: "https://github.com/o/r/issues/1", State: domain.IssueOpen},
		Comments: []ports.TrackerCommentObservation{
			{ID: "bot-1", Body: "x", IsBot: true},
		},
		Changed: ports.TrackerChanged{Comments: true},
	}
	if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
		t.Fatalf("ApplyTrackerFacts: %v", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("terminated session must not receive nudges, got %v", msg.msgs)
	}
}

func TestSCMObservation_ReadyToMergeSuppressedWhileBlocked(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityBlocked
	st.sessions["mer-1"] = rec
	obs := ports.SCMObservation{
		Fetched:      true,
		PR:           ports.SCMPRObservation{URL: "https://github.com/o/r/pull/1", Number: 1},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)},
	}
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(sink.intents) != 0 {
		t.Fatalf("blocked session emitted ready notification: %+v", sink.intents)
	}
}

func TestSendOnce_NoNudgeWhenBlockedAppearsBeforeSend(t *testing.T) {
	// The entry guard in ApplyPRObservation reads the session working (read #1);
	// a permission dialog then stores blocked before sendOnce's just-in-time
	// re-read (read #2), which must suppress the paste+Enter into the dialog.
	st := newFakeStore()
	st.sessions["mer-1"] = working("mer-1")
	bst := &blockOnNthGetStore{fakeStore: st, id: "mer-1", flipAt: 2}
	msg := &fakeMessenger{}
	m := New(bst, msg)
	o := ports.PRObservation{Fetched: true, URL: "pr1", CI: domain.CIFailing, Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}}}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("nudge sent into a session that went blocked before send: %v", msg.msgs)
	}
}

func TestSCMObservation_Notifications(t *testing.T) {
	for _, tc := range []struct {
		name string
		obs  ports.SCMObservation
		want domain.NotificationType
	}{
		{
			name: "ready",
			obs:  ports.SCMObservation{Fetched: true, PR: ports.SCMPRObservation{URL: "https://github.com/o/r/pull/1", Number: 1, Title: "checkout"}, CI: ports.SCMCIObservation{Summary: string(domain.CIPassing)}, Review: ports.SCMReviewObservation{Decision: string(domain.ReviewApproved)}, Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)}},
			want: domain.NotificationReadyToMerge,
		},
		{
			name: "merged",
			obs:  ports.SCMObservation{Fetched: true, PR: ports.SCMPRObservation{URL: "https://github.com/o/r/pull/2", Number: 2, Merged: true}},
			want: domain.NotificationPRMerged,
		},
		{
			name: "closed",
			obs:  ports.SCMObservation{Fetched: true, PR: ports.SCMPRObservation{URL: "https://github.com/o/r/pull/3", Number: 3, Closed: true}},
			want: domain.NotificationPRClosedUnmerged,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			sink := &fakeNotificationSink{}
			m := New(st, nil, WithNotificationSink(sink))
			st.sessions["mer-1"] = working("mer-1")
			if err := m.ApplySCMObservation(ctx, "mer-1", tc.obs); err != nil {
				t.Fatal(err)
			}
			if len(sink.intents) != 1 {
				t.Fatalf("intents = %d, want 1", len(sink.intents))
			}
			if got := sink.intents[0]; got.Type != tc.want || got.PRURL != tc.obs.PR.URL || got.PRNumber != tc.obs.PR.Number {
				t.Fatalf("intent = %+v, want type %s", got, tc.want)
			}
		})
	}
}

func TestSCMObservation_AnchoredBotReviewSuppressesReadyNotification(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	st.sessions["mer-1"] = working("mer-1")
	obs := ports.SCMObservation{
		Fetched: true,
		PR:      ports.SCMPRObservation{URL: "https://github.com/o/r/pull/1", Number: 1},
		CI:      ports.SCMCIObservation{Summary: string(domain.CIPassing)},
		Review: ports.SCMReviewObservation{
			Decision: string(domain.ReviewNone),
			Threads: []ports.SCMReviewThreadObservation{{
				ID: "thread-1", Path: "src/App.tsx", Line: 42, IsBot: true,
				Comments: []ports.SCMReviewCommentObservation{{ID: "bot-1", Author: "react-doctor[bot]", IsBot: true, Body: "avoid this pattern"}},
			}},
		},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)},
	}

	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(sink.intents) != 0 {
		t.Fatalf("ready notification raced actionable bot feedback: %+v", sink.intents)
	}
}

func TestSCMObservation_PersistedAnchoredBotReviewSuppressesReadyNotification(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	st.sessions["mer-1"] = working("mer-1")
	prURL := "https://github.com/o/r/pull/1"
	st.comments[prURL] = []domain.PullRequestComment{{
		ID: "bot-1", ThreadID: "thread-1", Author: "react-doctor[bot]", IsBot: true,
		File: "src/App.tsx", Line: 42, Body: "avoid this pattern", AutoInjectReview: true,
	}}
	obs := ports.SCMObservation{
		Fetched:      true,
		PR:           ports.SCMPRObservation{URL: prURL, Number: 1},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing)},
		Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewApproved)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)},
	}

	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(sink.intents) != 0 {
		t.Fatalf("ready notification ignored persisted actionable bot feedback: %+v", sink.intents)
	}
	if len(sink.resolutions) != 1 || sink.resolutions[0].Type != domain.NotificationReadyToMerge {
		t.Fatalf("ready notification was not resolved from persisted actionable bot feedback: %+v", sink.resolutions)
	}
}

func TestSCMObservation_AnchoredBotReviewResolvesExistingReadyNotification(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	st.sessions["mer-1"] = working("mer-1")
	prURL := "https://github.com/o/r/pull/1"
	ready := ports.SCMObservation{
		Fetched: true,
		PR:      ports.SCMPRObservation{URL: prURL, Number: 1},
		CI:      ports.SCMCIObservation{Summary: string(domain.CIPassing)},
		Review:  ports.SCMReviewObservation{Decision: string(domain.ReviewApproved)},
		Mergeability: ports.SCMMergeabilityObservation{
			State: string(domain.MergeMergeable),
		},
	}
	if err := m.ApplySCMObservation(ctx, "mer-1", ready); err != nil {
		t.Fatal(err)
	}
	if len(sink.intents) != 1 || sink.intents[0].Type != domain.NotificationReadyToMerge {
		t.Fatalf("initial intents = %+v, want one ready-to-merge notification", sink.intents)
	}

	st.comments[prURL] = []domain.PullRequestComment{{
		ID: "bot-1", ThreadID: "thread-1", Author: "react-doctor[bot]", IsBot: true,
		File: "src/App.tsx", Line: 42, Body: "avoid this pattern", AutoInjectReview: true,
	}}
	blocked := ready
	blocked.Review = ports.SCMReviewObservation{
		Decision: string(domain.ReviewApproved),
		Threads: []ports.SCMReviewThreadObservation{{
			ID: "thread-1", Path: "src/App.tsx", Line: 42, IsBot: true,
			Comments: []ports.SCMReviewCommentObservation{{ID: "bot-1", Author: "react-doctor[bot]", IsBot: true, Body: "avoid this pattern"}},
		}},
	}
	if err := m.ApplySCMObservation(ctx, "mer-1", blocked); err != nil {
		t.Fatal(err)
	}
	if len(sink.intents) != 1 {
		t.Fatalf("bot feedback emitted a competing intent: %+v", sink.intents)
	}
	if len(sink.resolutions) != 1 {
		t.Fatalf("resolutions = %+v, want existing ready notification resolved", sink.resolutions)
	}
	got := sink.resolutions[0]
	if got.Type != domain.NotificationReadyToMerge || got.SessionID != "mer-1" || got.PRURL != prURL {
		t.Fatalf("resolution = %+v", got)
	}
}

func TestSCMObservation_AnchoredBotReviewNudgesAgent(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = idleWorker("mer-1")
	st.comments["pr1"] = []domain.PullRequestComment{{
		ID: "bot-1", ThreadID: "thread-1", Author: "react-doctor[bot]", IsBot: true,
		File: "src/App.tsx", Line: 42, Body: "avoid this pattern", AutoInjectReview: true,
	}}
	obs := ports.SCMObservation{
		Fetched: true,
		PR:      ports.SCMPRObservation{URL: "pr1"},
		Review: ports.SCMReviewObservation{Threads: []ports.SCMReviewThreadObservation{{
			ID: "thread-1", Path: "src/App.tsx", Line: 42, IsBot: true,
			Comments: []ports.SCMReviewCommentObservation{{ID: "bot-1", Author: "react-doctor[bot]", IsBot: true, Body: "avoid this pattern"}},
		}}},
	}

	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "src/App.tsx:42") || !strings.Contains(msg.msgs[0], "avoid this pattern") {
		t.Fatalf("anchored bot feedback was not delivered through scm lifecycle: %v", msg.msgs)
	}
}

// Merging the PR is what resolves a ready-to-merge ping. So is the PR ceasing
// to be mergeable — either way there is nothing left for the user to merge.
func TestSCMObservation_ResolvesReadyToMergeWhenNoLongerReady(t *testing.T) {
	ready := ports.SCMObservation{
		Fetched:      true,
		PR:           ports.SCMPRObservation{URL: "https://github.com/o/r/pull/1", Number: 1},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing)},
		Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewApproved)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)},
	}
	merged := ready
	merged.PR.Merged = true
	ciBroke := ready
	ciBroke.CI.Summary = string(domain.CIFailing)

	for _, tt := range []struct {
		name string
		obs  ports.SCMObservation
		want int
	}{
		{name: "still ready", obs: ready, want: 0},
		{name: "merged", obs: merged, want: 1},
		{name: "ci went red", obs: ciBroke, want: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			sink := &fakeNotificationSink{}
			m := New(st, nil, WithNotificationSink(sink))
			st.sessions["mer-1"] = working("mer-1")

			if err := m.ApplySCMObservation(ctx, "mer-1", tt.obs); err != nil {
				t.Fatal(err)
			}
			if len(sink.resolutions) != tt.want {
				t.Fatalf("resolutions = %+v, want %d", sink.resolutions, tt.want)
			}
			if tt.want == 0 {
				return
			}
			got := sink.resolutions[0]
			if got.Type != domain.NotificationReadyToMerge || got.PRURL != tt.obs.PR.URL {
				t.Fatalf("resolution = %+v", got)
			}
		})
	}
}

func TestSCMObservation_NotReadyWhenCIOrReviewBlocks(t *testing.T) {
	for _, obs := range []ports.SCMObservation{
		{Fetched: true, PR: ports.SCMPRObservation{URL: "https://github.com/o/r/pull/1", Number: 1}, CI: ports.SCMCIObservation{Summary: string(domain.CIFailing)}, Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)}},
		{Fetched: true, PR: ports.SCMPRObservation{URL: "https://github.com/o/r/pull/1", Number: 1}, CI: ports.SCMCIObservation{Summary: string(domain.CIPending)}, Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)}},
		{Fetched: true, PR: ports.SCMPRObservation{URL: "https://github.com/o/r/pull/1", Number: 1}, CI: ports.SCMCIObservation{Summary: string(domain.CIUnknown)}, Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)}},
		{Fetched: true, PR: ports.SCMPRObservation{URL: "https://github.com/o/r/pull/1", Number: 1}, Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)}},
		{Fetched: true, PR: ports.SCMPRObservation{URL: "https://github.com/o/r/pull/1", Number: 1}, CI: ports.SCMCIObservation{Summary: string(domain.CIPassing)}, Review: ports.SCMReviewObservation{Decision: string(domain.ReviewChangesRequest)}, Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)}},
	} {
		st := newFakeStore()
		sink := &fakeNotificationSink{}
		m := New(st, nil, WithNotificationSink(sink))
		st.sessions["mer-1"] = working("mer-1")
		if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
			t.Fatal(err)
		}
		if len(sink.intents) != 0 {
			t.Fatalf("blocked PR emitted %+v", sink.intents)
		}
	}
}

func TestSCMObservation_ReadyToMergeSuppressedWhileWaitingInput(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityWaitingInput
	st.sessions["mer-1"] = rec
	obs := ports.SCMObservation{
		Fetched:      true,
		PR:           ports.SCMPRObservation{URL: "https://github.com/o/r/pull/1", Number: 1},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)},
	}
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(sink.intents) != 0 {
		t.Fatalf("waiting-input session emitted ready notification: %+v", sink.intents)
	}
}

// A noCI project's PR with no provider checks reaches lifecycle exactly as the
// SCM observer prepares it: ApplyNoCIPolicy with the head's review gate. With
// GitHub reviewDecision "none" (no branch protection) and no Tallpine approval of
// the current head, lifecycle must not announce ready_to_merge.
func TestSCMObservation_NoCIUnreviewedPRIsNotReadyToMerge(t *testing.T) {
	const (
		prURL = "https://github.com/o/r/pull/1"
		head  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		older = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	approvedAt := func(sha string) []domain.ReviewRun {
		return []domain.ReviewRun{{ID: "r1", SessionID: "mer-1", Harness: "claude-code", PRURL: prURL, TargetSHA: sha, Status: domain.ReviewRunDelivered, Verdict: domain.VerdictApproved}}
	}
	tests := []struct {
		name      string
		runs      []domain.ReviewRun
		decision  domain.ReviewDecision
		wantReady bool
	}{
		{"review decision none, no Tallpine approval", nil, domain.ReviewNone, false},
		{"Tallpine approval of an older head", approvedAt(older), domain.ReviewNone, false},
		{"provider APPROVED (stale) without Tallpine approval", nil, domain.ReviewApproved, false},
		{"Tallpine approval of the current head", approvedAt(head), domain.ReviewNone, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			sink := &fakeNotificationSink{}
			m := New(st, nil, WithNotificationSink(sink))
			st.sessions["mer-1"] = working("mer-1")
			obs := ports.SCMObservation{
				Fetched:      true,
				PR:           ports.SCMPRObservation{URL: prURL, Number: 1, HeadSHA: head},
				CI:           ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: head, NoChecks: true},
				Review:       ports.SCMReviewObservation{Decision: string(tt.decision)},
				Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
			}
			gate := domain.TallpineReviewGateForHead(tt.runs, prURL, head)
			obs = ports.ApplyNoCIPolicy(obs, true, gate)
			if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
				t.Fatal(err)
			}
			ready := false
			for _, intent := range sink.intents {
				if intent.Type == domain.NotificationReadyToMerge {
					ready = true
				}
			}
			if ready != tt.wantReady {
				t.Fatalf("ready_to_merge emitted = %v, want %v (CI %q, intents %+v)", ready, tt.wantReady, obs.CI.Summary, sink.intents)
			}
		})
	}
}
