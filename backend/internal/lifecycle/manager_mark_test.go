package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestMarkTerminated(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if !got.IsTerminated || got.Activity.State != domain.ActivityExited {
		t.Fatalf("want terminated/exited, got %+v", got)
	}
}

func TestMarkSpawnedReactivatesUsageAfterLifecycleTransition(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		IsTerminated: true,
		Activity:     domain.Activity{State: domain.ActivityExited},
		Metadata:     domain.SessionMetadata{RuntimeLaunchID: "launch-old"},
	}
	rec := st.sessions["mer-1"]
	rec.CleanupGeneration = 7
	st.sessions[rec.ID] = rec
	usage := &fakeUsageLifecycle{fakeUsageFinalizer: fakeUsageFinalizer{store: st}}
	m.SetUsageFinalizer(usage)

	if err := m.MarkSpawned(ctx, "mer-1", domain.SessionMetadata{RuntimeLaunchID: "launch-new"}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].CleanupGeneration; got != 8 {
		t.Fatalf("restored cleanup generation = %d, want 8", got)
	}
	if usage.reactivateCalls != 1 || usage.reactivateID != "mer-1" ||
		usage.reactivateLaunch != "launch-new" || !usage.sawLive {
		t.Fatalf("usage reactivation = calls:%d id:%q launch:%q live:%v",
			usage.reactivateCalls, usage.reactivateID, usage.reactivateLaunch, usage.sawLive)
	}
}

func TestMarkTerminatedFinalizesUsageBeforeLifecycleTransition(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.UpdatedAt = time.Date(2026, 8, 2, 11, 30, 0, 0, time.UTC)
	st.sessions[rec.ID] = rec
	finalizer := &fakeUsageFinalizer{store: st, err: errors.New("best effort failure")}
	m.SetUsageFinalizer(finalizer)

	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if finalizer.calls != 1 || finalizer.sawTerminated {
		t.Fatalf("finalizer calls=%d sawTerminated=%v, want 1/false", finalizer.calls, finalizer.sawTerminated)
	}
	if finalizer.sessionRevision != rec.Revision {
		t.Fatalf("finalizer session revision=%d, want %d", finalizer.sessionRevision, rec.Revision)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("finalizer failure prevented session termination")
	}
	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if finalizer.calls != 1 {
		t.Fatalf("already terminated session finalized %d times, want once", finalizer.calls)
	}
}

func TestMarkTerminatedDoesNotTerminateNewRuntimeGeneration(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-old"
	st.sessions[rec.ID] = rec
	finalizer := &fakeUsageFinalizer{store: st}
	finalizer.onFinalize = func(id domain.SessionID, _ string, _ int64) error {
		return m.MarkSpawned(ctx, id, domain.SessionMetadata{RuntimeLaunchID: "launch-new"})
	}
	m.SetUsageFinalizer(finalizer)

	if err := m.MarkTerminated(ctx, rec.ID); err == nil || !strings.Contains(err.Error(), "runtime launch changed") {
		t.Fatalf("MarkTerminated() error = %v, want runtime launch change", err)
	}
	if finalizer.launchID != "launch-old" {
		t.Fatalf("finalizer launch id=%q, want launch-old", finalizer.launchID)
	}
	got := st.sessions[rec.ID]
	if got.IsTerminated || got.Metadata.RuntimeLaunchID != "launch-new" {
		t.Fatalf("stale termination changed new runtime generation: %+v", got)
	}
}

