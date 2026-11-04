package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B393: a worker's reviewer was busy on PR #1 when the orchestrator asked to
// rerun PR #2. The 409 named PR #2 as "already reviewing", while `review ls`
// showed no pass on it, so it read as a phantom run. It must name PR #1.
func TestAgentRerunNamesThePRTheBusyReviewerIsOn(t *testing.T) {
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
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{}, &fakeLauncher{alive: true})

	_, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, Rerun: true})
	if !errors.Is(err, ErrReviewAlreadyRunning) {
		t.Fatalf("err = %v, want ErrReviewAlreadyRunning", err)
	}
	if !strings.Contains(err.Error(), "busy reviewing https://github.com/o/r/pull/1 head busysha") || !strings.Contains(err.Error(), "PR #2 head newsha cannot start yet") {
		t.Fatalf("err = %v, want it to name the busy PR and the waiting PR", err)
	}
}
