package lifecycle

import (
	"reflect"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestActivity_FirstSignalStampsReceipt(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now()}}
	// A same-state repeat (idle on an idle-seeded row) must still write: the
	// receipt itself is the durable fact that clears no_signal.
	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if got.FirstSignalAt.IsZero() {
		t.Fatalf("first signal not stamped: %+v", got)
	}
	stamped := got.FirstSignalAt
	// Later signals must not move the receipt.
	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Timestamp: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"]; !got.FirstSignalAt.Equal(stamped) {
		t.Fatalf("first signal moved: %v -> %v", stamped, got.FirstSignalAt)
	}
}

func TestActivity_SameStateRepeatAfterReceiptIsNoOp(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.FirstSignalAt = time.Now()
	st.sessions["mer-1"] = rec
	before := st.sessions["mer-1"]
	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityActive}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.sessions["mer-1"], before) {
		t.Fatalf("same-state repeat after receipt must not rewrite: %+v", st.sessions["mer-1"])
	}
}

func TestMarkSpawnedClearsFirstSignalAndLeavesResumeIdentityUnverified(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.FirstSignalAt = time.Now().Add(-time.Hour)
	rec.Metadata.RuntimeLaunchID = "launch-old"
	rec.Metadata.AgentSessionID = "native-1"
	rec.Metadata.AgentSessionIDLaunchID = "launch-old"
	st.sessions["mer-1"] = rec
	if err := m.MarkSpawned(ctx, "mer-1", domain.SessionMetadata{RuntimeLaunchID: "launch-new"}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if !got.FirstSignalAt.IsZero() {
		t.Fatalf("spawn/restore must clear the receipt, got %+v", got)
	}
	if got.Metadata.AgentSessionID != "native-1" || got.Metadata.AgentSessionIDLaunchID != "launch-old" {
		t.Fatalf("spawn/restore must retain the resume hint without re-proving it, got %+v", got.Metadata)
	}
	if got.Metadata.AgentSessionIDLaunchID == got.Metadata.RuntimeLaunchID {
		t.Fatalf("new launch unexpectedly inherited native identity proof: %+v", got.Metadata)
	}
}

func TestActivity_WaitingInputTransitionEmitsNotification(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", DisplayName: "checkout-flow", Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now.Add(-time.Minute)}, FirstSignalAt: now.Add(-time.Minute)}

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityWaitingInput}); err != nil {
		t.Fatal(err)
	}
	if len(sink.intents) != 1 {
		t.Fatalf("intents = %d, want 1", len(sink.intents))
	}
	intent := sink.intents[0]
	if intent.Type != domain.NotificationNeedsInput || intent.SessionID != "mer-1" || intent.ProjectID != "mer" || intent.SessionDisplayName != "checkout-flow" {
		t.Fatalf("intent = %+v", intent)
	}
}

// The user answering the agent is what resolves a needs-input notification —
// there is no manual acknowledgement anywhere in the flow.
func TestActivity_LeavingNeedsInputResolvesNotification(t *testing.T) {
	for _, tt := range []struct {
		name string
		next domain.ActivityState
	}{
		{name: "answered", next: domain.ActivityActive},
		{name: "went idle", next: domain.ActivityIdle},
		{name: "agent exited", next: domain.ActivityExited},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			sink := &fakeNotificationSink{}
			m := New(st, nil, WithNotificationSink(sink))
			now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
			m.clock = func() time.Time { return now }
			st.sessions["mer-1"] = domain.SessionRecord{
				ID: "mer-1", ProjectID: "mer",
				Activity:      domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: now.Add(-time.Minute)},
				FirstSignalAt: now.Add(-time.Minute),
			}

			if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: tt.next}); err != nil {
				t.Fatal(err)
			}
			if len(sink.resolutions) != 1 {
				t.Fatalf("resolutions = %+v, want 1", sink.resolutions)
			}
			got := sink.resolutions[0]
			if got.Type != domain.NotificationNeedsInput || got.SessionID != "mer-1" || !got.ResolvedAt.Equal(now) {
				t.Fatalf("resolution = %+v", got)
			}
		})
	}
}

// An in-family escalation is still the same pause: nothing was answered, so
// there is nothing to resolve.
func TestActivity_WaitingInputToBlockedDoesNotResolve(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	now := time.Now()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer",
		Activity:      domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: now},
		FirstSignalAt: now,
	}

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityBlocked}); err != nil {
		t.Fatal(err)
	}
	if len(sink.resolutions) != 0 {
		t.Fatalf("resolutions = %+v, want none", sink.resolutions)
	}
}

// Terminating a paused session also clears its ping: nobody is waiting on the
// user any more.
func TestMarkTerminated_ResolvesNeedsInputNotification(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	now := time.Now()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer",
		Activity: domain.Activity{State: domain.ActivityBlocked, LastActivityAt: now},
	}

	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(sink.resolutions) != 1 || sink.resolutions[0].Type != domain.NotificationNeedsInput {
		t.Fatalf("resolutions = %+v", sink.resolutions)
	}
}

