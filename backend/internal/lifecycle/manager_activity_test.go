package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestActivity_InvalidIsIgnored(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	before := st.sessions["mer-1"]
	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: false, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.sessions["mer-1"], before) {
		t.Fatal("invalid signal must not mutate")
	}
}

func TestActivity_MetadataOnlyStoresAgentSessionIDWithoutChangingActivity(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-1"
	rec.FirstSignalAt = time.Now().Add(-time.Minute)
	st.sessions["mer-1"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		LaunchID: "launch-1", AgentSessionID: "native-session-1",
	}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if got.Metadata.AgentSessionID != "native-session-1" {
		t.Fatalf("AgentSessionID = %q, want native-session-1", got.Metadata.AgentSessionID)
	}
	if got.Metadata.AgentSessionIDLaunchID != "launch-1" {
		t.Fatalf("AgentSessionIDLaunchID = %q, want launch-1", got.Metadata.AgentSessionIDLaunchID)
	}
	if got.Activity != rec.Activity {
		t.Fatalf("metadata-only hook changed activity: got %+v, want %+v", got.Activity, rec.Activity)
	}
	if !got.FirstSignalAt.Equal(rec.FirstSignalAt) {
		t.Fatalf("metadata-only hook changed FirstSignalAt: got %v, want %v", got.FirstSignalAt, rec.FirstSignalAt)
	}
}

func TestActivity_UserPromptStoresItsSignalTimestamp(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-1"
	rec.FirstSignalAt = time.Now().Add(-time.Minute)
	st.sessions[rec.ID] = rec
	signalAt := time.Unix(456, 0).UTC()

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		LaunchID: "launch-1", LatestUserPrompt: "eventless payload alias", Timestamp: signalAt.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions[rec.ID]
	if got.Metadata.LatestUserPrompt != "" || !got.Metadata.LatestUserPromptAt.IsZero() {
		t.Fatalf("eventless payload advanced user fact: %q at %s", got.Metadata.LatestUserPrompt, got.Metadata.LatestUserPromptAt)
	}

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: rec.Activity.State, Event: "user-prompt-submit", LaunchID: "launch-1",
		LatestUserPrompt: "keep the row compact", Timestamp: signalAt,
	}); err != nil {
		t.Fatal(err)
	}
	got = st.sessions[rec.ID]
	if got.Metadata.LatestUserPrompt != "keep the row compact" || !got.Metadata.LatestUserPromptAt.Equal(signalAt) {
		t.Fatalf("latest user prompt = %q at %s", got.Metadata.LatestUserPrompt, got.Metadata.LatestUserPromptAt)
	}

	repeatedAt := signalAt.Add(time.Minute)
	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: got.Activity.State, Event: "user-prompt-submit", LaunchID: "launch-1",
		LatestUserPrompt: "keep the row compact", Timestamp: repeatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	got = st.sessions[rec.ID]
	if !got.Metadata.LatestUserPromptAt.Equal(repeatedAt) {
		t.Fatalf("repeated user prompt timestamp = %s, want %s", got.Metadata.LatestUserPromptAt, repeatedAt)
	}
}

func TestActivity_ReorderedPromptPreservesLatestHumanCheckpoint(t *testing.T) {
	promptAt := time.Unix(456, 0).UTC()
	for _, tt := range []struct {
		name    string
		prompt  string
		at      time.Time
		wantNew bool
	}{
		{name: "older duplicate", prompt: "current prompt", at: promptAt.Add(-time.Minute)},
		{name: "older different prompt", prompt: "old prompt", at: promptAt.Add(-time.Minute)},
		{name: "exact duplicate", prompt: "current prompt", at: promptAt},
		{name: "equal time different prompt", prompt: "new prompt", at: promptAt, wantNew: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, st, _ := newManager()
			rec := working("mer-1")
			rec.Metadata.RuntimeLaunchID = "launch-current"
			rec.Metadata.AgentSessionID = "native-current"
			rec.Metadata.AgentSessionIDLaunchID = "launch-current"
			st.sessions[rec.ID] = rec
			for _, signal := range []ports.ActivitySignal{
				{Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit",
					LaunchID: "launch-current", AgentSessionID: "native-current",
					LatestUserPrompt: "current prompt", Timestamp: promptAt},
				{Valid: true, State: domain.ActivityIdle, Event: "stop",
					LaunchID: "launch-current", AgentSessionID: "native-current",
					LatestAssistantUpdate: "current answer", Timestamp: promptAt.Add(time.Second)},
			} {
				if err := m.ApplyActivitySignal(ctx, rec.ID, signal); err != nil {
					t.Fatal(err)
				}
			}
			before, _, err := st.GetSession(ctx, rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
				Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit",
				LaunchID: "launch-current", AgentSessionID: "native-current",
				LatestUserPrompt: tt.prompt, Timestamp: tt.at,
			}); err != nil {
				t.Fatal(err)
			}
			got, _, err := st.GetSession(ctx, rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Metadata.LatestUserPromptAt.Equal(promptAt) {
				t.Fatalf("last human message time regressed to %s, want %s", got.Metadata.LatestUserPromptAt, promptAt)
			}
			if tt.wantNew {
				if got.Metadata.LatestUserPrompt != "new prompt" || got.Metadata.LatestAssistantUpdate != "" ||
					got.Metadata.ConversationCheckpointState != domain.ConversationCheckpointPrompt {
					t.Fatalf("equal-time new prompt lost its own boundary: %+v", got.Metadata)
				}
			} else {
				if tt.prompt != before.Metadata.LatestUserPrompt {
					before.Metadata.ConversationCheckpointUnsettled = true
				}
				if got.Metadata != before.Metadata {
					t.Fatalf("delayed or duplicate prompt replaced newer checkpoint or lost ordering uncertainty: got %+v, want %+v", got.Metadata, before.Metadata)
				}
			}
		})
	}
}

