package review

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	stepPR  = "https://github.com/o/r/pull/1"
	stepSHA = "abc"
)

func stepState(status StateStatus, latest *domain.ReviewRun) PRReviewState {
	return PRReviewState{PRURL: stepPR, TargetSHA: stepSHA, Status: status, LatestRun: latest}
}

func stepRun(harness domain.ReviewerHarness, status domain.ReviewRunStatus, verdict domain.ReviewVerdict) domain.ReviewRun {
	return domain.ReviewRun{PRURL: stepPR, TargetSHA: stepSHA, Harness: harness, Status: status, Verdict: verdict}
}

// The eligibility step decides, per PR head, whether a trigger records a run.
func TestPRDueForTrigger(t *testing.T) {
	t.Parallel()
	approvedByCodex := stepRun(domain.ReviewerCodex, domain.ReviewRunComplete, domain.VerdictApproved)
	runningClaude := stepRun(domain.ReviewerClaudeCode, domain.ReviewRunRunning, domain.VerdictNone)
	cases := []struct {
		name                       string
		state                      PRReviewState
		runs                       []domain.ReviewRun
		source                     domain.ReviewTriggerSource
		opts                       TriggerOptions
		harnessOverride, secondOpn bool
		want                       bool
	}{
		{"ineligible never", stepState(ReviewStateIneligible, nil), nil, domain.ReviewTriggerManual, TriggerOptions{Rerun: true}, true, true, false},
		{"needs review", stepState(ReviewStateNeedsReview, nil), nil, domain.ReviewTriggerManual, TriggerOptions{}, false, false, true},
		{"up to date is skipped", stepState(ReviewStateUpToDate, &approvedByCodex), []domain.ReviewRun{approvedByCodex}, domain.ReviewTriggerManual, TriggerOptions{}, false, false, false},
		{"changes requested retried by a person", stepState(ReviewStateChangesRequested, nil), nil, domain.ReviewTriggerManual, TriggerOptions{}, false, false, true},
		{"changes requested not retried by auto", stepState(ReviewStateChangesRequested, nil), nil, domain.ReviewTriggerAuto, TriggerOptions{}, false, false, false},
		{"another reviewer gets a second opinion", stepState(ReviewStateUpToDate, &approvedByCodex), []domain.ReviewRun{approvedByCodex}, domain.ReviewTriggerManual, TriggerOptions{}, true, false, true},
		{"same reviewer override is not a second opinion", stepState(ReviewStateUpToDate, &approvedByCodex), []domain.ReviewRun{approvedByCodex}, domain.ReviewTriggerManual, TriggerOptions{}, false, false, false},
		{"config override reviews an approved head", stepState(ReviewStateUpToDate, &approvedByCodex), []domain.ReviewRun{approvedByCodex}, domain.ReviewTriggerManual, TriggerOptions{}, false, true, true},
		{"rerun reviews an approved head", stepState(ReviewStateUpToDate, &approvedByCodex), []domain.ReviewRun{approvedByCodex}, domain.ReviewTriggerManual, TriggerOptions{Rerun: true}, false, false, true},
		{"rerun never doubles a running reviewer", stepState(ReviewStateRunning, &runningClaude), []domain.ReviewRun{runningClaude}, domain.ReviewTriggerManual, TriggerOptions{Rerun: true}, false, false, false},
		{"reject-reviewed skips a reviewed head", stepState(ReviewStateNeedsReview, nil), []domain.ReviewRun{approvedByCodex}, domain.ReviewTriggerManual, TriggerOptions{RejectReviewedHead: true}, false, false, false},
		{"reject-reviewed allows a fresh head", stepState(ReviewStateNeedsReview, nil), nil, domain.ReviewTriggerManual, TriggerOptions{RejectReviewedHead: true}, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			harness := domain.ReviewerClaudeCode
			if got := prDueForTrigger(tc.state, tc.runs, harness, tc.source, tc.opts, tc.harnessOverride, tc.secondOpn); got != tc.want {
				t.Fatalf("prDueForTrigger = %v, want %v", got, tc.want)
			}
		})
	}
}

// The reviewer-resolution step merges a per-pass override into the project's
// reviewer without editing project config.
func TestResolveReviewerConfig(t *testing.T) {
	t.Parallel()
	base := domain.AgentConfig{Model: "base"}
	over := domain.AgentConfig{Model: "over"}
	cases := []struct {
		name         string
		harness      domain.ReviewerHarness
		config       domain.AgentConfig
		override     domain.ReviewerHarness
		overrideCfg  domain.AgentConfig
		wantHarness  domain.ReviewerHarness
		wantModel    string
		wantOverride bool
	}{
		{"no override", domain.ReviewerCodex, base, "", domain.AgentConfig{}, domain.ReviewerCodex, "base", false},
		{"config only merges", domain.ReviewerCodex, base, "", over, domain.ReviewerCodex, "over", true},
		{"same harness merges", domain.ReviewerCodex, base, domain.ReviewerCodex, over, domain.ReviewerCodex, "over", true},
		{"same harness, same config is no override", domain.ReviewerCodex, base, domain.ReviewerCodex, base, domain.ReviewerCodex, "base", false},
		{"other harness takes only the override config", domain.ReviewerCodex, base, domain.ReviewerClaudeCode, over, domain.ReviewerClaudeCode, "over", false},
		{"other harness drops the resolved config", domain.ReviewerCodex, base, domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewerClaudeCode, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			harness, config, overridden := resolveReviewerConfig(tc.harness, tc.config, tc.override, tc.overrideCfg)
			if harness != tc.wantHarness || config.Model != tc.wantModel || overridden != tc.wantOverride {
				t.Fatalf("got (%s, %q, %v), want (%s, %q, %v)", harness, config.Model, overridden, tc.wantHarness, tc.wantModel, tc.wantOverride)
			}
		})
	}
}