func TestActivity_WaitingInputSameStateDoesNotEmitNotification(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	now := time.Now()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Activity: domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: now}, FirstSignalAt: now}

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityWaitingInput}); err != nil {
		t.Fatal(err)
	}
	if len(sink.intents) != 0 {
		t.Fatalf("same-state waiting_input emitted %+v", sink.intents)
	}
}

func TestActivity_BlockedTransitionEmitsNotification(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	now := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", DisplayName: "checkout-flow", Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now.Add(-time.Minute)}, FirstSignalAt: now.Add(-time.Minute)}

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityBlocked}); err != nil {
		t.Fatal(err)
	}
	if len(sink.intents) != 1 {
		t.Fatalf("intents = %d, want 1 (blocked is a needs-input entry)", len(sink.intents))
	}
	if sink.intents[0].Type != domain.NotificationNeedsInput {
		t.Fatalf("intent type = %q, want needs_input", sink.intents[0].Type)
	}
}

func TestActivity_WaitingInputToBlockedDoesNotReNotify(t *testing.T) {
	// waiting_input -> blocked is an in-family escalation: the user was already
	// pinged once for this pause, so no second notification.
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	now := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Activity: domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: now.Add(-time.Minute)}, FirstSignalAt: now.Add(-time.Minute)}

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityBlocked}); err != nil {
		t.Fatal(err)
	}
	if len(sink.intents) != 0 {
		t.Fatalf("in-family escalation emitted notification: %+v", sink.intents)
	}
}

func TestActivity_TerminatedSessionDoesNotEmitNotification(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true, Activity: domain.Activity{State: domain.ActivityExited}}

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityWaitingInput}); err != nil {
		t.Fatal(err)
	}
	if len(sink.intents) != 0 {
		t.Fatalf("terminated session emitted %+v", sink.intents)
	}
}

func TestActivity_WorkerIdleDoesNotNudgeOrchestrator(t *testing.T) {
	m, st, msg := newManager()
	now := time.Now()
	st.sessions["mer-orch"] = domain.SessionRecord{ID: "mer-orch", ProjectID: "mer", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}, FirstSignalAt: now}
	st.sessions["mer-8"] = domain.SessionRecord{ID: "mer-8", ProjectID: "mer", Kind: domain.KindWorker, DisplayName: "husky-setup", Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, FirstSignalAt: now}

	if err := m.ApplyActivitySignal(ctx, "mer-8", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-8"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("worker activity = %q, want idle", got)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("orchestrator nudges = %d, want 0", len(msg.msgs))
	}
}

func TestActivity_OrchestratorIdleDoesNotNudge(t *testing.T) {
	m, st, msg := newManager()
	now := time.Now()
	st.sessions["mer-orch"] = domain.SessionRecord{ID: "mer-orch", ProjectID: "mer", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, FirstSignalAt: now}

	if err := m.ApplyActivitySignal(ctx, "mer-orch", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-orch"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("orchestrator activity = %q, want idle", got)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("orchestrator idle self-nudged: %d, want 0", len(msg.msgs))
	}
}

func TestActivity_WorkerWaitingToIdleDoesNotNudge(t *testing.T) {
	m, st, msg := newManager()
	now := time.Now()
	st.sessions["mer-orch"] = domain.SessionRecord{ID: "mer-orch", ProjectID: "mer", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}, FirstSignalAt: now}
	st.sessions["mer-8"] = domain.SessionRecord{ID: "mer-8", ProjectID: "mer", Kind: domain.KindWorker, Activity: domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: now}, FirstSignalAt: now}

	if err := m.ApplyActivitySignal(ctx, "mer-8", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("waiting_input->idle nudged: %d, want 0", len(msg.msgs))
	}
}

func TestActivity_WorkerIdleOrchestratorBlockedSuppressed(t *testing.T) {
	m, st, msg := newManager()
	now := time.Now()
	st.sessions["mer-orch"] = domain.SessionRecord{ID: "mer-orch", ProjectID: "mer", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityBlocked, LastActivityAt: now}, FirstSignalAt: now}
	st.sessions["mer-8"] = domain.SessionRecord{ID: "mer-8", ProjectID: "mer", Kind: domain.KindWorker, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, FirstSignalAt: now}

	if err := m.ApplyActivitySignal(ctx, "mer-8", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("nudged a blocked orchestrator: %d, want 0", len(msg.msgs))
	}
}

func TestActivity_WorkerIdleOrchestratorActiveDefersNoNudge(t *testing.T) {
	m, st, msg := newManager()
	now := time.Now()
	st.sessions["mer-orch"] = domain.SessionRecord{ID: "mer-orch", ProjectID: "mer", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, FirstSignalAt: now}
	st.sessions["mer-8"] = domain.SessionRecord{ID: "mer-8", ProjectID: "mer", Kind: domain.KindWorker, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, FirstSignalAt: now}

	if err := m.ApplyActivitySignal(ctx, "mer-8", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("nudged a busy orchestrator: %d, want 0", len(msg.msgs))
	}
}