func TestActivity_MetadataOnlyConfirmsIdentityWithoutCreatingActivityReceipt(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-1"
	rec.FirstSignalAt = time.Time{}
	st.sessions["mer-1"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		Event: "session-start", LaunchID: "launch-1", AgentSessionID: "native-session-1",
	}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if got.Metadata.AgentSessionID != "native-session-1" || got.Metadata.AgentSessionIDLaunchID != "launch-1" {
		t.Fatalf("metadata-only hook did not confirm current native identity: %+v", got.Metadata)
	}
	if !got.FirstSignalAt.IsZero() {
		t.Fatalf("metadata-only hook created an activity receipt: %v", got.FirstSignalAt)
	}
	if got.Activity != rec.Activity {
		t.Fatalf("metadata-only hook changed activity: got %+v, want %+v", got.Activity, rec.Activity)
	}
}

func TestActivity_SameStateSignalStillStoresAgentSessionID(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-1"
	rec.FirstSignalAt = time.Now().Add(-time.Minute)
	st.sessions["mer-1"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		Valid:          true,
		State:          rec.Activity.State,
		LaunchID:       "launch-1",
		AgentSessionID: "native-session-1",
	}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if got.Metadata.AgentSessionID != "native-session-1" {
		t.Fatalf("AgentSessionID = %q, want native-session-1", got.Metadata.AgentSessionID)
	}
	if got.Metadata.AgentSessionIDLaunchID != "launch-1" {
		t.Fatalf("AgentSessionIDLaunchID = %q, want launch-1", got.Metadata.AgentSessionIDLaunchID)
	}
	if got.Activity != rec.Activity {
		t.Fatalf("same-state metadata signal changed activity: got %+v, want %+v", got.Activity, rec.Activity)
	}
}

func TestActivity_TerminalReconciliationRequiresUnchangedSnapshot(t *testing.T) {
	m, st, _ := newManager()
	updatedAt := time.Unix(100, 0).UTC()
	rec := working("mer-1")
	rec.Kind = domain.KindWorker
	rec.FirstSignalAt = updatedAt
	rec.UpdatedAt = updatedAt
	st.sessions[rec.ID] = rec
	staleRevision := rec.Revision - 1

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid:            true,
		State:            domain.ActivityIdle,
		Event:            "terminal-idle",
		ExpectedRevision: &staleRevision,
	}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[rec.ID].Activity.State; got != domain.ActivityActive {
		t.Fatalf("stale reconciliation changed activity to %q", got)
	}

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid:            true,
		State:            domain.ActivityIdle,
		Event:            "terminal-idle",
		ExpectedRevision: &rec.Revision,
	}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[rec.ID].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("current reconciliation left activity %q", got)
	}

	idleRevision := st.sessions[rec.ID].Revision
	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid:            true,
		State:            domain.ActivityActive,
		Event:            "terminal-active",
		ExpectedRevision: &idleRevision,
	}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[rec.ID].Activity.State; got != domain.ActivityActive {
		t.Fatalf("current idle snapshot did not reconcile to active: %q", got)
	}
}

func TestActivity_RepeatedUserPromptFencesTerminalReconciliation(t *testing.T) {
	m, st, _ := newManager()
	before := time.Unix(100, 0).UTC()
	after := before // Session writes need not move the wall-clock timestamp.
	m.clock = func() time.Time { return after }
	rec := working("mer-1")
	rec.FirstSignalAt = before
	rec.UpdatedAt = before
	st.sessions[rec.ID] = rec

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true,
		State: domain.ActivityActive,
		Event: "user-prompt-submit",
	}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[rec.ID].UpdatedAt; !got.Equal(after) {
		t.Fatalf("updated at = %v, want %v", got, after)
	}

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid:            true,
		State:            domain.ActivityIdle,
		Event:            "terminal-idle",
		ExpectedRevision: &rec.Revision,
	}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[rec.ID].Activity.State; got != domain.ActivityActive {
		t.Fatalf("fresh prompt was overwritten with %q", got)
	}
}

func TestActivity_BlankAgentSessionIDDoesNotOverwriteMetadata(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.AgentSessionID = "existing-native-1"
	st.sessions["mer-1"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		Valid:          true,
		State:          domain.ActivityActive,
		AgentSessionID: "   ",
	}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].Metadata.AgentSessionID; got != "existing-native-1" {
		t.Fatalf("AgentSessionID = %q, want existing-native-1", got)
	}
}

func TestActivity_MissingSessionReturnsNotFound(t *testing.T) {
	m, _, _ := newManager()
	err := m.ApplyActivitySignal(ctx, "missing-1", ports.ActivitySignal{Valid: true, State: domain.ActivityWaitingInput})
	if !errors.Is(err, ports.ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestActivity_ExitedPreservesLiveSessionAndRejectsDelayedHooks(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.FirstSignalAt = time.Now().Add(-time.Minute)
	st.sessions["mer-1"] = rec
	m.flights["mer-1"] = &toolFlight{inflight: map[string]string{"tool-1": "Bash"}, blockedCandidate: "tool-1"}

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityExited}); err != nil {
		t.Fatal(err)
	}
	exited := st.sessions["mer-1"]
	if exited.IsTerminated || exited.Activity.State != domain.ActivityExited {
		t.Fatalf("agent exit should preserve live session, got %+v", exited)
	}
	if _, ok := m.flights["mer-1"]; ok {
		t.Fatal("tool-flight state leaked after agent exit")
	}

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityActive}); err != nil {
		t.Fatal(err)
	}
	stillExited := st.sessions["mer-1"]
	if stillExited.IsTerminated || stillExited.Activity.State != domain.ActivityExited {
		t.Fatalf("delayed active signal resurrected exited launch: %+v", stillExited)
	}
}

func TestActivity_UserPromptResumesExitedWorkload(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityExited
	rec.Metadata.RuntimeLaunchID = "launch-2"
	st.sessions["mer-1"] = rec
	signalAt := time.Unix(123, 0).UTC()

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		Valid:     true,
		State:     domain.ActivityActive,
		Event:     "user-prompt-submit",
		Timestamp: signalAt,
		LaunchID:  "launch-2",
	}); err != nil {
		t.Fatal(err)
	}

	got := st.sessions["mer-1"]
	if got.IsTerminated || got.Activity.State != domain.ActivityActive {
		t.Fatalf("valid prompt did not resume exited workload: %+v", got)
	}
	if !got.Activity.LastActivityAt.Equal(signalAt) {
		t.Fatalf("last activity = %v, want %v", got.Activity.LastActivityAt, signalAt)
	}
}