func TestMarkTerminatedRetriesFinalizationAfterSameLaunchRevisionChange(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-1"
	rec.UpdatedAt = time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	st.sessions[rec.ID] = rec
	var revisions []int64
	finalizer := &fakeUsageFinalizer{store: st}
	finalizer.onFinalize = func(id domain.SessionID, _ string, revision int64) error {
		revisions = append(revisions, revision)
		if len(revisions) == 1 {
			current := st.sessions[id]
			current.Revision++
			st.sessions[id] = current
		}
		return nil
	}
	m.SetUsageFinalizer(finalizer)

	if err := m.MarkTerminated(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 || revisions[0] != rec.Revision || revisions[1] != rec.Revision+1 {
		t.Fatalf("finalization revisions = %v", revisions)
	}
	if !st.sessions[rec.ID].IsTerminated {
		t.Fatal("session was not terminated after revision-fenced retry")
	}
}

func TestMarkSpawnedStoresRuntimeMetadata(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true}
	metadata := domain.SessionMetadata{
		Branch:            "b",
		WorkspacePath:     "/ws",
		WorkspaceRepoPath: "/repos/mer",
		RuntimeHandleID:   "h1",
		AgentSessionID:    "agent",
		Prompt:            "prompt",
	}
	if err := m.MarkSpawned(ctx, "mer-1", metadata); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if got.IsTerminated || got.Activity.State != domain.ActivityIdle || got.Metadata.RuntimeHandleID != "h1" {
		t.Fatalf("spawn metadata wrong: %+v", got)
	}
	if got.Metadata.WorkspaceRepoPath != metadata.WorkspaceRepoPath {
		t.Fatalf("workspace repo path = %q, want %q", got.Metadata.WorkspaceRepoPath, metadata.WorkspaceRepoPath)
	}
}

func TestMarkSpawnedPersistsAndPreservesDiffBase(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true}

	wantSHA := "0123456789abcdef"
	wantRef := "refs/remotes/origin/main"
	if err := m.MarkSpawned(ctx, "mer-1", domain.SessionMetadata{
		WorkspacePath: "/ws",
		DiffBaseSHA:   wantSHA,
		DiffBaseRef:   wantRef,
	}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"].Metadata
	if got.DiffBaseSHA != wantSHA || got.DiffBaseRef != wantRef {
		t.Fatalf("spawn diff base = sha:%q ref:%q, want sha:%q ref:%q", got.DiffBaseSHA, got.DiffBaseRef, wantSHA, wantRef)
	}

	// Restore does not recompute the base. Empty incoming values must preserve
	// the durable comparison metadata recorded by the initial spawn.
	if err := m.MarkSpawned(ctx, "mer-1", domain.SessionMetadata{
		WorkspacePath:   "/ws",
		RuntimeHandleID: "h2",
	}); err != nil {
		t.Fatal(err)
	}
	got = st.sessions["mer-1"].Metadata
	if got.DiffBaseSHA != wantSHA || got.DiffBaseRef != wantRef {
		t.Fatalf("restored diff base = sha:%q ref:%q, want preserved sha:%q ref:%q", got.DiffBaseSHA, got.DiffBaseRef, wantSHA, wantRef)
	}
}

