package sessionmanager

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/lifecycle"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

func TestSend_WritesAttachmentAndAppendsReference(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st := newFakeStore()
	st.sessions["mer-1"] = pastStartupGate(domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Metadata:  domain.SessionMetadata{WorkspacePath: dir},
	})
	msg := &fakeMessenger{}
	ws := &fakeWorkspace{}
	m := New(Deps{Store: st, Messenger: msg, Workspace: ws, DataDir: t.TempDir()})

	attachment := &ports.SpawnAttachment{Ext: ".png", Data: []byte("snapshot-bytes")}
	if err := m.SendWithOptions(ctx, "mer-1", "Make the button blue.", attachment, ports.MessageDeliveryOptions{AuthoredByUser: true}); err != nil {
		t.Fatal(err)
	}

	if len(msg.msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msg.msgs))
	}
	got := msg.msgs[0]
	if !strings.HasPrefix(got, "Make the button blue.\n\n") {
		t.Fatalf("delivered message lost the original text: %q", got)
	}
	if !strings.Contains(got, "Attached files (read these files in the workspace for context):") {
		t.Fatalf("delivered message missing attachment reference block: %q", got)
	}

	// The referenced path in the delivered message must be the file actually
	// written to disk, not just well-formed text.
	refLine := ""
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "- .tallpine/attachments/attachment-") {
			refLine = strings.TrimPrefix(line, "- ")
		}
	}
	if refLine == "" {
		t.Fatalf("no attachment reference line found in: %q", got)
	}
	writtenBytes, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(refLine)))
	if err != nil {
		t.Fatalf("read written attachment: %v", err)
	}
	if string(writtenBytes) != "snapshot-bytes" {
		t.Errorf("written attachment content = %q, want %q", writtenBytes, "snapshot-bytes")
	}

	if len(ws.calls) == 0 || !strings.Contains(ws.calls[0], "AddExclude") {
		t.Errorf("workspace calls = %v, want AddExclude", ws.calls)
	}
}

func TestSend_WithoutAttachmentSkipsWorkspaceWrite(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = pastStartupGate(domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker})
	msg := &fakeMessenger{}
	ws := &fakeWorkspace{}
	m := New(Deps{Store: st, Messenger: msg, Workspace: ws})

	if err := m.SendWithOptions(ctx, "mer-1", "make the button red", nil, ports.MessageDeliveryOptions{AuthoredByUser: true}); err != nil {
		t.Fatal(err)
	}

	if got := msg.msgs[0]; got != "make the button red" {
		t.Fatalf("message = %q, want unchanged original", got)
	}
	if len(ws.calls) != 0 {
		t.Errorf("workspace calls = %v, want none when no attachment is sent", ws.calls)
	}
}

// A session with no WorkspacePath has nowhere safe to write an attachment:
// StageAttachments' filepath.Join would otherwise produce a relative
// ".tallpine/attachments" path, writing beneath the daemon's own working directory
// instead of the session's worktree and handing the agent a reference it
// cannot reach. Send must refuse rather than silently mis-deliver.
func TestSend_RejectsAttachmentWithEmptyWorkspace(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	msg := &fakeMessenger{}
	ws := &fakeWorkspace{}
	m := New(Deps{Store: st, Messenger: msg, Workspace: ws})

	attachment := &ports.SpawnAttachment{Ext: ".png", Data: []byte("snapshot-bytes")}
	err := m.Send(ctx, "mer-1", "Make the button blue.", attachment)
	if err == nil {
		t.Fatal("want an error for a session with no workspace, got nil")
	}
	if len(msg.msgs) != 0 {
		t.Errorf("messenger calls = %v, want none: the send must not proceed after the attachment write fails", msg.msgs)
	}
}

func TestSend_SkipsConfirmForHooklessHarness(t *testing.T) {
	t.Parallel()

	// A harness whose adapter does NOT implement the activity-signal interfaces (plain
	// fakeAgent) must skip confirmActive entirely: one Send, no nudges, and the
	// call returns immediately without polling.
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code"})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, fakeAgent{}, msg, st)

	start := time.Now()
	if err := m.Send(context.Background(), "s1", "hello", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("Send calls = %d, want 1 (no nudges for a hookless harness)", len(msg.msgs))
	}
	// Hookless path returns within milliseconds (no 2s+ confirmation wait).
	if dt := time.Since(start); dt > 250*time.Millisecond {
		t.Fatalf("Send took %s for a hookless harness; confirmActive should have been skipped", dt)
	}
}