func TestActivity_StaleUserPromptDoesNotResumeExitedWorkload(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityExited
	rec.Metadata.RuntimeLaunchID = "launch-2"
	st.sessions["mer-1"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		Valid:    true,
		State:    domain.ActivityActive,
		Event:    "user-prompt-submit",
		LaunchID: "launch-1",
	}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"]; !reflect.DeepEqual(got, rec) {
		t.Fatalf("stale prompt resumed exited workload: %+v", got)
	}
}

func TestActivity_CurrentChatControllerResumesExitedWorkload(t *testing.T) {
	signals := []ports.ActivitySignal{
		{Valid: true, State: domain.ActivityActive, Event: "chat.turn.started", ControllerGeneration: "gen-current"},
		{Valid: true, State: domain.ActivityIdle, Event: "chat.turn.completed", ControllerGeneration: "gen-current"},
		{Valid: true, State: domain.ActivityWaitingInput, Event: "chat.input.requested", ControllerGeneration: "gen-current"},
	}
	for _, signal := range signals {
		m, st, _ := newManager()
		st.sessions["mer-1"] = domain.SessionRecord{
			ID: "mer-1", ProjectID: "mer", Mode: domain.SessionModeChat,
			Metadata: domain.SessionMetadata{ControllerGeneration: "gen-current"},
			Activity: domain.Activity{State: domain.ActivityExited},
		}
		if err := m.ApplyActivitySignal(ctx, "mer-1", signal); err != nil {
			t.Fatalf("event %q: %v", signal.Event, err)
		}
		if got := st.sessions["mer-1"].Activity.State; got != signal.State {
			t.Fatalf("event %q left state %q, want %q", signal.Event, got, signal.State)
		}
	}
}

func TestActivity_LivenessProbeResumesExitedWorkload(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityExited
	rec.Metadata.RuntimeLaunchID = "launch-2"
	st.sessions["mer-1"] = rec
	signalAt := time.Unix(123, 0).UTC()

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		Valid:     true,
		State:     domain.ActivityIdle,
		Event:     "liveness-probe",
		Timestamp: signalAt,
		LaunchID:  "launch-2",
	}); err != nil {
		t.Fatal(err)
	}

	got := st.sessions["mer-1"]
	if got.IsTerminated || got.Activity.State != domain.ActivityIdle {
		t.Fatalf("liveness probe did not reconcile exited workload: %+v", got)
	}
	if !got.Activity.LastActivityAt.Equal(signalAt) {
		t.Fatalf("last activity = %v, want %v", got.Activity.LastActivityAt, signalAt)
	}
}

func TestActivity_StaleLaunchSignalIsIgnored(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-2"
	st.sessions["mer-1"] = rec
	before := st.sessions["mer-1"]
	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityExited, LaunchID: "launch-1"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.sessions["mer-1"], before) {
		t.Fatalf("stale process exit mutated session: %+v", st.sessions["mer-1"])
	}
}

func TestActivity_NewLaunchSignalWaitsForMarkSpawned(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityExited
	rec.Metadata.RuntimeLaunchID = "launch-old"
	st.sessions["mer-1"] = rec

	if err := m.PrepareLaunch("mer-1", "launch-new"); err != nil {
		t.Fatal(err)
	}
	signalAt := time.Unix(123, 0).UTC()
	signalDone := make(chan error, 1)
	go func() {
		signalDone <- m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
			Valid:     true,
			State:     domain.ActivityActive,
			Timestamp: signalAt,
			LaunchID:  "launch-new",
		})
	}()

	select {
	case err := <-signalDone:
		t.Fatalf("new-generation signal completed before MarkSpawned: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	if err := m.MarkSpawned(ctx, "mer-1", domain.SessionMetadata{
		RuntimeHandleID: "tmux-mer-1",
		RuntimeLaunchID: "launch-new",
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-signalDone; err != nil {
		t.Fatal(err)
	}

	got := st.sessions["mer-1"]
	if got.IsTerminated || got.Activity.State != domain.ActivityActive {
		t.Fatalf("early signal was not applied after spawn commit: %+v", got)
	}
	if got.Metadata.RuntimeLaunchID != "launch-new" {
		t.Fatalf("runtime launch id = %q, want launch-new", got.Metadata.RuntimeLaunchID)
	}
	if !got.FirstSignalAt.Equal(signalAt) {
		t.Fatalf("first signal at = %v, want %v", got.FirstSignalAt, signalAt)
	}
}

func TestActivity_CancelledLaunchReleasesAndRejectsEarlySignal(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityExited
	rec.Metadata.RuntimeLaunchID = "launch-old"
	st.sessions["mer-1"] = rec

	if err := m.PrepareLaunch("mer-1", "launch-new"); err != nil {
		t.Fatal(err)
	}
	signalDone := make(chan error, 1)
	go func() {
		signalDone <- m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
			Valid:    true,
			State:    domain.ActivityActive,
			LaunchID: "launch-new",
		})
	}()

	m.CancelLaunch("mer-1", "launch-new")
	if err := <-signalDone; err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"]; !reflect.DeepEqual(got, rec) {
		t.Fatalf("cancelled launch signal mutated durable state: %+v", got)
	}
}

func TestPrepareLaunchRejectsOverlappingGeneration(t *testing.T) {
	m, _, _ := newManager()
	if err := m.PrepareLaunch("mer-1", "launch-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.PrepareLaunch("mer-1", "launch-1"); err != nil {
		t.Fatalf("same generation should be idempotent: %v", err)
	}
	if err := m.PrepareLaunch("mer-1", "launch-2"); err == nil {
		t.Fatal("overlapping generation was accepted")
	}
	m.CancelLaunch("mer-1", "launch-1")
}