func TestCommitControllerEpochOwnsModeAndActivityFacts(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: time.Unix(10, 0)},
		Metadata: domain.SessionMetadata{
			RuntimeHandleID: "runtime-1", RuntimeLaunchID: "launch-1",
			AgentSessionID:   "native-1",
			LatestUserPrompt: "trusted Terminal turn A", LatestAssistantUpdate: "trusted Terminal answer A",
			ConversationCheckpointState:      domain.ConversationCheckpointComplete,
			ConversationCheckpointGeneration: "launch-1", ConversationCheckpointNativeID: "native-1",
		},
	}

	changed, err := m.CommitControllerEpoch(
		ctx, "mer-1", domain.SessionModeTUI, domain.SessionModeChat, "native-1", false,
	)
	if err != nil || !changed {
		t.Fatalf("CommitControllerEpoch: changed=%v err=%v", changed, err)
	}
	got := st.sessions["mer-1"]
	if got.Mode != domain.SessionModeChat || got.Activity.State != domain.ActivityIdle {
		t.Fatalf("controller facts = mode:%q activity:%q", got.Mode, got.Activity.State)
	}
	if got.Metadata.RuntimeHandleID != "" || got.Metadata.RuntimeLaunchID != "" ||
		got.Metadata.AgentSessionID != "native-1" ||
		got.Metadata.AgentSessionIDLaunchID != "" ||
		got.Metadata.ProviderConversationID != "native-1" ||
		got.Metadata.ControllerGeneration != "" {
		t.Fatalf("controller metadata = %+v", got.Metadata)
	}
	if got.Metadata.ConversationCheckpointState != domain.ConversationCheckpointComplete ||
		got.Metadata.LatestUserPrompt != "trusted Terminal turn A" ||
		got.Metadata.LatestAssistantUpdate != "trusted Terminal answer A" {
		t.Fatalf("TUI -> Chat discarded the admission checkpoint: %+v", got.Metadata)
	}
	changed, err = m.CommitControllerEpoch(
		ctx, "mer-1", domain.SessionModeTUI, domain.SessionModeChat, "native-1", false,
	)
	if err != nil || changed {
		t.Fatalf("stale controller epoch: changed=%v err=%v", changed, err)
	}
	changed, err = m.RestoreControllerEpoch(
		ctx, "mer-1", domain.SessionModeChat, domain.SessionModeTUI, "native-1", false,
	)
	if err != nil || !changed {
		t.Fatalf("restore Chat -> TUI controller epoch: changed=%v err=%v", changed, err)
	}
	got = st.sessions["mer-1"]
	if got.Metadata.ConversationCheckpointState != domain.ConversationCheckpointComplete ||
		got.Metadata.LatestUserPrompt != "trusted Terminal turn A" ||
		got.Metadata.LatestAssistantUpdate != "trusted Terminal answer A" {
		t.Fatalf("rollback discarded the unaccepted replay checkpoint: %+v", got.Metadata)
	}
	changed, err = m.CommitControllerEpoch(
		ctx, "mer-1", domain.SessionModeTUI, domain.SessionModeChat, "native-1", false,
	)
	if err != nil || !changed {
		t.Fatalf("second TUI -> Chat controller epoch: changed=%v err=%v", changed, err)
	}
	changed, err = m.CommitControllerEpoch(
		ctx, "mer-1", domain.SessionModeChat, domain.SessionModeTUI, "native-1", false,
	)
	if err != nil || !changed {
		t.Fatalf("Chat -> TUI controller epoch: changed=%v err=%v", changed, err)
	}
	got = st.sessions["mer-1"]
	if got.Metadata.LatestUserPrompt != "" || got.Metadata.LatestAssistantUpdate != "" ||
		got.Metadata.ConversationCheckpointState != domain.ConversationCheckpointEmpty ||
		got.Metadata.ConversationCheckpointGeneration != "" ||
		got.Metadata.ConversationCheckpointNativeID != "" {
		t.Fatalf("Chat -> TUI retained stale admission checkpoint: %+v", got.Metadata)
	}
}

func TestCommitControllerEpochAllowsExplicitFreshHandoff(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{
			RuntimeHandleID: "runtime-1", RuntimeLaunchID: "launch-1",
			AgentSessionID: "reserved-but-empty",
		},
	}

	changed, err := m.CommitControllerEpoch(
		ctx, "mer-1", domain.SessionModeTUI, domain.SessionModeChat, "", true,
	)
	if err != nil || !changed {
		t.Fatalf("CommitControllerEpoch fresh: changed=%v err=%v", changed, err)
	}
	got := st.sessions["mer-1"]
	if got.Mode != domain.SessionModeChat || got.Metadata.AgentSessionID != "" ||
		got.Metadata.ProviderConversationID != "" {
		t.Fatalf("fresh controller facts = %+v", got)
	}

	if _, err := m.CommitControllerEpoch(
		ctx, "mer-1", domain.SessionModeChat, domain.SessionModeTUI, "", false,
	); err == nil {
		t.Fatal("blank native id without explicit fresh handoff was accepted")
	}
}

