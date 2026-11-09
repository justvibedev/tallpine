package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B394 (was B393): a worker's reviewer is busy on PR #1 when an agent asks to
// rerun PR #2. That used to be a 409; the request is queued so it starts once
// the reviewer is free, and nothing is created now.
func TestAgentRerunQueuesWhenReviewerBusyOnAnotherHead(t *testing.T) {
	busy := requestedRun("run-1", domain.ReviewerClaudeCode, domain.ReviewRunRunning, domain.VerdictNone, 1)
	busy.PRURL, busy.TargetSHA = "https://github.com/o/r/pull/1", "busysha"
	done := requestedRun("run-0", domain.ReviewerClaudeCode, domain.ReviewRunComplete, domain.VerdictChangesRequested, 0)
	done.PRURL, done.TargetSHA = "https://github.com/o/r/pull/2", "oldsha"
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-claude-code", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "claude-pane"},
		},
		runs: []domain.ReviewRun{done, busy},
	}
	prs := fakePRs{prs: []domain.PullRequest{{URL: "https://github.com/o/r/pull/2", Number: 2, HeadSHA: "newsha"}}}
	qs := &queueStore{fakeStore: store}
	eng := newEngineForTest(qs, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{}, &fakeLauncher{alive: true})

	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, Rerun: true, Config: domain.AgentConfig{Model: "m-1", Effort: "high"}, InterfaceMode: domain.ReviewerInterfaceTUI})
	if err != nil {
		t.Fatalf("err = %v, want the rerun queued", err)
	}
	if !res.Queued || res.Created {
		t.Fatalf("result = %+v, want queued and not created", res)
	}
	if len(qs.queued) != 1 || qs.queued[0].PRURL != "https://github.com/o/r/pull/2" || qs.queued[0].Harness != domain.ReviewerClaudeCode {
		t.Fatalf("queue = %+v, want PR #2 for claude-code", qs.queued)
	}
	if got := qs.queued[0]; got.Model != "m-1" || got.Effort != "high" || got.InterfaceMode != domain.ReviewerInterfaceTUI {
		t.Fatalf("queued options = %+v, want --model, --effort and the surface kept", got)
	}
}

// A pass already running on this very head is a true duplicate: still a 409.
func TestAgentRerunOnTheRunningHeadIsStillRejected(t *testing.T) {
	busy := requestedRun("run-1", domain.ReviewerClaudeCode, domain.ReviewRunRunning, domain.VerdictNone, 1)
	busy.PRURL, busy.TargetSHA = "https://github.com/o/r/pull/1", "samesha"
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-claude-code", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "claude-pane"},
		},
		runs: []domain.ReviewRun{busy},
	}
	prs := fakePRs{prs: []domain.PullRequest{{URL: "https://github.com/o/r/pull/1", Number: 1, HeadSHA: "samesha"}}}
	qs := &queueStore{fakeStore: store}
	eng := newEngineForTest(qs, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{}, &fakeLauncher{alive: true})

	_, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, Rerun: true})
	if !errors.Is(err, ErrReviewAlreadyRunning) || !strings.Contains(err.Error(), "already reviewing PR #1") {
		t.Fatalf("err = %v, want ErrReviewAlreadyRunning naming PR #1", err)
	}
	if len(qs.queued) != 0 {
		t.Fatalf("queue = %+v, want nothing queued", qs.queued)
	}
}

// A rerun that waited in the queue has nothing to do once the reviewer ran the
// head after the request was queued.
func TestQueuedRerunIsSatisfiedByALaterPassOnTheHead(t *testing.T) {
	state := PRReviewState{PRURL: "https://github.com/o/r/pull/2", TargetSHA: "newsha", Status: ReviewStateNeedsReview}
	since := time.Unix(100, 0).UTC()
	run := func(status domain.ReviewRunStatus, at int64) []domain.ReviewRun {
		return []domain.ReviewRun{{PRURL: state.PRURL, TargetSHA: "newsha", Harness: domain.ReviewerCodex, Status: status, CreatedAt: time.Unix(at, 0).UTC()}}
	}
	for _, tc := range []struct {
		name string
		runs []domain.ReviewRun
		due  bool
	}{
		{"reviewed after queueing", run(domain.ReviewRunComplete, 200), false},
		{"reviewed before queueing", run(domain.ReviewRunComplete, 50), true},
		{"failed after queueing", run(domain.ReviewRunFailed, 200), true},
		{"cancelled after queueing", run(domain.ReviewRunCancelled, 200), true},
	} {
		opts := TriggerOptions{Rerun: true, Source: domain.ReviewTriggerAgent, QueuedSince: since}
		if got := prDueForTrigger(state, tc.runs, domain.ReviewerCodex, domain.ReviewTriggerAgent, opts, false, false); got != tc.due {
			t.Fatalf("%s: due = %v, want %v", tc.name, got, tc.due)
		}
	}
}