func TestActivity_MainPromptStartsNewConversationCheckpoint(t *testing.T) {
	m, store, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-current"
	rec.Metadata.AgentSessionID = "native-current"
	rec.Metadata.AgentSessionIDLaunchID = "launch-current"
	rec.Metadata.LatestUserPrompt = "previous prompt"
	rec.Metadata.LatestAssistantUpdate = "previous answer"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	rec.Metadata.ConversationCheckpointGeneration = "launch-current"
	rec.Metadata.ConversationCheckpointNativeID = "native-current"
	rec.Metadata.ConversationCheckpointUnsettled = true
	store.sessions[rec.ID] = rec

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit",
		LaunchID: "launch-current", AgentSessionID: "native-current",
		LatestUserPrompt: "current prompt", LatestAssistantUpdate: "stale payload alias",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal prompt: %v", err)
	}
	got := store.sessions[rec.ID].Metadata
	if got.LatestUserPrompt != "current prompt" || got.LatestAssistantUpdate != "" {
		t.Fatalf("checkpoint after prompt = user:%q assistant:%q, want current prompt with no prior-turn answer",
			got.LatestUserPrompt, got.LatestAssistantUpdate)
	}
	if got.ConversationCheckpointState != domain.ConversationCheckpointPrompt ||
		got.ConversationCheckpointGeneration != "launch-current" ||
		got.ConversationCheckpointNativeID != "native-current" ||
		got.ConversationCheckpointUnsettled {
		t.Fatalf("prompt provenance = state:%q generation:%q native:%q", got.ConversationCheckpointState,
			got.ConversationCheckpointGeneration, got.ConversationCheckpointNativeID)
	}

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop",
		LaunchID: "launch-current", AgentSessionID: "native-current",
		LatestUserPrompt: "stale payload alias", LatestAssistantUpdate: "current answer",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal stop: %v", err)
	}
	got = store.sessions[rec.ID].Metadata
	if got.LatestUserPrompt != "current prompt" || got.LatestAssistantUpdate != "current answer" {
		t.Fatalf("completed checkpoint = user:%q assistant:%q", got.LatestUserPrompt, got.LatestAssistantUpdate)
	}
	if got.ConversationCheckpointState != domain.ConversationCheckpointComplete {
		t.Fatalf("completed checkpoint state = %q, want complete", got.ConversationCheckpointState)
	}
}

func TestActivity_CodexCheckpointRequiresSameTurnStop(t *testing.T) {
	for _, stopTurn := range []string{"native-turn", "older-turn", ""} {
		t.Run("stop="+stopTurn, func(t *testing.T) {
			m, store, _ := newManager()
			rec := working("mer-1")
			rec.Harness = domain.HarnessCodex
			rec.Metadata.RuntimeLaunchID = "launch-current"
			rec.Metadata.AgentSessionID = "native-current"
			rec.Metadata.AgentSessionIDLaunchID = "launch-current"
			store.sessions[rec.ID] = rec
			prompt := ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit",
				LaunchID: "launch-current", AgentSessionID: "native-current", ProviderTurnID: "native-turn", LatestUserPrompt: "continue"}
			if err := m.ApplyActivitySignal(ctx, rec.ID, prompt); err != nil {
				t.Fatal(err)
			}
			got := store.sessions[rec.ID].Metadata
			if got.ConversationCheckpointState != domain.ConversationCheckpointPrompt || got.ConversationCheckpointTurnID != "native-turn" {
				t.Fatalf("prompt checkpoint: %+v", got)
			}
			if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{Valid: true, State: domain.ActivityIdle, Event: "stop",
				LaunchID: "launch-current", AgentSessionID: "native-current", ProviderTurnID: stopTurn}); err != nil {
				t.Fatal(err)
			}
			got = store.sessions[rec.ID].Metadata
			if stopTurn == "native-turn" {
				if got.ConversationCheckpointState != domain.ConversationCheckpointComplete || got.ConversationCheckpointUnsettled {
					t.Fatalf("matching Stop checkpoint: %+v", got)
				}
			} else if got.ConversationCheckpointState != domain.ConversationCheckpointPrompt || !got.ConversationCheckpointUnsettled {
				t.Fatalf("unmatched Stop must fail closed: %+v", got)
			}
		})
	}
}

func TestActivity_MainPromptWithoutTextInvalidatesPriorConversationCheckpoint(t *testing.T) {
	m, store, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-current"
	rec.Metadata.AgentSessionID = "native-current"
	rec.Metadata.AgentSessionIDLaunchID = "launch-current"
	rec.Metadata.LatestUserPrompt = "previous prompt"
	rec.Metadata.LatestAssistantUpdate = "previous answer"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	rec.Metadata.ConversationCheckpointGeneration = "launch-current"
	rec.Metadata.ConversationCheckpointNativeID = "native-current"
	store.sessions[rec.ID] = rec

	// The event boundary is authoritative even when a provider omits the prompt
	// text. Retaining the previous completed pair would make it look like the new
	// turn never started and could permanently gate replay on stale history.
	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit",
		LaunchID: "launch-current", AgentSessionID: "native-current",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal: %v", err)
	}
	got := store.sessions[rec.ID].Metadata
	if got.LatestUserPrompt != "" || got.LatestAssistantUpdate != "" {
		t.Fatalf("prior checkpoint survived a new main-turn boundary without text: %+v", got)
	}
	if got.ConversationCheckpointState != domain.ConversationCheckpointPrompt {
		t.Fatalf("missing-text prompt state = %q, want prompt", got.ConversationCheckpointState)
	}
}