func TestSendSemanticTUIRequiresCorrelatedPromptAcceptance(t *testing.T) {
	t.Parallel()

	const deliveryID = "report-batch:abc123"
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{
		ID: "s1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	})
	msg := &fakeMessenger{onSend: func(id domain.SessionID, message string) {
		if message == "" {
			return
		}
		if got, ok := domain.ReportDeliveryID(message); !ok || got != deliveryID {
			t.Fatalf("delivery envelope = %q, %v", got, ok)
		}
		rec := st.sessions[id]
		rec.Activity.State = domain.ActivityActive
		rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointCoordination
		rec.Metadata.ConversationCheckpointGeneration = "launch-1"
		rec.Metadata.ConversationCheckpointTurnID = deliveryID
		st.sessions[id] = rec
	}}
	m := newSendTestManager(t, semanticSignalingAgent{}, msg, st)

	if err := m.SendSemantic(context.Background(), "s1", "Reports since your previous turn:", deliveryID); err != nil {
		t.Fatalf("SendSemantic: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("pane writes = %d, want 1", len(msg.msgs))
	}
	if err := m.SendSemantic(context.Background(), "s1", "retry", deliveryID); err != nil {
		t.Fatalf("idempotent SendSemantic: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("retry pane writes = %d, want 1", len(msg.msgs))
	}
}

func TestSendSemanticTUIDoesNotAcceptPaneWrite(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{
		ID: "s1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, semanticSignalingAgent{}, msg, st)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	err := m.SendSemantic(ctx, "s1", "report", "report-batch:unaccepted")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SendSemantic error = %v, want context deadline", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("pane writes = %d, want 1", len(msg.msgs))
	}
}

func TestSendSemanticTUIRejectsAdapterWithoutAcceptanceSignal(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{
		ID: "s1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle},
	})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, signalingAgent{}, msg, st)

	err := m.SendSemantic(context.Background(), "s1", "report", "report-batch:unsupported")
	if !errors.Is(err, ErrSemanticAcceptanceUnsupported) {
		t.Fatalf("SendSemantic error = %v", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("pane writes = %d, want 0", len(msg.msgs))
	}
}

func TestSend_RecordsDeliveredUserInput(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code"})
	messenger := &fakeMessenger{}
	m := newSendTestManager(t, fakeAgent{}, messenger, st)

	if err := m.SendWithOptions(context.Background(), "s1", "continue with the migration", nil, ports.MessageDeliveryOptions{AuthoredByUser: true}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(messenger.msgs) != 1 || messenger.msgs[0] != "continue with the migration" {
		t.Fatalf("human send was wrapped as coordination: %+v", messenger.msgs)
	}
	if got := st.sessions["s1"].Metadata.LatestUserPrompt; got != "continue with the migration" {
		t.Fatalf("LatestUserPrompt = %q, want delivered user input", got)
	}
	if got := st.sessions["s1"].Metadata.LatestUserPromptAt; got.IsZero() {
		t.Fatal("LatestUserPromptAt is zero after delivered user input")
	}
}

func TestSend_PaneFallbackCannotPairLostPromptHookWithPriorTrustedAssistant(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	st, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID: "proj", Path: "/repo", RegisteredAt: now,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	created, err := st.CreateSession(ctx, domain.SessionRecord{
		ID: "session-1", ProjectID: "proj", Kind: domain.KindWorker,
		Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/session-1", Branch: "tp/session-1",
			RuntimeHandleID: "runtime-1", RuntimeLaunchID: "terminal-generation",
			AgentSessionID: "native-1", AgentSessionIDLaunchID: "terminal-generation",
			LatestUserPrompt: "prior trusted prompt", LatestAssistantUpdate: "prior trusted answer",
			ConversationCheckpointState:      domain.ConversationCheckpointComplete,
			ConversationCheckpointGeneration: "terminal-generation",
			ConversationCheckpointNativeID:   "native-1",
		},
		Activity:      domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		FirstSignalAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}

	lcm := lifecycle.New(st, nil)
	manager := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: fakeAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: lcm,
	})
	if err := manager.SendWithOptions(ctx, created.ID, "new prompt whose UserPromptSubmit hook is lost", nil, ports.MessageDeliveryOptions{AuthoredByUser: true}); err != nil {
		t.Fatalf("send pane prompt: %v", err)
	}
	afterFallback, ok, err := st.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("read fallback checkpoint: ok=%v err=%v", ok, err)
	}
	checkpoint := afterFallback.Metadata
	if checkpoint.LatestUserPrompt != "new prompt whose UserPromptSubmit hook is lost" ||
		checkpoint.LatestAssistantUpdate != "" ||
		checkpoint.ConversationCheckpointState != domain.ConversationCheckpointLegacy ||
		checkpoint.ConversationCheckpointGeneration != "" ||
		checkpoint.ConversationCheckpointNativeID != "" {
		t.Fatalf("pane fallback checkpoint = %+v, want untrusted new prompt with prior assistant/provenance cleared", checkpoint)
	}

	// A delayed Stop from the old trusted turn must not borrow the new pane
	// prompt after its UserPromptSubmit hook was lost.
	if err := lcm.ApplyActivitySignal(ctx, created.ID, ports.ActivitySignal{
		Event: "stop", AgentSessionID: "native-1", LatestAssistantUpdate: "delayed prior answer",
		LaunchID: "terminal-generation", Valid: true, State: domain.ActivityIdle,
	}); err != nil {
		t.Fatalf("apply delayed prior Stop: %v", err)
	}
	afterDelayedStop, ok, err := st.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("read after delayed Stop: ok=%v err=%v", ok, err)
	}
	if afterDelayedStop.Metadata.LatestAssistantUpdate != "" ||
		afterDelayedStop.Metadata.ConversationCheckpointState != domain.ConversationCheckpointLegacy ||
		!afterDelayedStop.Metadata.ConversationCheckpointUnsettled {
		t.Fatalf("delayed Stop paired with pane fallback: %+v", afterDelayedStop.Metadata)
	}

	if err := lcm.ApplyActivitySignal(ctx, created.ID, ports.ActivitySignal{
		Event: "user-prompt-submit", AgentSessionID: "native-1",
		LatestUserPrompt: "later canonical prompt",
		LaunchID:         "terminal-generation", Valid: true, State: domain.ActivityActive,
	}); err != nil {
		t.Fatalf("apply later canonical UserPromptSubmit: %v", err)
	}
	if err := lcm.ApplyActivitySignal(ctx, created.ID, ports.ActivitySignal{
		Event: "stop", AgentSessionID: "native-1", LatestAssistantUpdate: "new trusted answer",
		LaunchID: "terminal-generation", Valid: true, State: domain.ActivityIdle,
	}); err != nil {
		t.Fatalf("apply canonical Stop: %v", err)
	}
	afterHooks, ok, err := st.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("read promoted checkpoint: ok=%v err=%v", ok, err)
	}
	promoted := afterHooks.Metadata
	// Hooks without native identities cannot repair the missing witness, even
	// after another prompt. Only transcript-backed admission can prove a pair.
	if promoted.LatestUserPrompt != "later canonical prompt" ||
		promoted.LatestAssistantUpdate != "" ||
		promoted.ConversationCheckpointState != domain.ConversationCheckpointPrompt ||
		promoted.ConversationCheckpointGeneration != "terminal-generation" ||
		promoted.ConversationCheckpointNativeID != "native-1" ||
		!promoted.ConversationCheckpointUnsettled ||
		!strings.Contains(promoted.NativeCheckpointEvidence, `"invalid":true`) {
		t.Fatalf("unproven canonical hooks certified completion: %+v", promoted)
	}
}

