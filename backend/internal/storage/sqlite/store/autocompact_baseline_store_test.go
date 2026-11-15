package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

var baselineEpoch = time.Unix(1_700_000_000, 0).UTC()

type baselineCall struct {
	input int64
	at    time.Duration // after the session's creation
}

// baselineSession creates a session of the project, kind and harness created
// at minute n, so a larger n is a newer session.
func baselineSession(t *testing.T, s *sqlite.Store, project string, kind domain.SessionKind, harness domain.AgentHarness, n int) domain.SessionRecord {
	t.Helper()
	rec := sampleRecord(project)
	rec.Kind, rec.Harness = kind, harness
	rec.CreatedAt = baselineEpoch.Add(time.Duration(n) * time.Minute)
	rec.UpdatedAt = rec.CreatedAt
	got, err := s.CreateSession(context.Background(), rec)
	mustNoError(t, err, "create baseline session")
	return got
}

// baselineCalls records model calls on a new binding (native root) and source
// of the session, in the order given.
func baselineCalls(t *testing.T, s *sqlite.Store, session domain.SessionRecord, root string, kind domain.UsageSourceKind, calls ...baselineCall) {
	t.Helper()
	binding := mustUpsertUsageBinding(t, s, session, baselineEpoch, domain.UsageBindingRecord{NativeRootID: root, InitialModelID: "m", State: domain.UsageBindingActive})
	source := mustInsertUsageSource(t, s, baselineEpoch, domain.UsageSourceRecord{BindingID: binding.ID, Kind: kind, NativeSessionID: root, ArtifactPath: "/tmp/" + string(session.ID) + root + string(kind), State: domain.UsageSourcePending})
	events := make([]domain.ModelUsageEvent, 0, len(calls))
	for i, call := range calls {
		ev := usageEvent(fmt.Sprintf("%s-%d", root, i), canonicalUsageTokens(call.input, 0, call.input, 1))
		if session.Harness == domain.HarnessClaudeCode {
			ev.ProviderID, ev.ProviderUsageJSON = domain.UsageProviderAnthropic, anthropicProviderUsage(call.input, 0, nil, nil)
		}
		ev.CreatedAt = session.CreatedAt.Add(call.at)
		events = append(events, ev)
	}
	if err := s.ApplyUsageChunk(context.Background(), source.ID, 0, source.UpdatedAt, domain.SourceCursorState{ByteOffset: 1, State: domain.UsageSourceActive, UpdatedAt: baselineEpoch}, events); err != nil {
		t.Fatalf("apply baseline usage: %v", err)
	}
}

func mainSource(harness domain.AgentHarness) domain.UsageSourceKind {
	if harness == domain.HarnessCodex {
		return domain.UsageSourceCodexRollout
	}
	return domain.UsageSourceClaudeMain
}

func readBaseline(t *testing.T, s *sqlite.Store, project string, kind domain.SessionKind, harness domain.AgentHarness) int64 {
	t.Helper()
	got, err := s.Baseline(context.Background(), domain.ProjectID(project), kind, harness)
	mustNoError(t, err, "read baseline")
	return got
}

// TestAutoCompactBaselineTakesEachSessionsFirstMainCall: the sample is the
// session's first main-thread call. A later call in the same binding, a
// subagent's call (even an earlier one) and a later binding (a resume, which
// starts with the old context) are not its baseline.
func TestAutoCompactBaselineTakesEachSessionsFirstMainCall(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "base")
	for n := range 3 {
		session := baselineSession(t, s, "base", domain.KindWorker, domain.HarnessClaudeCode, n)
		baselineCalls(t, s, session, "sub", domain.UsageSourceClaudeSubagent, baselineCall{1_000, 0})
		baselineCalls(t, s, session, "main", domain.UsageSourceClaudeMain,
			baselineCall{66_000, time.Second}, baselineCall{500_000, time.Minute})
		baselineCalls(t, s, session, "resumed", domain.UsageSourceClaudeMain, baselineCall{2_000, time.Hour})
	}
	if got := readBaseline(t, s, "base", domain.KindWorker, domain.HarnessClaudeCode); got != 66_000 {
		t.Fatalf("baseline = %d, want 66000 (each session's first main call)", got)
	}
}

// TestAutoCompactBaselineReadsTheNewestTenSessions: the two oldest sessions
// are left out. All twelve would give 50k, the oldest ten 40k.
func TestAutoCompactBaselineReadsTheNewestTenSessions(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "base")
	inputs := []int64{10_000, 10_000, 40_000, 40_000, 40_000, 40_000, 60_000, 60_000, 60_000, 60_000, 60_000, 60_000}
	for n, input := range inputs {
		session := baselineSession(t, s, "base", domain.KindWorker, domain.HarnessClaudeCode, n)
		baselineCalls(t, s, session, "main", domain.UsageSourceClaudeMain, baselineCall{input, 0})
	}
	if got := readBaseline(t, s, "base", domain.KindWorker, domain.HarnessClaudeCode); got != 60_000 {
		t.Fatalf("baseline = %d, want 60000 from the newest 10 sessions", got)
	}
}

// TestAutoCompactBaselineMatchesProjectKindAndHarness: each reading sees only
// its own project, kind and harness, planners and reviewers read the worker
// baseline, and fewer than three samples use the default.
func TestAutoCompactBaselineMatchesProjectKindAndHarness(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "base")
	seedProject(t, s, "other")
	n := 0
	add := func(project string, kind domain.SessionKind, harness domain.AgentHarness, input int64, count int) {
		for range count {
			n++
			session := baselineSession(t, s, project, kind, harness, n)
			baselineCalls(t, s, session, "main", mainSource(harness), baselineCall{input, 0})
		}
	}
	add("base", domain.KindWorker, domain.HarnessClaudeCode, 66_000, 3)
	add("base", domain.KindOrchestrator, domain.HarnessClaudeCode, 51_000, 3)
	add("base", domain.KindWorker, domain.HarnessCodex, 30_000, 3)
	add("other", domain.KindWorker, domain.HarnessClaudeCode, 900_000, 3)
	add("base", domain.KindOrchestrator, domain.HarnessCodex, 20_000, 2)

	for _, tt := range []struct {
		name    string
		kind    domain.SessionKind
		harness domain.AgentHarness
		want    int64
	}{
		{"worker", domain.KindWorker, domain.HarnessClaudeCode, 66_000},
		{"planner reads the worker baseline", "planner", domain.HarnessClaudeCode, 66_000},
		{"reviewer reads the worker baseline", "reviewer", domain.HarnessClaudeCode, 66_000},
		{"orchestrator", domain.KindOrchestrator, domain.HarnessClaudeCode, 51_000},
		{"codex worker", domain.KindWorker, domain.HarnessCodex, 30_000},
		{"two codex orchestrators use the default", domain.KindOrchestrator, domain.HarnessCodex, domain.AutoCompactBaselineDefault},
	} {
		if got := readBaseline(t, s, "base", tt.kind, tt.harness); got != tt.want {
			t.Errorf("%s: baseline = %d, want %d", tt.name, got, tt.want)
		}
	}
	if got := readBaseline(t, s, "other", domain.KindWorker, domain.HarnessClaudeCode); got != 900_000 {
		t.Errorf("other project baseline = %d, want 900000", got)
	}
	if got := readBaseline(t, s, "none", domain.KindWorker, domain.HarnessClaudeCode); got != domain.AutoCompactBaselineDefault {
		t.Errorf("a project with no sessions = %d, want the default", got)
	}
}
