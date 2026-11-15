package domain

import (
	"strings"
	"testing"
	"time"
)

var finishedNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func doneReport(note string, outputs ...ReportOutput) ReportRecord {
	return ReportRecord{State: ReportDone, Note: note, Outputs: outputs, CreatedAt: finishedNow.Add(-2 * time.Hour), DeliveryState: ReportAcknowledged}
}

func finishedFacts(reports ...ReportRecord) FinishedWorkerFacts {
	return FinishedWorkerFacts{
		Session: SessionRecord{Kind: KindWorker, Activity: Activity{State: ActivityIdle, LastActivityAt: finishedNow.Add(-90 * time.Minute)}},
		Reports: reports,
		Now:     finishedNow,
	}
}

// B588: a worker with a clean finished report and no PR anywhere is closable.
func TestFinishedWorkerKeepReason_CloseCases(t *testing.T) {
	reviewed := ReportOutput{Kind: ReportOutputPRReviewed, Reference: "https://github.com/o/r/pull/1030"}
	for name, f := range map[string]FinishedWorkerFacts{
		"research with no PR":          finishedFacts(doneReport("Research written up in the artifact.")),
		"review-only names its PR":     finishedFacts(doneReport("Reviewed PR #1030, no blockers found.", reviewed)),
		"review-only URL of its PR":    finishedFacts(doneReport("Reviewed https://github.com/o/r/pull/1030/", reviewed)),
		"mention of its own closed PR": withPRs(finishedFacts(doneReport("Closed PR #7 unmerged.")), PullRequest{URL: "https://github.com/o/r/pull/7", Closed: true}),
	} {
		if got := FinishedWorkerKeepReason(f); got != "" {
			t.Errorf("%s: kept: %s", name, got)
		}
	}
}

func withPRs(f FinishedWorkerFacts, prs ...PullRequest) FinishedWorkerFacts {
	f.PRs = prs
	return f
}

// B588: every keep-condition decided from durable facts keeps the worker.
func TestFinishedWorkerKeepReason_KeepCases(t *testing.T) {
	clean := func() FinishedWorkerFacts { return finishedFacts(doneReport("Finished.")) }
	mutate := func(fn func(*FinishedWorkerFacts)) FinishedWorkerFacts { f := clean(); fn(&f); return f }
	bitbucketURL := "https://bitbucket.org/acme/dpf/pull-requests/2"
	for name, tc := range map[string]struct {
		facts FinishedWorkerFacts
		want  string
	}{
		"orchestrator":          {mutate(func(f *FinishedWorkerFacts) { f.Session.Kind = KindOrchestrator }), "not a worker"},
		"pinned":                {mutate(func(f *FinishedWorkerFacts) { f.Session.IsPinned = true }), "pinned"},
		"no report at all":      {finishedFacts(), "no finished report"},
		"last asks input":       {finishedFacts(doneReport("Finished."), ReportRecord{State: ReportNeedsInput, Note: "Which option?", CreatedAt: finishedNow.Add(-time.Hour), DeliveryState: ReportAcknowledged}), "needs_input"},
		"last stuck":            {finishedFacts(ReportRecord{State: ReportStuck, Note: "Cannot build", CreatedAt: finishedNow.Add(-time.Hour), DeliveryState: ReportAcknowledged}), "stuck"},
		"last checkpoint":       {finishedFacts(ReportRecord{State: ReportCheckpoint, Note: "Halfway", CreatedAt: finishedNow.Add(-time.Hour), DeliveryState: ReportAcknowledged}), "checkpoint"},
		"done but waiting":      {finishedFacts(doneReport("Done, awaiting merge by the owner.")), "awaiting"},
		"done waits on you":     {finishedFacts(doneReport("Done. Waiting on your review.")), "waiting on"},
		"done asks a question":  {finishedFacts(doneReport("Finished. Should I also update the docs?")), "?"},
		"done offers more work": {finishedFacts(doneReport("Finished; tell me if you want me to open a follow-up.")), "want me to"},
		"report undelivered": {mutate(func(f *FinishedWorkerFacts) {
			f.Reports[0].DeliveryState = ReportPending
		}), "not yet delivered"},
		"message after report": {mutate(func(f *FinishedWorkerFacts) {
			f.Session.Metadata.LatestInteractionAt = finishedNow.Add(-time.Hour)
		}), "directed after"},
		"prompt after report": {mutate(func(f *FinishedWorkerFacts) {
			f.Session.Metadata.LatestUserPromptAt = finishedNow.Add(-time.Hour)
		}), "directed after"},
		"restored after report": {mutate(func(f *FinishedWorkerFacts) { f.Session.CleanupGeneration = 1 }), "restored"},
		"inside grace": {mutate(func(f *FinishedWorkerFacts) {
			f.Reports[0].CreatedAt = finishedNow.Add(-5 * time.Minute)
			f.Session.Activity.LastActivityAt = time.Time{}
		}), "grace"},
		"recent activity inside grace": {mutate(func(f *FinishedWorkerFacts) {
			f.Session.Activity.LastActivityAt = finishedNow.Add(-time.Minute)
		}), "grace"},
		"open linked PR": {withPRs(clean(), PullRequest{URL: "https://github.com/o/r/pull/9"}), "open PR"},
		// dpf-4: Bitbucket has no SCM adapter, so prs is empty and report
		// validation refuses the URL as an output; only the note carries it.
		"bitbucket PR URL in note, empty prs":  {finishedFacts(doneReport("Opened " + bitbucketURL + " for the fix.")), "no observed state"},
		"bitbucket PR number in note":          {finishedFacts(doneReport("PR #2 is up on Bitbucket.")), "no observed state"},
		"bitbucket PR in an earlier report":    {finishedFacts(ReportRecord{State: ReportCheckpoint, Note: "Pushed, PR-2 open", CreatedAt: finishedNow.Add(-3 * time.Hour), DeliveryState: ReportAcknowledged}, doneReport("All finished.")), "no observed state"},
		"reported created PR with no link row": {finishedFacts(doneReport("Finished.", ReportOutput{Kind: ReportOutputPRCreated, Reference: "https://github.com/o/r/pull/44"})), "no observed state"},
		"another PR than the reviewed one":     {finishedFacts(doneReport("Reviewed; follow-up in PR #55.", ReportOutput{Kind: ReportOutputPRReviewed, Reference: "https://github.com/o/r/pull/1030"})), "no observed state"},
	} {
		got := FinishedWorkerKeepReason(tc.facts)
		if got == "" || !strings.Contains(got, tc.want) {
			t.Errorf("%s: reason = %q, want it to mention %q", name, got, tc.want)
		}
	}
}