func TestActivitySignal_CASRetryPreservesCorrelatedPermissionPost(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	baseStore, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := baseStore.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	st := &activityProjectionBarrierStore{
		Store: baseStore, updateEntered: make(chan struct{}), releaseUpdate: make(chan struct{}),
		// The permission request is the first projection. Let it establish the
		// blocked row, then interleave the correlated post at the next CAS.
		skipUpdates: 1,
	}
	t.Cleanup(func() {
		select {
		case <-st.releaseUpdate:
		default:
			close(st.releaseUpdate)
		}
	})
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID: "proj", Path: "/repo", RegisteredAt: now,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	created, err := st.CreateSession(ctx, domain.SessionRecord{
		ID: "session-1", ProjectID: "proj", Kind: domain.KindWorker,
		Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/session-1", Branch: "tp/session-1",
			RuntimeHandleID: "runtime-1", RuntimeLaunchID: "terminal-generation",
		},
		Activity:      domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		FirstSignalAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}

	lcm := lifecycle.New(st, nil)
	if err := lcm.ApplyActivitySignal(ctx, created.ID, ports.ActivitySignal{
		Event: "pre-tool-use", ToolName: "Bash", ToolUseID: "tool-1",
		LaunchID: "terminal-generation", Valid: true, State: domain.ActivityActive,
	}); err != nil {
		t.Fatalf("apply pre-tool-use: %v", err)
	}
	if err := lcm.ApplyActivitySignal(ctx, created.ID, ports.ActivitySignal{
		Event: "permission-request", ToolName: "Bash", ToolUseID: "tool-1",
		LaunchID: "terminal-generation", Valid: true, State: domain.ActivityBlocked,
	}); err != nil {
		t.Fatalf("apply permission request: %v", err)
	}
	blocked, ok, err := st.GetSession(ctx, created.ID)
	if err != nil || !ok || blocked.Activity.State != domain.ActivityBlocked {
		t.Fatalf("permission request did not block: session=%+v ok=%v err=%v", blocked, ok, err)
	}

	postDone := make(chan error, 1)
	go func() {
		postDone <- lcm.ApplyActivitySignal(ctx, created.ID, ports.ActivitySignal{
			Event: "post-tool-use", ToolName: "Bash", ToolUseID: "tool-1",
			LaunchID: "terminal-generation", Valid: true, State: domain.ActivityActive,
		})
	}()
	select {
	case <-st.updateEntered:
	case <-time.After(time.Second):
		t.Fatal("correlated post did not reach the storage barrier")
	}
	if changed, err := st.RecordSessionLatestUserPrompt(
		ctx, created.ID, "pane prompt during permission post", blocked.UpdatedAt.Add(time.Second),
	); err != nil || !changed {
		t.Fatalf("advance session revision: changed=%v err=%v", changed, err)
	}
	close(st.releaseUpdate)
	if err := <-postDone; err != nil {
		t.Fatalf("apply correlated post: %v", err)
	}

	after, ok, err := st.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("read session after CAS retry: ok=%v err=%v", ok, err)
	}
	if after.Activity.State != domain.ActivityActive {
		t.Fatalf("correlated permission post left activity %q, want active", after.Activity.State)
	}
	if after.Metadata.LatestUserPrompt != "pane prompt during permission post" ||
		after.Metadata.LatestAssistantUpdate != "" ||
		after.Metadata.ConversationCheckpointState != domain.ConversationCheckpointLegacy {
		t.Fatalf("CAS retry overwrote concurrent pane checkpoint: %+v", after.Metadata)
	}
}

