package activity

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type fakeSessions struct {
	rows []domain.SessionRecord
	err  error
}

func (f fakeSessions) ListLiveSessions(context.Context) ([]domain.SessionRecord, error) {
	return liveOnly(f.rows), f.err
}

// liveOnly mirrors the store's WHERE is_terminated = 0.
func liveOnly(rows []domain.SessionRecord) []domain.SessionRecord {
	var out []domain.SessionRecord
	for _, r := range rows {
		if !r.IsTerminated {
			out = append(out, r)
		}
	}
	return out
}

type fakeSink struct {
	id      domain.SessionID
	signals []ports.ActivitySignal
	applied func()
	err     error
}

func (f *fakeSink) ApplyActivitySignal(_ context.Context, id domain.SessionID, signal ports.ActivitySignal) error {
	f.id = id
	f.signals = append(f.signals, signal)
	if f.err != nil {
		return f.err
	}
	if f.applied != nil {
		f.applied()
	}
	return nil
}

type fakeRuntime struct {
	output string
	err    error
	calls  int
}

func (f *fakeRuntime) GetOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	f.calls++
	return f.output, f.err
}

type fakeAgents map[domain.AgentHarness]ports.Agent

func (f fakeAgents) Agent(harness domain.AgentHarness) (ports.Agent, bool) {
	agent, ok := f[harness]
	return agent, ok
}

func activeSession(now time.Time, harness domain.AgentHarness) domain.SessionRecord {
	return domain.SessionRecord{
		ID:        "tp-1",
		Harness:   harness,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now.Add(-3 * time.Minute)},
		UpdatedAt: now.Add(-3 * time.Minute),
		Metadata: domain.SessionMetadata{
			RuntimeHandleID: "tp-1",
			RuntimeLaunchID: "launch-1",
		},
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPollReconcilesStaleCodexAtComposer(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	session := activeSession(now, domain.HarnessCodex)
	sink := &fakeSink{}
	runtime := &fakeRuntime{output: "› Write tests for @filename\n\ngpt-5.6-sol low · ~/project\n"}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{session}},
		sink,
		runtime,
		fakeAgents{domain.HarnessCodex: codex.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)

	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 1 {
		t.Fatalf("signals = %d, want 1", len(sink.signals))
	}
	signal := sink.signals[0]
	if sink.id != session.ID || signal.State != domain.ActivityIdle || signal.Event != "terminal-idle" {
		t.Fatalf("unexpected reconciliation: id=%q signal=%+v", sink.id, signal)
	}
	if signal.ExpectedRevision == nil || *signal.ExpectedRevision != session.Revision || signal.LaunchID != "launch-1" {
		t.Fatalf("reconciliation fence = %+v, want revision=%d launch=launch-1", signal, session.Revision)
	}
}

func TestPollKeepsGenuineLongCodexTurnActive(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	sink := &fakeSink{}
	runtime := &fakeRuntime{output: "• Working (3m 10s • esc to interrupt)\n› Add tests\n\ngpt-5.6-sol low · ~/project\n"}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{activeSession(now, domain.HarnessCodex)}},
		sink,
		runtime,
		fakeAgents{domain.HarnessCodex: codex.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)

	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 0 {
		t.Fatalf("long active turn emitted reconciliation: %+v", sink.signals)
	}
}

func TestPollPreservesClaudeWaitingInputWithoutContinuousCapability(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	session := activeSession(now, domain.HarnessClaudeCode)
	session.Activity = domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: now.Add(-time.Second)}
	session.UpdatedAt = now.Add(-time.Second)
	sink := &fakeSink{}
	runtime := &fakeRuntime{output: claudeStuckActiveScreen}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{session}},
		sink,
		runtime,
		fakeAgents{domain.HarnessClaudeCode: claudecode.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)

	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.calls != 0 || len(sink.signals) != 0 {
		t.Fatalf("sticky Claude waiting state was sampled: output calls=%d signals=%+v", runtime.calls, sink.signals)
	}
}

const claudeStuckActiveScreen = "⏺ Login expired · Please run /login\n" +
	"\n" +
	"✻ Worked for 0s\n" +
	"\n" +
	"────────────────────────────────────────────────\n" +
	"❯ so btw, the status isn't permanently stuck\n" +
	"────────────────────────────────────────────────\n" +
	"\n" +
	"  ⏵⏵ bypass permissions on (shift+tab to cycle) · PR #4090\n"