func TestActivity_CoordinationPromptFollowedByPromptlessStopDoesNotAdvanceCheckpoint(t *testing.T) {
	m, store, _ := newManager()
	previousPromptAt := time.Unix(122, 0).UTC()
	coordinationPromptAt := previousPromptAt.Add(time.Minute)
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "terminal-generation"
	rec.Metadata.AgentSessionID = "native-1"
	rec.Metadata.AgentSessionIDLaunchID = "terminal-generation"
	rec.Metadata.LatestUserPrompt = "last real user direction"
	rec.Metadata.LatestUserPromptAt = previousPromptAt
	rec.Metadata.LatestAssistantUpdate = "last real assistant update"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	rec.Metadata.ConversationCheckpointGeneration = "terminal-generation"
	rec.Metadata.ConversationCheckpointNativeID = "native-1"
	store.sessions[rec.ID] = rec

	// The hook client recognizes Tallpine's continuation kickoff and therefore omits
	// its text. Lifecycle must still carry that ineligible turn boundary across
	// to the provider's later Stop, which need not echo the prompt.
	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit",
		LaunchID: "terminal-generation", AgentSessionID: "native-1",
		Timestamp:                    coordinationPromptAt,
		ConversationCheckpointOrigin: domain.ConversationCheckpointOriginCoordination,
		CoordinationID:               "report-batch:abc123",
	}); err != nil {
		t.Fatalf("apply coordination prompt boundary: %v", err)
	}
	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop",
		LaunchID: "terminal-generation", AgentSessionID: "native-1",
		LatestAssistantUpdate: "Tallpine continuation acknowledged",
		Timestamp:             coordinationPromptAt.Add(time.Second),
	}); err != nil {
		t.Fatalf("apply promptless coordination Stop: %v", err)
	}

	got := store.sessions[rec.ID].Metadata
	if got.LatestUserPrompt != rec.Metadata.LatestUserPrompt ||
		!got.LatestUserPromptAt.Equal(previousPromptAt) ||
		got.LatestAssistantUpdate != rec.Metadata.LatestAssistantUpdate ||
		got.ConversationCheckpointState != domain.ConversationCheckpointCoordination ||
		got.ConversationCheckpointTurnID != "report-batch:abc123" {
		t.Fatalf("coordination turn advanced user checkpoint: got %+v, want prior human facts at %s",
			got, previousPromptAt)
	}
}

func TestActivity_StopWithoutCurrentPromptNeverPairsWithPriorTurn(t *testing.T) {
	m, store, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-current"
	rec.Metadata.AgentSessionID = "native-current"
	rec.Metadata.AgentSessionIDLaunchID = "launch-current"
	rec.Metadata.LatestUserPrompt = "prior turn prompt"
	rec.Metadata.LatestUserPromptAt = time.Unix(122, 0).UTC()
	rec.Metadata.LatestAssistantUpdate = "prior turn answer"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	rec.Metadata.ConversationCheckpointGeneration = "launch-current"
	rec.Metadata.ConversationCheckpointNativeID = "native-current"
	store.sessions[rec.ID] = rec

	// The current turn's UserPromptSubmit was lost. Stop must not combine its
	// assistant with the only prompt Tallpine has, which belongs to the prior turn. The
	// older coherent checkpoint stays intact while a hard unresolved-boundary
	// witness records that replay cannot safely stop there.
	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop",
		LaunchID: "launch-current", AgentSessionID: "native-current",
		LatestAssistantUpdate: "new turn answer with missing prompt boundary",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal stop: %v", err)
	}
	got := store.sessions[rec.ID].Metadata
	if got.LatestUserPrompt != "prior turn prompt" || !got.LatestUserPromptAt.Equal(rec.Metadata.LatestUserPromptAt) ||
		got.LatestAssistantUpdate != "prior turn answer" ||
		got.ConversationCheckpointState != domain.ConversationCheckpointComplete ||
		got.ConversationCheckpointGeneration != "launch-current" ||
		got.ConversationCheckpointNativeID != "native-current" ||
		!got.ConversationCheckpointUnsettled {
		t.Fatalf("out-of-order Stop did not retain unresolved boundary: %+v", got)
	}
}

func TestActivity_NonBoundaryEventCannotReplaceConversationCheckpoint(t *testing.T) {
	m, store, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-current"
	rec.Metadata.AgentSessionID = "native-current"
	rec.Metadata.AgentSessionIDLaunchID = "launch-current"
	rec.Metadata.LatestUserPrompt = "trusted prompt"
	rec.Metadata.LatestUserPromptAt = time.Unix(123, 0).UTC()
	rec.Metadata.LatestAssistantUpdate = "trusted answer"
	store.sessions[rec.ID] = rec

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, Event: "post-tool-use",
		LaunchID: "launch-current", AgentSessionID: "native-current",
		LatestUserPrompt: "repeated payload alias", LatestAssistantUpdate: "tool output alias",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal: %v", err)
	}
	got := store.sessions[rec.ID].Metadata
	if got.LatestUserPrompt != "trusted prompt" || !got.LatestUserPromptAt.Equal(rec.Metadata.LatestUserPromptAt) ||
		got.LatestAssistantUpdate != "trusted answer" {
		t.Fatalf("non-boundary event replaced the trusted checkpoint: %+v", got)
	}
}

func TestActivity_NewNativeConversationClearsPriorConversationCheckpoint(t *testing.T) {
	m, store, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-current"
	rec.Metadata.AgentSessionID = "native-old"
	rec.Metadata.AgentSessionIDLaunchID = "launch-current"
	rec.Metadata.LatestUserPrompt = "old conversation prompt"
	rec.Metadata.LatestAssistantUpdate = "old conversation answer"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	store.sessions[rec.ID] = rec

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		LaunchID: "launch-current", AgentSessionID: "native-new",
		TranscriptPath: "/tmp/native-new.jsonl",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal: %v", err)
	}
	got := store.sessions[rec.ID].Metadata
	if got.AgentSessionID != "native-new" || got.AgentSessionIDLaunchID != "launch-current" {
		t.Fatalf("native identity = id:%q launch:%q", got.AgentSessionID, got.AgentSessionIDLaunchID)
	}
	if got.LatestUserPrompt != "" || got.LatestAssistantUpdate != "" {
		t.Fatalf("old native conversation checkpoint survived identity change: %+v", got)
	}
	if got.ConversationCheckpointState != domain.ConversationCheckpointEmpty {
		t.Fatalf("new conversation checkpoint state = %q, want empty", got.ConversationCheckpointState)
	}
}

