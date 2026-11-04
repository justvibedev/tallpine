package domain

import (
	"testing"
	"time"
)

func TestDeriveNeedsInputAudience(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	reportAt := now.Add(-time.Minute)
	base := NeedsInputFacts{Activity: ActivityIdle, ReportAt: reportAt, ReportNote: "need a decision", OrchestratorLive: true, Now: now}
	with := func(edit func(*NeedsInputFacts)) NeedsInputFacts {
		f := base
		edit(&f)
		return f
	}
	cases := []struct {
		name string
		in   NeedsInputFacts
		want NeedsInputAudience
	}{
		{"orchestrator-bound report waits on the orchestrator", base, NeedsInputOrchestrator},
		{"a waiting_input agent wait (approval, question, permission-request, notification) needs the human even after a report", with(func(f *NeedsInputFacts) { f.Activity = ActivityWaitingInput }), NeedsInputHuman},
		{"orchestrator message clears it", with(func(f *NeedsInputFacts) { f.InteractionAt = reportAt.Add(time.Second) }), NeedsInputNone},
		{"new turn (prompt) clears it", with(func(f *NeedsInputFacts) { f.PromptAt = reportAt.Add(time.Second) }), NeedsInputNone},
		{"working session is not waiting", with(func(f *NeedsInputFacts) { f.Activity = ActivityActive }), NeedsInputNone},
		{"timeout escalates", with(func(f *NeedsInputFacts) { f.Now = reportAt.Add(OrchestratorWaitTimeout) }), NeedsInputHuman},
		{"just inside the timeout still waits", with(func(f *NeedsInputFacts) { f.Now = reportAt.Add(OrchestratorWaitTimeout - time.Second) }), NeedsInputOrchestrator},
		{"standalone or dead orchestrator needs the human", with(func(f *NeedsInputFacts) { f.OrchestratorLive = false }), NeedsInputHuman},
		{"pending approval or question needs the human", with(func(f *NeedsInputFacts) { f.Activity = ActivityBlocked }), NeedsInputHuman},
		{"daemon-authored agent wait needs the human", with(func(f *NeedsInputFacts) { f.ReportNote = SynthesizedReportPrefix + " input_required: blocked" }), NeedsInputHuman},
		{"paused at the prompt without a report needs the human", NeedsInputFacts{Activity: ActivityWaitingInput, OrchestratorLive: true, Now: now}, NeedsInputHuman},
		{"idle without a report is not waiting", NeedsInputFacts{Activity: ActivityIdle, Now: now}, NeedsInputNone},
	}
	for _, tc := range cases {
		if got := DeriveNeedsInputAudience(tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