// TestMarkSpawned_StampsUTCActivity locks the lifecycle clock to UTC so
// activity-driven timestamps match the session manager's spawn timestamps. A
// local clock here left `tallpine session get` showing created in UTC but updated in
// local time.
func TestMarkSpawned_StampsUTCActivity(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true}
	if err := m.MarkSpawned(ctx, "mer-1", domain.SessionMetadata{RuntimeHandleID: "h1"}); err != nil {
		t.Fatal(err)
	}
	if loc := st.sessions["mer-1"].Activity.LastActivityAt.Location(); loc != time.UTC {
		t.Fatalf("LastActivityAt location = %v, want UTC", loc)
	}
}

// mergeMetadata is an explicit allowlist, so a field added to SessionMetadata
// without a line here is silently dropped on every spawn and restore. That
// happened to the chat resume handle: the provider still held the conversation,
// but Tallpine forgot its id, so no restart could ever resume it — and nothing failed
// loudly, the column was just empty.
func TestMarkSpawnedPersistsChatControllerFacts(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{
			RuntimeHandleID: "stale-tui-runtime",
			RuntimeLaunchID: "stale-tui-generation",
		},
	}
	m := New(st, nil)

	if err := m.MarkSpawned(ctx, "mer-1", domain.SessionMetadata{
		WorkspacePath:            "/ws",
		ProviderConversationID:   "thread-abc",
		ControllerGeneration:     "gen-1",
		LatestAssistantUpdateAt:  time.Unix(100, 0),
		NativeIdentityObservedAt: time.Unix(101, 0),
	}); err != nil {
		t.Fatalf("MarkSpawned: %v", err)
	}

	got, _, err := st.GetSession(ctx, "mer-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Metadata.ProviderConversationID != "thread-abc" {
		t.Fatalf("provider conversation id = %q; without it a restart cannot resume",
			got.Metadata.ProviderConversationID)
	}
	if got.Metadata.ControllerGeneration != "gen-1" {
		t.Fatalf("controller generation = %q", got.Metadata.ControllerGeneration)
	}
	if !got.Metadata.LatestAssistantUpdateAt.Equal(time.Unix(100, 0)) || !got.Metadata.NativeIdentityObservedAt.Equal(time.Unix(101, 0)) {
		t.Fatalf("spawn dropped native history provenance: %+v", got.Metadata)
	}
	if got.Metadata.RuntimeHandleID != "" || got.Metadata.RuntimeLaunchID != "" {
		t.Fatalf("Chat spawn retained terminal ownership metadata: %+v", got.Metadata)
	}

	// A relaunch rotates the generation: the new value must replace the old, or
	// events from the superseded controller could not be told apart.
	if err := m.MarkSpawned(ctx, "mer-1", domain.SessionMetadata{
		WorkspacePath:          "/ws",
		ProviderConversationID: "thread-abc",
		ControllerGeneration:   "gen-2",
	}); err != nil {
		t.Fatalf("second MarkSpawned: %v", err)
	}
	got, _, _ = st.GetSession(ctx, "mer-1")
	if got.Metadata.ControllerGeneration != "gen-2" {
		t.Fatalf("generation = %q after relaunch, want it rotated to gen-2", got.Metadata.ControllerGeneration)
	}
}

// Model is the same allowlist omission as the chat resume handle above, one
// field over: it is declared on SessionMetadata, has its own sessions.model
// column, and is read back by the API — but mergeMetadata never copied it, so
// every `tallpine spawn --model X` persisted an empty model and the session reported
// no model at all.
func TestMarkSpawnedPersistsResolvedModelAndEffort(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	m := New(st, nil)

	if err := m.MarkSpawned(ctx, "mer-1", domain.SessionMetadata{
		WorkspacePath: "/ws",
		Model:         "sonnet",
		Effort:        "high",
	}); err != nil {
		t.Fatalf("MarkSpawned: %v", err)
	}

	got, _, err := st.GetSession(ctx, "mer-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Metadata.Model != "sonnet" {
		t.Fatalf("model = %q, want %q; a spawn's resolved model must survive the merge",
			got.Metadata.Model, "sonnet")
	}
	if got.Metadata.Effort != "high" {
		t.Fatalf("effort = %q, want %q; a spawn's resolved effort must survive the merge",
			got.Metadata.Effort, "high")
	}

	// Merged rather than assigned: a relaunch that resolves no explicit model
	// must leave the recorded one alone instead of blanking it.
	if err := m.MarkSpawned(ctx, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws"}); err != nil {
		t.Fatalf("second MarkSpawned: %v", err)
	}
	got, _, _ = st.GetSession(ctx, "mer-1")
	if got.Metadata.Model != "sonnet" {
		t.Fatalf("model = %q after a relaunch that resolved none, want it preserved", got.Metadata.Model)
	}
	if got.Metadata.Effort != "high" {
		t.Fatalf("effort = %q after a relaunch that resolved none, want it preserved", got.Metadata.Effort)
	}
}