func TestActivity_NewRuntimeLaunchClearsPriorConversationCheckpoint(t *testing.T) {
	m, store, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-current"
	rec.Metadata.AgentSessionID = "native-current"
	rec.Metadata.AgentSessionIDLaunchID = "launch-old"
	rec.Metadata.LatestUserPrompt = "old launch prompt"
	rec.Metadata.LatestAssistantUpdate = "old launch answer"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	rec.Metadata.ConversationCheckpointUnsettled = true
	store.sessions[rec.ID] = rec

	// A SessionStart hook proves that this native conversation belongs to the
	// current runtime launch, but it carries no main-turn checkpoint of its own.
	// Text observed under the prior launch must not become a hard replay gate for
	// an interface switch from this launch.
	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Event: "session-start", LaunchID: "launch-current", AgentSessionID: "native-current",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal: %v", err)
	}
	got := store.sessions[rec.ID].Metadata
	if got.AgentSessionID != "native-current" || got.AgentSessionIDLaunchID != "launch-current" {
		t.Fatalf("native identity = id:%q launch:%q", got.AgentSessionID, got.AgentSessionIDLaunchID)
	}
	if got.LatestUserPrompt != "" || got.LatestAssistantUpdate != "" {
		t.Fatalf("old runtime launch checkpoint survived current-launch identity proof: %+v", got)
	}
	if got.ConversationCheckpointState != domain.ConversationCheckpointEmpty {
		t.Fatalf("new launch checkpoint state = %q, want empty", got.ConversationCheckpointState)
	}
	if !got.ConversationCheckpointUnsettled {
		t.Fatal("same-native runtime restart erased unresolved provider turn boundary")
	}
}

func TestActivity_NewCheckpointEpochKeepsLastHumanTimeMonotonic(t *testing.T) {
	lastHumanAt := time.Unix(456, 0).UTC()
	for _, tt := range []struct {
		name           string
		previousLaunch string
		previousNative string
	}{
		{name: "new native identity", previousLaunch: "launch-current", previousNative: "native-old"},
		{name: "new runtime launch", previousLaunch: "launch-old", previousNative: "native-current"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, st, _ := newManager()
			rec := working("mer-1")
			rec.Metadata = domain.SessionMetadata{
				RuntimeLaunchID: "launch-current", AgentSessionID: tt.previousNative,
				AgentSessionIDLaunchID: tt.previousLaunch, LatestUserPrompt: "old prompt",
				LatestUserPromptAt: lastHumanAt, LatestAssistantUpdate: "old answer",
				ConversationCheckpointState:      domain.ConversationCheckpointComplete,
				ConversationCheckpointGeneration: tt.previousLaunch,
				ConversationCheckpointNativeID:   tt.previousNative,
			}
			st.sessions[rec.ID] = rec
			if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
				Event: "session-start", LaunchID: "launch-current", AgentSessionID: "native-current",
			}); err != nil {
				t.Fatal(err)
			}
			cleared, _, err := st.GetSession(ctx, rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !cleared.Metadata.LatestUserPromptAt.Equal(lastHumanAt) ||
				cleared.Metadata.LatestUserPrompt != "" || cleared.Metadata.LatestAssistantUpdate != "" ||
				cleared.Metadata.ConversationCheckpointState != domain.ConversationCheckpointEmpty {
				t.Fatalf("epoch reset mixed old checkpoint with new owner or lost human time: %+v", cleared.Metadata)
			}
			if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
				Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit",
				LaunchID: "launch-current", AgentSessionID: "native-current",
				LatestUserPrompt: "new owner prompt", Timestamp: lastHumanAt.Add(-time.Minute),
			}); err != nil {
				t.Fatal(err)
			}
			got, _, err := st.GetSession(ctx, rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Metadata.LatestUserPromptAt.Equal(lastHumanAt) ||
				got.Metadata.LatestUserPrompt != "new owner prompt" || got.Metadata.LatestAssistantUpdate != "" ||
				got.Metadata.ConversationCheckpointState != domain.ConversationCheckpointPrompt ||
				got.Metadata.ConversationCheckpointGeneration != "launch-current" ||
				got.Metadata.ConversationCheckpointNativeID != "native-current" {
				t.Fatalf("current owner did not establish an independent checkpoint with monotonic human time: %+v", got.Metadata)
			}
			// The retained time is a session-wide high-water mark, not this
			// owner's clock. A later prompt below it is ordering-ambiguous and
			// its Stop must not validate the previous prompt with a new answer.
			for _, signal := range []ports.ActivitySignal{
				{Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit",
					LaunchID: "launch-current", AgentSessionID: "native-current",
					LatestUserPrompt: "later skewed prompt", Timestamp: lastHumanAt.Add(-time.Second)},
				{Valid: true, State: domain.ActivityIdle, Event: "stop",
					LaunchID: "launch-current", AgentSessionID: "native-current",
					LatestAssistantUpdate: "later answer", Timestamp: lastHumanAt},
			} {
				if err := m.ApplyActivitySignal(ctx, rec.ID, signal); err != nil {
					t.Fatal(err)
				}
			}
			got, _, err = st.GetSession(ctx, rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Metadata.ConversationCheckpointUnsettled ||
				got.Metadata.LatestUserPrompt != "new owner prompt" || got.Metadata.LatestAssistantUpdate != "" ||
				!got.Metadata.LatestUserPromptAt.Equal(lastHumanAt) {
				t.Fatalf("ambiguous prompt's Stop admitted a mixed checkpoint: %+v", got.Metadata)
			}
		})
	}
}

func TestActivity_OldRuntimeGenerationCannotReplaceConversationCheckpoint(t *testing.T) {
	m, store, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-current"
	rec.Metadata.AgentSessionID = "native-current"
	rec.Metadata.AgentSessionIDLaunchID = "launch-current"
	rec.Metadata.LatestUserPrompt = "current prompt"
	rec.Metadata.LatestAssistantUpdate = "current answer"
	store.sessions[rec.ID] = rec

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop",
		LaunchID: "launch-old", AgentSessionID: "native-old",
		LatestAssistantUpdate: "late old-generation answer",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal: %v", err)
	}
	if got := store.sessions[rec.ID]; !reflect.DeepEqual(got, rec) {
		t.Fatalf("old generation mutated current checkpoint: got %+v, want %+v", got, rec)
	}
}