func TestPollReconcilesStaleClaudeCodeAfterAbortedTurn(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	session := activeSession(now, domain.HarnessClaudeCode)
	sink := &fakeSink{}
	runtime := &fakeRuntime{output: claudeStuckActiveScreen}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{session}},
		sink,
		runtime,
		fakeAgents{domain.HarnessClaudeCode: claudecode.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)

	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 1 {
		t.Fatalf("signals = %d, want 1", len(sink.signals))
	}
	signal := sink.signals[0]
	if sink.id != session.ID || signal.State != domain.ActivityWaitingInput || signal.Event != "terminal-waiting-input" {
		t.Fatalf("unexpected reconciliation: id=%q signal=%+v", sink.id, signal)
	}
	if signal.ExpectedRevision == nil || *signal.ExpectedRevision != session.Revision || signal.LaunchID != "launch-1" {
		t.Fatalf("reconciliation fence = %+v, want revision=%d launch=launch-1", signal, session.Revision)
	}
}

func TestPollKeepsGenuineLongClaudeTurnActive(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	sink := &fakeSink{}
	runtime := &fakeRuntime{output: "✻ Computing… (3m 10s · ↓ 114 tokens)\n" +
		"────────────────────────────────────────────────\n" +
		"❯\n" +
		"────────────────────────────────────────────────\n" +
		"⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt · ← for agents\n"}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{activeSession(now, domain.HarnessClaudeCode)}},
		sink,
		runtime,
		fakeAgents{domain.HarnessClaudeCode: claudecode.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)

	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 0 {
		t.Fatalf("long active turn emitted reconciliation: %+v", sink.signals)
	}
}

func TestPollSkipsFreshActiveAndOutputFailures(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	fresh := activeSession(now, domain.HarnessCodex)
	fresh.Activity.LastActivityAt = now.Add(-time.Minute)
	runtime := &fakeRuntime{err: errors.New("capture failed")}
	observer := New(
		fakeSessions{rows: []domain.SessionRecord{fresh}},
		&fakeSink{},
		runtime,
		fakeAgents{domain.HarnessCodex: codex.New()},
		Config{Clock: func() time.Time { return now }, Logger: testLogger()},
	)
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.calls != 0 {
		t.Fatalf("fresh session output calls = %d, want 0", runtime.calls)
	}

	stale := activeSession(now, domain.HarnessCodex)
	observer.sessions = fakeSessions{rows: []domain.SessionRecord{stale}}
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPollReturnsSessionListFailure(t *testing.T) {
	want := errors.New("list failed")
	observer := New(fakeSessions{err: want}, &fakeSink{}, &fakeRuntime{}, nil, Config{Logger: testLogger()})
	if err := observer.Poll(context.Background()); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

type renderedRuntime struct {
	fakeRuntime
	rendered    string
	renderedErr error
}

type renderedWaitingAgent struct{ ports.Agent }

func (renderedWaitingAgent) DetectTerminalActivity(string) (domain.ActivityState, bool) {
	return domain.ActivityWaitingInput, true
}
func (renderedWaitingAgent) ContinuouslyDetectTerminalActivity() bool { return true }
func (renderedWaitingAgent) RequiresRenderedActivity() bool           { return true }

func TestRenderedModalResolutionKeepsBoundaryEvent(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	rec := activeSession(now, domain.AgentHarness("hermes"))
	rec.Activity.State = domain.ActivityBlocked
	sink := &fakeSink{}
	observer := New(fakeSessions{rows: []domain.SessionRecord{rec}}, sink, &renderedRuntime{}, fakeAgents{domain.AgentHarness("hermes"): renderedWaitingAgent{}}, Config{Clock: func() time.Time { return now }, Logger: testLogger()})
	if err := observer.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 1 || sink.signals[0].Event != "permission-resolved" || sink.signals[0].State != domain.ActivityWaitingInput {
		t.Fatalf("blocked to waiting lost modal boundary: %+v", sink.signals)
	}
}

func (r *renderedRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return r.rendered, r.renderedErr
}