func TestWorkerBranchWork_KeepReason(t *testing.T) {
	if got := (WorkerBranchWork{}).KeepReason(); got != "" {
		t.Errorf("clean work kept: %s", got)
	}
	if got := (WorkerBranchWork{Unknown: "no git"}).KeepReason(); !strings.Contains(got, "unknown") {
		t.Errorf("unknown state: %q", got)
	}
	if got := (WorkerBranchWork{Unmerged: []string{"heads/x"}}).KeepReason(); !strings.Contains(got, "heads/x") {
		t.Errorf("unmerged: %q", got)
	}
}

func TestReportWaitingReason(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	r := func(st ReportState, note string, at time.Time, outs ...ReportOutput) []ReportRecord {
		return []ReportRecord{{State: st, Note: note, CreatedAt: at, Outputs: outs}}
	}
	const url = "https://github.com/o/r/pull/7"
	merged := []PullRequest{{Number: 7, URL: url, Merged: true, StateChangedAt: t0}}
	otherClosed := []PullRequest{{Number: 7, URL: url}, {Number: 3, URL: "https://github.com/o/r/pull/3", Closed: true, StateChangedAt: t0}}
	after, before := t0.Add(time.Minute), t0.Add(-time.Minute)
	for _, tc := range []struct {
		name string
		in   []ReportRecord
		prs  []PullRequest
		want bool
	}{
		{"none", nil, merged, false},
		{"needs input after merge", r(ReportNeedsInput, "x", after), merged, true},
		{"stuck after merge", r(ReportStuck, "x", after), merged, true},
		{"checkpoint waiting after merge", r(ReportCheckpoint, "waiting for the owner's go", after), merged, true},
		{"PR-created needs_input before merge", r(ReportNeedsInput, "PR #7 ready", before, ReportOutput{Kind: ReportOutputPRCreated, Reference: url}), merged, false},
		{"announce by number only", r(ReportNeedsInput, "PR #7 head abc; tests pass", before), merged, false},
		{"pre-merge wait on a post-merge step", r(ReportNeedsInput, "PR #7 ready; waiting for the owner to say Tallpine is open before the SSH checklist", before), merged, true},
		{"pre-merge wait naming no PR", r(ReportNeedsInput, "need a decision", before), merged, true},
		{"wait then unrelated PR closes", r(ReportNeedsInput, "waiting for the owner", before), otherClosed, true},
		{"announce of open PR while another closes", r(ReportNeedsInput, "PR #7 ready", before), otherClosed, true},
		{"plain done", r(ReportDone, "merged", after), merged, false},
	} {
		if got := ReportWaitingReason(tc.in, tc.prs) != ""; got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