func TestSend_PaneFallbackWinsAgainstStaleLifecycleProjection(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	baseStore, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := baseStore.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	st := &activityProjectionBarrierStore{
		Store: baseStore, updateEntered: make(chan struct{}), releaseUpdate: make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-st.releaseUpdate:
		default:
			close(st.releaseUpdate)
		}
	})
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID: "proj", Path: "/repo", RegisteredAt: now,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	created, err := st.CreateSession(ctx, domain.SessionRecord{
		ID: "session-1", ProjectID: "proj", Kind: domain.KindWorker,
		Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/session-1", Branch: "tp/session-1",
			RuntimeHandleID: "runtime-1", RuntimeLaunchID: "terminal-generation",
			AgentSessionID: "native-1", AgentSessionIDLaunchID: "terminal-generation",
			LatestUserPrompt:                 "prior trusted prompt",
			ConversationCheckpointState:      domain.ConversationCheckpointPrompt,
			ConversationCheckpointGeneration: "terminal-generation",
			ConversationCheckpointNativeID:   "native-1",
		},
		Activity:      domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		FirstSignalAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}

	lcm := lifecycle.New(st, nil)
	manager := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: fakeAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: lcm,
	})
	hookDone := make(chan error, 1)
	go func() {
		hookDone <- lcm.ApplyActivitySignal(ctx, created.ID, ports.ActivitySignal{
			Event: "stop", AgentSessionID: "native-1", LatestAssistantUpdate: "stale prior answer",
			LaunchID: "terminal-generation", Valid: true, State: domain.ActivityIdle,
		})
	}()
	select {
	case <-st.updateEntered:
	case <-time.After(time.Second):
		t.Fatal("lifecycle projection did not reach the storage barrier")
	}

	if err := manager.SendWithOptions(ctx, created.ID, "pane prompt whose hook is lost", nil, ports.MessageDeliveryOptions{AuthoredByUser: true}); err != nil {
		t.Fatalf("send pane prompt: %v", err)
	}
	afterPane, ok, err := st.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("read pane fallback: ok=%v err=%v", ok, err)
	}
	if afterPane.Metadata.LatestUserPrompt != "pane prompt whose hook is lost" ||
		afterPane.Metadata.LatestAssistantUpdate != "" ||
		afterPane.Metadata.ConversationCheckpointState != domain.ConversationCheckpointLegacy {
		t.Fatalf("pane fallback before stale projection = %+v", afterPane.Metadata)
	}
	close(st.releaseUpdate)
	if err := <-hookDone; err != nil {
		t.Fatalf("apply stale Stop: %v", err)
	}

	afterRace, ok, err := st.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("read checkpoint after race: ok=%v err=%v", ok, err)
	}
	checkpoint := afterRace.Metadata
	if checkpoint.LatestUserPrompt != "pane prompt whose hook is lost" ||
		checkpoint.LatestAssistantUpdate != "" ||
		checkpoint.ConversationCheckpointState != domain.ConversationCheckpointLegacy ||
		checkpoint.ConversationCheckpointGeneration != "" ||
		checkpoint.ConversationCheckpointNativeID != "" ||
		!checkpoint.ConversationCheckpointUnsettled {
		t.Fatalf("stale lifecycle projection resurrected trusted checkpoint: %+v", checkpoint)
	}

	if err := lcm.ApplyActivitySignal(ctx, created.ID, ports.ActivitySignal{
		Event: "user-prompt-submit", AgentSessionID: "native-1", LatestUserPrompt: "later canonical prompt",
		LaunchID: "terminal-generation", Valid: true, State: domain.ActivityActive,
	}); err != nil {
		t.Fatalf("apply later canonical UserPromptSubmit: %v", err)
	}
	if err := lcm.ApplyActivitySignal(ctx, created.ID, ports.ActivitySignal{
		Event: "stop", AgentSessionID: "native-1", LatestAssistantUpdate: "later canonical answer",
		LaunchID: "terminal-generation", Valid: true, State: domain.ActivityIdle,
	}); err != nil {
		t.Fatalf("apply later canonical Stop: %v", err)
	}
	promoted, ok, err := st.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("read later canonical checkpoint: ok=%v err=%v", ok, err)
	}
	if promoted.Metadata.LatestUserPrompt != "later canonical prompt" ||
		promoted.Metadata.LatestAssistantUpdate != "" ||
		promoted.Metadata.ConversationCheckpointState != domain.ConversationCheckpointPrompt ||
		promoted.Metadata.ConversationCheckpointGeneration != "terminal-generation" ||
		promoted.Metadata.ConversationCheckpointNativeID != "native-1" ||
		!promoted.Metadata.ConversationCheckpointUnsettled ||
		!strings.Contains(promoted.Metadata.NativeCheckpointEvidence, `"invalid":true`) {
		t.Fatalf("unproven later canonical hooks certified completion: %+v", promoted.Metadata)
	}
}