// `review ls` shows queued passes and `review cancel` withdraws them.
func TestQueuedPassesAreListedAndCancelWithdrawsThem(t *testing.T) {
	store := &fakeStore{review: &domain.Review{ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode}}
	qs := &queueStore{fakeStore: store, queued: []domain.QueuedReviewTrigger{
		{SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", Harness: domain.ReviewerClaudeCode},
		{SessionID: "other", PRURL: "https://github.com/o/r/pull/9", Harness: domain.ReviewerClaudeCode},
	}}
	eng := newEngineForTest(qs, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, &fakeLauncher{alive: true})
	got, err := eng.List(context.Background(), "mer-1")
	if err != nil || len(got.Queued) != 1 || got.Queued[0].PRURL != "https://github.com/o/r/pull/1" {
		t.Fatalf("list queued = %+v err=%v, want only this worker's entry", got.Queued, err)
	}
	if _, err := eng.Cancel(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(qs.queued) != 1 || qs.queued[0].SessionID != "other" {
		t.Fatalf("queue after cancel = %+v, want only the other worker's entry", qs.queued)
	}
}

// B394 auto path: a pass cancelled because its reviewer vanished must not block
// the head for the auto policy forever, but it still counts toward the retry
// limit so a reviewer that dies every time cannot loop.
func TestReviewerGoneCancelDoesNotBlockAutoReviewButIsBounded(t *testing.T) {
	const pr, sha = "https://github.com/o/r/pull/1", "sha1"
	gone := func(i int) domain.ReviewRun {
		return domain.ReviewRun{ID: fmt.Sprint("g", i), PRURL: pr, TargetSHA: sha, Harness: domain.ReviewerCodex, Status: domain.ReviewRunCancelled, Body: reviewerTerminalUnavailableReason}
	}
	if autoReviewHeadBlocked([]domain.ReviewRun{gone(1)}, pr, sha, domain.ReviewerCodex) {
		t.Fatal("one reviewer-gone cancel blocks the head: the retry of #839/#844 could never start")
	}
	if !autoReviewHeadBlocked([]domain.ReviewRun{gone(1), gone(2), gone(3)}, pr, sha, domain.ReviewerCodex) {
		t.Fatal("three reviewer-gone cancels do not block the head: a dying reviewer would loop")
	}
	byPerson := domain.ReviewRun{ID: "c", PRURL: pr, TargetSHA: sha, Harness: domain.ReviewerCodex, Status: domain.ReviewRunCancelled, Body: "cancelled by user"}
	if !autoReviewHeadBlocked([]domain.ReviewRun{byPerson}, pr, sha, domain.ReviewerCodex) {
		t.Fatal("a cancel by a person no longer blocks the head")
	}
}

func TestAutoTriggerReconcilesDeadReviewerBeforeRunningGate(t *testing.T) {
	pr1 := "https://github.com/o/r/pull/1"
	pr2 := "https://github.com/o/r/pull/2"
	store := &fakeStore{
		review: &domain.Review{ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-mer-1"},
		runs: []domain.ReviewRun{{
			ID: "run-1", ReviewID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode,
			PRURL: pr1, TargetSHA: "sha1", Status: domain.ReviewRunRunning,
		}},
	}
	launcher := &fakeLauncher{alive: false, handle: "review-mer-1"}
	prs := fakePRs{prs: []domain.PullRequest{
		{URL: pr1, Number: 1, HeadSHA: "sha1"},
		{URL: pr2, Number: 2, HeadSHA: "sha2"},
	}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{}, launcher)

	res, err := triggerWithSource(context.Background(), eng, "mer-1", domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewTriggerAuto)
	if err != nil {
		t.Fatalf("TriggerWithSource: %v", err)
	}
	// Both PRs run: the pending one, and the pass the dead reviewer left
	// cancelled, which must not stay blocked for the auto policy (B394).
	created := map[string]bool{}
	for _, run := range res.CreatedRuns {
		created[run.PRURL] = true
	}
	if !res.Created || !created[pr1] || !created[pr2] {
		t.Fatalf("dead reviewer did not release its PRs: result=%+v", res)
	}
	if store.runs[0].Status != domain.ReviewRunCancelled || len(store.runs) != 3 {
		t.Fatalf("stale run was not reconciled before retry: %+v", store.runs)
	}
	if !launcher.aliveChecked || !launcher.spawned || launcher.notified {
		t.Fatalf("pending PR should launch a fresh reviewer: %+v", launcher)
	}
}