func TestActivity_UntaggedTUIHookCannotMutateLaunchedRuntime(t *testing.T) {
	m, store, _ := newManager()
	rec := working("mer-1")
	rec.Mode = domain.SessionModeTUI
	rec.Metadata.RuntimeLaunchID = "launch-current"
	rec.Metadata.AgentSessionID = "native-current"
	rec.Metadata.AgentSessionIDLaunchID = "launch-current"
	rec.Metadata.LatestUserPrompt = "current prompt"
	rec.Metadata.LatestAssistantUpdate = "current answer"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	rec.Metadata.ConversationCheckpointGeneration = "launch-current"
	rec.Metadata.ConversationCheckpointNativeID = "native-current"
	store.sessions[rec.ID] = rec

	// Once a runtime launch owns the TUI session, an untagged legacy callback
	// cannot prove that it belongs to that generation. A delayed callback from a
	// prior launch must therefore be ignored in its entirety.
	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop",
		AgentSessionID: "native-old", LatestAssistantUpdate: "late untagged answer",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal: %v", err)
	}
	if got := store.sessions[rec.ID]; !reflect.DeepEqual(got, rec) {
		t.Fatalf("untagged callback mutated launched runtime: got %+v, want %+v", got, rec)
	}
}

func TestActivity_SideHeadlessClaudeRunCannotHijackTUIIdentity(t *testing.T) {
	m, store, _ := newManager()
	rec := working("mer-1")
	rec.Mode = domain.SessionModeTUI
	rec.Harness = domain.HarnessClaudeCode
	rec.Metadata.RuntimeLaunchID = "launch-current"
	rec.Metadata.AgentSessionID = "native-main"
	rec.Metadata.AgentSessionIDLaunchID = "launch-current"
	rec.Metadata.NativeTranscriptPath = "/claude/projects/wt/native-main.jsonl"
	store.sessions[rec.ID] = rec

	// A summarizer's `claude -p` in the same worktree inherits TALLPINE_SESSION_ID and
	// the current TALLPINE_RUNTIME_LAUNCH_ID, so the generation fence accepts it. Its
	// sdk-cli entrypoint proves it is not the interactive runtime Tallpine launched:
	// its identity must not replace the main conversation and its SessionEnd
	// must not mark the live session exited (#6454).
	side := []ports.ActivitySignal{
		{Event: "session-start", LaunchID: "launch-current", AgentSessionID: "native-side",
			TranscriptPath: "/claude/projects/wt/native-side.jsonl", NativeEntrypoint: "sdk-cli"},
		{Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit", LaunchID: "launch-current",
			AgentSessionID: "native-side", LatestUserPrompt: "Summarize this agent result", NativeEntrypoint: "sdk-cli"},
		{Valid: true, State: domain.ActivityIdle, Event: "stop", LaunchID: "launch-current",
			AgentSessionID: "native-side", LatestAssistantUpdate: "one line", NativeEntrypoint: "sdk-cli"},
		{Valid: true, State: domain.ActivityExited, Event: "session-end", LaunchID: "launch-current",
			AgentSessionID: "native-side", NativeEntrypoint: "sdk-cli"},
	}
	for _, s := range side {
		s.Timestamp = time.Now().Add(time.Hour)
		if err := m.ApplyActivitySignal(ctx, rec.ID, s); err != nil {
			t.Fatalf("ApplyActivitySignal(%s): %v", s.Event, err)
		}
	}
	if got := store.sessions[rec.ID]; !reflect.DeepEqual(got, rec) {
		t.Fatalf("headless side run mutated TUI session: got %+v, want %+v", got, rec)
	}

	// The interactive runtime's own hooks keep their existing semantics.
	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop", LaunchID: "launch-current",
		AgentSessionID: "native-main-cleared", NativeEntrypoint: "cli", Timestamp: time.Now().Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("ApplyActivitySignal(cli): %v", err)
	}
	got := store.sessions[rec.ID]
	if got.Metadata.AgentSessionID != "native-main-cleared" || got.Activity.State != domain.ActivityIdle {
		t.Fatalf("interactive runtime hook rejected: id=%q state=%q", got.Metadata.AgentSessionID, got.Activity.State)
	}
}

func TestActivity_LaunchTaggedTUIStopAfterChatEpochCannotMutateSession(t *testing.T) {
	m, store, _ := newManager()
	rec := working("mer-1")
	rec.Mode = domain.SessionModeChat
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	rec.Metadata.ProviderConversationID = "native-current"
	rec.Metadata.AgentSessionID = "native-current"
	rec.Metadata.LatestUserPrompt = "trusted terminal prompt"
	rec.Metadata.LatestAssistantUpdate = "trusted terminal answer"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	rec.Metadata.ConversationCheckpointGeneration = "tui-launch"
	rec.Metadata.ConversationCheckpointNativeID = "native-current"
	store.sessions[rec.ID] = rec

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop", LaunchID: "tui-launch",
		AgentSessionID: "native-current", LatestAssistantUpdate: "late terminal stop",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal: %v", err)
	}
	if got := store.sessions[rec.ID]; !reflect.DeepEqual(got, rec) {
		t.Fatalf("late TUI Stop mutated Chat owner: got %+v, want %+v", got, rec)
	}
}

func TestActivity_UntaggedTUIStopAfterChatEpochCannotMutateSession(t *testing.T) {
	m, store, _ := newManager()
	rec := working("mer-1")
	rec.Mode = domain.SessionModeChat
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	rec.Metadata.ProviderConversationID = "native-current"
	rec.Metadata.ControllerGeneration = "chat-current"
	rec.Metadata.AgentSessionID = "native-current"
	rec.Metadata.LatestUserPrompt = "trusted terminal prompt"
	rec.Metadata.LatestAssistantUpdate = "trusted terminal answer"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	rec.Metadata.ConversationCheckpointGeneration = "tui-launch"
	rec.Metadata.ConversationCheckpointNativeID = "native-current"
	store.sessions[rec.ID] = rec

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop",
		AgentSessionID: "native-current", LatestAssistantUpdate: "late untagged terminal stop",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal: %v", err)
	}
	if got := store.sessions[rec.ID]; !reflect.DeepEqual(got, rec) {
		t.Fatalf("untagged late TUI Stop mutated Chat owner: got %+v, want %+v", got, rec)
	}
}