func TestSend_ConfirmsAndNudgesUntilActive(t *testing.T) {
	t.Parallel()

	// A signaling harness starts idle. The first nudge (Enter-only Send) should
	// flip the session active, after which confirmActive stops. Net: the
	// initial message plus exactly one nudge.
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityIdle}})
	// A messenger that flips the session active on the first Enter-only nudge,
	// mimicking the agent accepting the prompt.
	msg := &flipOnNudgeMessenger{sessionID: "s1", store: st}
	m := newSendTestManager(t, signalingAgent{}, msg, st)

	if err := m.SendWithOptions(context.Background(), "s1", "do the thing", nil, ports.MessageDeliveryOptions{AuthoredByUser: true}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("Send calls = %d, want 2 (initial + one nudge)", len(msg.msgs))
	}
	if msg.msgs[0] != "do the thing" {
		t.Fatalf("first msg = %q, want the prompt", msg.msgs[0])
	}
	if msg.msgs[1] != "" {
		t.Fatalf("nudge msg = %q, want empty (Enter-only)", msg.msgs[1])
	}
	if got := st.sessions["s1"].Activity.State; got != domain.ActivityActive {
		t.Fatalf("Activity.State = %q, want active", got)
	}
}

func TestSend_ConfirmBudgetCapsRetries(t *testing.T) {
	t.Parallel()

	// A signaling harness that never goes active must still terminate: at most
	// maxAttempts Sends (initial + maxAttempts-1 nudges), and Send surfaces ErrDeliveryUnconfirmed.
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityIdle}})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, signalingAgent{}, msg, st)
	var logBuf bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&logBuf, nil))

	err := m.Send(context.Background(), "s1", "stuck prompt", nil)
	if !errors.Is(err, ErrDeliveryUnconfirmed) {
		t.Fatalf("Send error = %v, want ErrDeliveryUnconfirmed", err)
	}
	if len(msg.msgs) > m.sendConfirm.maxAttempts {
		t.Fatalf("Send calls = %d, want <= %d (budget cap)", len(msg.msgs), m.sendConfirm.maxAttempts)
	}
	if got := st.sessions["s1"].Activity.State; got == domain.ActivityActive {
		t.Fatalf("Activity.State = active, want unchanged (session never went active)")
	}
	logText := logBuf.String()
	if !strings.Contains(logText, "level=WARN") ||
		!strings.Contains(logText, "activity confirmation budget exhausted") ||
		!strings.Contains(logText, "sessionID=s1") ||
		!strings.Contains(logText, "attempts=3") {
		t.Fatalf("log = %q, want exhausted confirmation warning with session and attempt count", logText)
	}
}