func TestMarkChatSpawnedKeepsPreviousOwnerWhenAtomicBoundaryCommitFails(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	previous := domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{
			ProviderConversationID: "thread-source",
			ControllerGeneration:   "generation-source",
		},
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Unix(1, 0)},
	}
	st.sessions[previous.ID] = previous
	commitErr := errors.New("provider boundary transaction failed")
	st.chatSpawnErr = commitErr
	m := New(st, nil)
	boundary := domain.ConversationBranch{
		ID: "fresh-provider-boundary", ConversationID: "conversation-1", SessionID: previous.ID,
		ProviderConversationID: "thread-fresh", ParentBranchID: "source-provider-boundary",
		ProviderScopeID: "fresh-provider-boundary", CreatedAt: time.Unix(2, 0),
	}

	err := m.MarkChatSpawned(ctx, previous.ID, domain.SessionMetadata{
		ProviderConversationID: "thread-fresh",
		ControllerGeneration:   "generation-fresh",
	}, boundary)
	if !errors.Is(err, commitErr) {
		t.Fatalf("MarkChatSpawned error = %v, want atomic commit failure", err)
	}
	got := st.sessions[previous.ID]
	if got.Metadata.ProviderConversationID != previous.Metadata.ProviderConversationID ||
		got.Metadata.ControllerGeneration != previous.Metadata.ControllerGeneration {
		t.Fatalf("owner after failed boundary commit = handle %q generation %q, want %q/%q",
			got.Metadata.ProviderConversationID, got.Metadata.ControllerGeneration,
			previous.Metadata.ProviderConversationID, previous.Metadata.ControllerGeneration)
	}
	if len(st.chatSpawnCalls) != 0 {
		t.Fatalf("committed Chat boundaries after failure = %+v", st.chatSpawnCalls)
	}
}

func TestMarkChatSpawnedCommitsReservedBoundaryWithLifecycleOwner(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Mode: domain.SessionModeChat, IsTerminated: true,
	}
	m := New(st, nil)
	boundary := domain.ConversationBranch{
		ID: "fresh-provider-boundary", ConversationID: "conversation-1", SessionID: "mer-1",
		ProviderConversationID: "thread-fresh", ParentBranchID: "source-provider-boundary",
		ProviderScopeID: "fresh-provider-boundary", CreatedAt: time.Unix(2, 0),
	}

	if err := m.MarkChatSpawned(ctx, "mer-1", domain.SessionMetadata{
		ProviderConversationID: "thread-fresh",
		ControllerGeneration:   "generation-fresh",
	}, boundary); err != nil {
		t.Fatalf("MarkChatSpawned: %v", err)
	}
	if len(st.chatSpawnCalls) != 1 || st.chatSpawnCalls[0].ID != boundary.ID {
		t.Fatalf("committed Chat boundaries = %+v, want %q", st.chatSpawnCalls, boundary.ID)
	}
	got := st.sessions["mer-1"]
	if got.IsTerminated || got.Activity.State != domain.ActivityIdle ||
		got.Metadata.ProviderConversationID != "thread-fresh" ||
		got.Metadata.ControllerGeneration != "generation-fresh" {
		t.Fatalf("committed Chat owner = %+v", got)
	}
}