func TestActivitySignalRejectsStaleChatControllerGenerationAcrossHandoff(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{ControllerGeneration: "chat-generation-2"},
		Activity: domain.Activity{State: domain.ActivityIdle},
	}
	m := New(st, nil)

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, ControllerGeneration: "chat-generation-1",
	}); err != nil {
		t.Fatalf("stale signal: %v", err)
	}
	if got := st.sessions["mer-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("stale generation changed activity to %q", got)
	}

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, ControllerGeneration: "chat-generation-2",
	}); err != nil {
		t.Fatalf("current signal: %v", err)
	}
	if got := st.sessions["mer-1"].Activity.State; got != domain.ActivityActive {
		t.Fatalf("current generation left activity at %q", got)
	}

	rec := st.sessions["mer-1"]
	rec.Mode = domain.SessionModeTUI
	rec.Activity.State = domain.ActivityIdle
	st.sessions["mer-1"] = rec
	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, ControllerGeneration: "chat-generation-2",
	}); err != nil {
		t.Fatalf("post-handoff stale signal: %v", err)
	}
	if got := st.sessions["mer-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("old Chat controller changed TUI activity to %q", got)
	}
}

func TestActivitySignalFencesChatByControllerGenerationDespiteStaleRuntimeMetadata(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{
			RuntimeHandleID:                  "stale-tui-runtime",
			RuntimeLaunchID:                  "stale-tui-generation",
			ControllerGeneration:             "chat-generation",
			AgentSessionID:                   "native-current",
			LatestUserPrompt:                 "trusted terminal prompt",
			LatestAssistantUpdate:            "trusted terminal answer",
			ConversationCheckpointState:      domain.ConversationCheckpointComplete,
			ConversationCheckpointGeneration: "stale-tui-generation",
			ConversationCheckpointNativeID:   "native-current",
		},
		Activity: domain.Activity{State: domain.ActivityIdle},
	}
	m := New(st, nil)

	for _, signal := range []ports.ActivitySignal{
		{Valid: true, State: domain.ActivityActive, LaunchID: "stale-tui-generation"},
		{Valid: true, State: domain.ActivityActive, ControllerGeneration: "foreign-generation"},
		{Valid: true, State: domain.ActivityIdle, Event: "stop", LaunchID: "stale-tui-generation", AgentSessionID: "native-current", LatestAssistantUpdate: "late terminal answer"},
	} {
		if err := m.ApplyActivitySignal(ctx, "mer-1", signal); err != nil {
			t.Fatalf("reject non-owner signal: %v", err)
		}
		if got := st.sessions["mer-1"].Activity.State; got != domain.ActivityIdle {
			t.Fatalf("non-owner signal changed activity to %q", got)
		}
		if got := st.sessions["mer-1"].Metadata.LatestAssistantUpdate; got != "trusted terminal answer" {
			t.Fatalf("non-owner signal changed checkpoint to %q", got)
		}
	}

	for _, state := range []domain.ActivityState{domain.ActivityActive, domain.ActivityIdle} {
		if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
			Valid: true, State: state, ControllerGeneration: "chat-generation",
		}); err != nil {
			t.Fatalf("apply current Chat signal %q: %v", state, err)
		}
		if got := st.sessions["mer-1"].Activity.State; got != state {
			t.Fatalf("current Chat signal left activity at %q, want %q", got, state)
		}
	}
}

func TestActivitySignalRetryPreservesCursorPermissionCorrelation(t *testing.T) {
	st := &activityRevisionConflictStore{fakeStore: newFakeStore()}
	seedSignaled(st.fakeStore, "mer-1", domain.ActivityActive)
	m := New(st, nil)
	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "before-shell-execution", "git push", ""))

	// Finishing the approved tool removes its in-memory correlation before the
	// durable write. A revision miss must restore that correlation for the retry,
	// or the real completion is suppressed and the UI remains stuck on approval.
	st.conflictNext = true
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "after-shell-execution", "git push", ""))
	if st.conflictNext {
		t.Fatal("test did not exercise a conflicting activity projection")
	}
	if got := stateOf(st.fakeStore, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after approved tool and revision retry = %q, want active", got)
	}
}

func TestScopedPostAgentRefreshesIdleProofWithoutStateTransition(t *testing.T) {
	for _, tc := range []struct {
		name, launch, native string
		newer, wantAdvance   bool
	}{
		{"completed turn", "launch-1", "native-1", true, true},
		{"unscoped hook", "", "native-1", true, false},
		{"stale generation", "launch-old", "native-1", true, false},
		{"missing native identity", "launch-1", "", true, false},
		{"duplicate boundary", "launch-1", "native-1", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _ := newManager()
			at := time.Now().Add(-time.Minute)
			rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Harness: domain.AgentHarness("vibe"), Mode: domain.SessionModeTUI,
				Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: at}, FirstSignalAt: at,
				Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1", AgentSessionID: "native-1", AgentSessionIDLaunchID: "launch-1"}}
			st.sessions["mer-1"] = rec
			observed := at
			if tc.newer {
				observed = at.Add(time.Second)
			}
			err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle, Event: "post-agent", LaunchID: tc.launch, AgentSessionID: tc.native, Timestamp: observed})
			if err != nil {
				t.Fatal(err)
			}
			got := st.sessions["mer-1"].Activity
			if got.State != domain.ActivityIdle {
				t.Fatalf("state = %q", got.State)
			}
			want := at
			if tc.wantAdvance {
				want = observed
			}
			if !got.LastActivityAt.Equal(want) {
				t.Fatalf("idle proof = %v, want %v", got.LastActivityAt, want)
			}
		})
	}
}