func TestSend_ConfirmAcceptsRetainedSubmissionEvidence(t *testing.T) {
	t.Parallel()

	// A user prompt whose UserPromptSubmit hook lands AFTER delivery (advancing
	// LatestUserPromptAt past the delivery instant) but whose turn completes
	// between activity polls leaves the session idle, with Active never observed.
	// Confirmation must treat that later durable submission evidence as success
	// instead of returning a false ErrDeliveryUnconfirmed, which would prompt a
	// duplicate retry of an already-delivered message. The baseline is the
	// delivery instant (options.InteractionAt), so the hook's later write is
	// detectable regardless of when, relative to a store read, it arrives.
	st := newFakeStore()
	base := time.Now()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{LatestUserPromptAt: base}})
	msg := &advancePromptOnNudgeMessenger{sessionID: "s1", store: st, at: base.Add(time.Hour)}
	m := newSendTestManager(t, signalingAgent{}, msg, st)

	if err := m.SendWithOptions(context.Background(), "s1", "do the thing", nil,
		ports.MessageDeliveryOptions{AuthoredByUser: true}); err != nil {
		t.Fatalf("Send error = %v, want nil (retained submission evidence recognized)", err)
	}
	if got := st.sessions["s1"].Activity.State; got == domain.ActivityActive {
		t.Fatalf("Activity.State = active, want it to have stayed idle (evidence path, not the active path)")
	}
}

func TestSend_CoordinationDeliveryIgnoresPromptEvidence(t *testing.T) {
	t.Parallel()

	// A coordination (tallpine send) delivery deliberately leaves LatestUserPromptAt
	// unchanged, so that marker is the wrong evidence for it: an advance during
	// the confirm window can only come from an unrelated concurrent user prompt,
	// and crediting it would falsely confirm a coordination send. Here the nudge
	// advances LatestUserPromptAt but no coordination checkpoint is recorded, so
	// confirmation must still exhaust its budget and surface ErrDeliveryUnconfirmed.
	st := newFakeStore()
	base := time.Now().Add(-time.Hour)
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{LatestUserPromptAt: base}})
	msg := &advancePromptOnNudgeMessenger{sessionID: "s1", store: st, at: time.Now()}
	m := newSendTestManager(t, signalingAgent{}, msg, st)

	err := m.Send(context.Background(), "s1", "status update please", nil)
	if !errors.Is(err, ErrDeliveryUnconfirmed) {
		t.Fatalf("Send error = %v, want ErrDeliveryUnconfirmed (coordination send ignores prompt evidence)", err)
	}
}

// The positive coordination-acceptance case (marker recorded, session back to
// idle before confirmation polls) is covered end-to-end against a real sqlite
// store and the real lifecycle write path by
// TestSend_CoordinationAcceptanceMarker_RealStore in
// send_confirm_coordination_integration_test.go.

func TestSend_BlockedSessionRejectsDelivery(t *testing.T) {
	t.Parallel()

	// A session paused on a permission decision (blocked) must not receive the
	// paste at all: the runtime appends Enter, which could answer the dialog.
	// Send surfaces ErrAwaitingDecision (the API's 409) and the messenger is
	// never called, so nothing — message or nudge — reaches the pane.
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityBlocked}})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, signalingAgent{}, msg, st)

	err := m.Send(context.Background(), "s1", "status update please", nil)
	if !errors.Is(err, ErrAwaitingDecision) {
		t.Fatalf("Send error = %v, want ErrAwaitingDecision", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("Send calls = %d, want 0 (no paste into a pending decision)", len(msg.msgs))
	}
}

func TestSend_ExitedAgentRejectsDelivery(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["s1"] = domain.SessionRecord{ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityExited}}
	msg := &fakeMessenger{}
	m := newSendTestManager(t, signalingAgent{}, msg, st)

	err := m.Send(context.Background(), "s1", "status update please", nil)
	if !errors.Is(err, ErrAgentExited) {
		t.Fatalf("Send error = %v, want ErrAgentExited", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("Send calls = %d, want 0 (no paste into an exited agent shell)", len(msg.msgs))
	}
}

func TestSend_TUIStartupPendingRejectsDelivery(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["s1"] = domain.SessionRecord{
		ID: "s1", Harness: domain.HarnessKiro, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "h1"},
	}
	msg := &fakeMessenger{}
	m := newSendTestManager(t, startupReadySignalingAgent{}, msg, st)

	err := m.Send(context.Background(), "s1", "follow-up after spawn", nil)
	if !errors.Is(err, ErrStartupPending) {
		t.Fatalf("Send error = %v, want ErrStartupPending", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("Send calls = %d, want 0 before first hook signal", len(msg.msgs))
	}
}

func TestSend_HooklessTUIStartupAllowsDelivery(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["s1"] = domain.SessionRecord{
		ID: "s1", Harness: domain.HarnessKiro, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "h1"},
	}
	msg := &fakeMessenger{}
	m := newSendTestManager(t, fakeAgent{}, msg, st)

	if err := m.Send(context.Background(), "s1", "follow-up after spawn", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("Send calls = %d, want 1 for hookless TUI harness", len(msg.msgs))
	}
}

func TestSend_NoNudgeWhenBlockedAppearsMidWait(t *testing.T) {
	t.Parallel()

	// The permission dialog can appear between polls (e.g. the delivered prompt
	// itself triggered a tool approval). The confirm loop must abort on the
	// first blocked observation instead of nudging after the deadline.
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityIdle}})
	msg := &blockOnSendMessenger{sessionID: "s1", store: st}
	m := newSendTestManager(t, signalingAgent{}, msg, st)

	if err := m.Send(context.Background(), "s1", "run the migration", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("Send calls = %d, want 1 (blocked observed mid-confirm, no nudge)", len(msg.msgs))
	}
}

func TestSend_StillNudgesWhenWaitingInput(t *testing.T) {
	t.Parallel()

	// waiting_input (an idle prompt awaiting the next instruction) is the
	// PRIMARY nudge scenario: a long-idle worker with an unsubmitted pasted
	// draft. The decision-safety guard must not disable it.
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityWaitingInput}})
	msg := &flipOnNudgeMessenger{sessionID: "s1", store: st}
	m := newSendTestManager(t, signalingAgent{}, msg, st)

	if err := m.Send(context.Background(), "s1", "do the thing", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("Send calls = %d, want 2 (initial + one nudge for waiting_input)", len(msg.msgs))
	}
	if msg.msgs[1] != "" {
		t.Fatalf("nudge msg = %q, want empty (Enter-only)", msg.msgs[1])
	}
}

func TestSend_NoNudgeWhenBlockedAppearsBeforeNudge(t *testing.T) {
	t.Parallel()

	// The TOCTOU the per-poll check cannot cover: the session is not blocked on
	// waitForActive's final poll, but a permission dialog lands in the gap
	// before the Enter-only nudge. The just-in-time re-read in confirmActive
	// must catch it — exactly one Send, no nudge.
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityIdle}})
	// blockAfterFirstReadStore flips the session to blocked on read #4. The
	// deterministic read sequence (attemptDeadline 0 makes waitForActive do
	// exactly one poll): #1 Deliver's pre-paste read, #2 Send's harness lookup,
	// #3 waitForActive's poll (idle → timeout), #4 the JIT pre-nudge re-read —
	// which is the first to see blocked, landing the flip in the exact
	// post-final-poll / pre-nudge window this test exists to cover.
	bst := &blockAfterFirstReadStore{fakeStore: st, id: "s1"}
	msg := &fakeMessenger{}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{signalingAgent{}}, Workspace: &fakeWorkspace{},
		Store: bst, Messenger: msg, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.sendConfirm = sendConfirmConfig{pollInterval: time.Millisecond, attemptDeadline: 0, maxAttempts: 3}

	if err := m.Send(context.Background(), "s1", "run the migration", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("Send calls = %d, want 1 (blocked appeared before nudge, JIT re-read caught it)", len(msg.msgs))
	}
	if bst.reads < 4 {
		t.Fatalf("GetSession reads = %d, want >= 4 (the JIT pre-nudge re-read must have run)", bst.reads)
	}
}

func TestSend_SkipsConfirmForSubmitOnlyHarness(t *testing.T) {
	t.Parallel()

	// A harness that submits but cannot report blocked (for example Kiro) is
	// NOT nudge-safe: confirmActive must be skipped entirely, so an Enter can
	// never reach a permission dialog the harness could not have signalled.
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: domain.HarnessKiro,
		Activity: domain.Activity{State: domain.ActivityIdle}})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, submitOnlyAgent{}, msg, st)

	if err := m.Send(context.Background(), "s1", "do the thing", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("Send calls = %d, want 1 (submit-only harness must not be nudged)", len(msg.msgs))
	}
}

func TestHarnessNudgeSafe(t *testing.T) {
	t.Parallel()

	m := New(Deps{Agents: singleAgent{agent: fakeAgent{}}})
	if m.harnessNudgeSafe("claude-code") {
		t.Fatalf("hookless agent reported as nudge-safe")
	}
	m2 := New(Deps{Agents: singleAgent{agent: signalingAgent{}}})
	if !m2.harnessNudgeSafe("claude-code") {
		t.Fatalf("submit+blocked agent not reported as nudge-safe")
	}
	m3 := New(Deps{Agents: singleAgent{agent: submitOnlyAgent{}}})
	if m3.harnessNudgeSafe("claude-code") {
		t.Fatalf("submit-only agent (no blocked signal) reported as nudge-safe")
	}
	m4 := New(Deps{Agents: missingAgents{}})
	if m4.harnessNudgeSafe("claude-code") {
		t.Fatalf("unresolved harness reported as nudge-safe")
	}
}

func TestSwitchTargetsOnlyRetryEnterWhenActivitySignalsMakeItSafe(t *testing.T) {
	t.Parallel()

	agents := testAgentRegistry{
		domain.HarnessClaudeCode: claudecode.New(),
		domain.HarnessCodex:      codex.New(),
	}
	m := New(Deps{Agents: agents})

	cases := []struct {
		harness domain.AgentHarness
		want    bool
	}{
		{domain.HarnessClaudeCode, true},
		{domain.HarnessCodex, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.harness), func(t *testing.T) {
			if got := m.harnessNudgeSafe(tc.harness); got != tc.want {
				t.Fatalf("harnessNudgeSafe(%q) = %v, want %v", tc.harness, got, tc.want)
			}
		})
	}
}

func TestSendAutomationDoesNotRecordHumanDirection(t *testing.T) {
	t.Parallel()

	for _, sender := range []string{"", "orchestrator-1", "worker-1", "unknown"} {
		t.Run("sender="+sender, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code", Metadata: domain.SessionMetadata{LatestUserPrompt: "human task", LatestUserPromptAt: time.Unix(10, 0)}})
			messenger := &fakeMessenger{}
			m := newSendTestManager(t, fakeAgent{}, messenger, st)
			if err := m.SendWithOptions(context.Background(), "s1", "automation direction", nil, ports.MessageDeliveryOptions{SenderSessionID: sender}); err != nil {
				t.Fatal(err)
			}
			got := st.sessions["s1"].Metadata
			if got.LatestUserPrompt != "human task" || !got.LatestUserPromptAt.Equal(time.Unix(10, 0)) {
				t.Fatalf("human facts=%+v", got)
			}
			if len(messenger.msgs) != 1 {
				t.Fatalf("messages=%v", messenger.msgs)
			}
			if _, ok := domain.CoordinationDeliveryID(messenger.msgs[0]); !ok {
				t.Fatal("terminal delivery lacks coordination provenance for hooks")
			}
		})
	}
}

func TestSendEmptyNudgeDoesNotBecomeCoordinationPrompt(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code"})
	messenger := &fakeMessenger{}
	m := newSendTestManager(t, fakeAgent{}, messenger, st)
	if err := m.Send(context.Background(), "s1", "", nil); err != nil {
		t.Fatal(err)
	}
	if len(messenger.msgs) != 1 || messenger.msgs[0] != "" {
		t.Fatalf("empty nudge=%v", messenger.msgs)
	}
	if !st.sessions["s1"].Metadata.LatestUserPromptAt.IsZero() {
		t.Fatal("nudge counted as human direction")
	}
}

func TestSendRecordsInteractionOnlyForDirectTerminalSender(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                string
		chat, human, replay bool
		sender              string
		want                int
	}{
		{name: "terminal orchestrator", sender: "orchestrator-1", want: 1},
		{name: "terminal human", human: true},
		{name: "terminal queued replay", sender: "orchestrator-1", replay: true},
		{name: "chat orchestrator", sender: "orchestrator-1", chat: true},
		{name: "chat human", human: true, chat: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			rec := pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code"})
			if tc.chat {
				rec.Mode = domain.SessionModeChat
			}
			st.sessions["s1"] = rec
			m := newSendTestManager(t, fakeAgent{}, &fakeMessenger{}, st)
			counter := &interactionCountingStore{fakeStore: st}
			m.store = counter
			m.chat = &recordingLauncher{}
			key := ""
			if tc.replay {
				key = "outbox-1"
			}
			if err := m.send(context.Background(), "s1", "direction", key, ports.MessageDeliveryOptions{SenderSessionID: tc.sender, AuthoredByUser: tc.human}); err != nil {
				t.Fatal(err)
			}
			if counter.calls != tc.want {
				t.Fatalf("interaction writes=%d want=%d", counter.calls, tc.want)
			}
		})
	}
}
