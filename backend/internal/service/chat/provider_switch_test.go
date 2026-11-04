package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func TestProviderSwitchPublishesApproximateBranch(t *testing.T) {
	t.Parallel()

	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()

	// Give the source a complete durable exchange so the target assertion proves
	// the bootstrap came from Tallpine's canonical rows, not provider scrollback.
	completeTurn(t, h, "keep this context", "provider-turn-1")
	h.awaitSnapshot(t, func(snapshot store.ConversationSnapshot) bool {
		return len(snapshot.Messages) == 2
	})
	// The messages and the terminal turn row are committed before the controller
	// clears its pending turn; a switch refuses while it is busy, so also wait
	// for the controller to return to ready.
	h.awaitSnapshot(t, func(snapshot store.ConversationSnapshot) bool {
		return len(snapshot.Turns) == 1 && snapshot.Turns[0].State.Terminal() &&
			h.ctrl.State() == ports.ChatControllerReady
	})

	// A turn this session had in the Terminal UI lives in the terminal turn
	// table. The note is built from both stores, merged by time.
	earlier := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC) // before the harness clock
	if err := h.st.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
		SessionID: testSession, Harness: domain.HarnessCodex, UserRequest: "terminal ask", RequestedAt: earlier,
	}); err != nil {
		t.Fatalf("record terminal request: %v", err)
	}
	if err := h.st.RecordTerminalTurnResponse(ctx, testSession, "terminal answer", earlier.Add(time.Minute)); err != nil {
		t.Fatalf("record terminal response: %v", err)
	}

	if source, err := h.st.ConversationForSession(ctx, testSession); err != nil {
		t.Fatalf("read conversation: %v", err)
	} else if err := h.st.RecordUsage(ctx, source.ID, domain.ConversationUsage{ContextUsed: 26956, ContextWindow: 475000}); err != nil {
		t.Fatalf("record source usage: %v", err)
	}
	before, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("read conversation before the switch: %v", err)
	}
	if source, found, err := h.st.GetSession(ctx, testSession); err != nil || !found || source.Harness != domain.HarnessCodex {
		t.Fatalf("source session = %+v found=%v err=%v, want a Codex session", source, found, err)
	}
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{
		TargetHarness:     domain.HarnessClaudeCode,
		IdempotencyKey:    "switch-once",
		AutoCompactWindow: "1m",
	}); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}

	targetConfig := lastStart(driver)
	if after, err := h.st.ConversationForSession(ctx, testSession); err != nil || after.Usage != nil && after.Usage.ContextWindow != 0 {
		t.Fatalf("usage after the switch = %+v, %v; want the source's reading cleared", after.Usage, err)
	}
	if got := targetConfig.Env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "1000000" {
		t.Fatalf("target compaction window = %q, want the switch's 1m", got)
	}
	if !strings.Contains(targetConfig.SystemPrompt, "keep this context") {
		t.Fatalf("target system prompt did not contain Tallpine handoff context: %q", targetConfig.SystemPrompt)
	}
	for _, want := range []string{domain.HandoffNotePrefix, "terminal ask", "terminal answer", "## Latest turn"} {
		if !strings.Contains(targetConfig.SystemPrompt, want) {
			t.Fatalf("target handoff note is missing %q: %q", want, targetConfig.SystemPrompt)
		}
	}
	if strings.Index(targetConfig.SystemPrompt, "terminal ask") > strings.Index(targetConfig.SystemPrompt, "keep this context") {
		t.Fatalf("the older terminal turn must come before the latest chat turn: %q", targetConfig.SystemPrompt)
	}
	history, total, err := h.svc.SessionHistory(ctx, testSession, 0, 10)
	if err != nil || total != 2 || len(history) != 2 ||
		history[0].UserRequest != "keep this context" || history[1].UserRequest != "terminal ask" {
		t.Fatalf("session history = %+v (total %d), %v; want the chat turn then the terminal turn", history, total, err)
	}
	// A page past the end is empty, not nil: the HTTP layer serialises it as [].
	if past, total, err := h.svc.SessionHistory(ctx, testSession, 5, 10); err != nil || total != 2 || past == nil || len(past) != 0 {
		t.Fatalf("history past the end = %#v (total %d), %v; want an empty page", past, total, err)
	}

	// A switch is not replayed: asking again for the provider that now owns
	// the session is refused.
	err = h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{
		TargetHarness:  domain.HarnessClaudeCode,
		IdempotencyKey: "switch-once",
	})
	var refusal *domain.ProviderSwitchError
	if !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalUnsupported {
		t.Fatalf("repeated switch error = %v, want an unsupported refusal", err)
	}

	record, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("read switched session: found=%v err=%v", found, err)
	}
	if record.Harness != domain.HarnessClaudeCode || record.Mode != domain.SessionModeChat {
		t.Fatalf("durable session owner = %+v, want Claude Chat", record)
	}
	if record.Metadata.ProviderConversationID == "" {
		t.Fatalf("switched session has no target provider conversation: %+v", record.Metadata)
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, before.ID)
	if err != nil {
		t.Fatalf("read switched conversation: %v", err)
	}
	if snapshot.ActiveBranch.ID == "" || snapshot.ActiveBranch.ID == before.ActiveBranchID {
		t.Fatalf("active branch = %q, want a new branch after %q", snapshot.ActiveBranch.ID, before.ActiveBranchID)
	}
	if snapshot.ActiveBranch.ProviderConversationID != record.Metadata.ProviderConversationID {
		t.Fatalf("active branch provider conversation = %q, want the session's %q",
			snapshot.ActiveBranch.ProviderConversationID, record.Metadata.ProviderConversationID)
	}
	if !strings.Contains(snapshot.ActiveBranch.HandoffNote, "keep this context") {
		t.Fatalf("the handoff note was not stored with the new branch: %q", snapshot.ActiveBranch.HandoffNote)
	}
	if snapshot.ActiveBranch.Strategy != domain.ConversationBranchStrategyApproximateContext {
		t.Fatalf("branch strategy = %q, want approximate_context", snapshot.ActiveBranch.Strategy)
	}
	foundMarker := false
	for _, activity := range snapshot.Activities {
		if activity.RequestID == "switch-once" && strings.Contains(activity.Summary, "Switched provider") {
			foundMarker = true
			break
		}
	}
	if !foundMarker {
		t.Fatalf("the switch marker was not committed with the branch")
	}
}

func TestProviderSwitchFailureRestoresSourceController(t *testing.T) {
	t.Parallel()

	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	before, err := h.svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{
		ApprovalMode: domain.PermissionModeAcceptEdits,
	})
	if err != nil {
		t.Fatalf("SetTurnSettings: %v", err)
	}
	driver.startErr = errProviderSwitchTarget

	request := domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode, Model: "claude-foreign-model", Effort: "max", IdempotencyKey: "failed-switch",
	}
	err = h.svc.SwitchProvider(ctx, testSession, request)
	if err == nil || !strings.Contains(err.Error(), "start target provider") {
		t.Fatalf("SwitchProvider error = %v, want target-start failure", err)
	}
	if _, err := h.svc.Controller(testSession); err != nil {
		t.Fatalf("source controller was not restored: %v", err)
	}
	snapshot, err := h.svc.Snapshot(ctx, testSession)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	got := snapshot.Conversation.Settings
	if got.Model != before.Model || got.ReasoningEffort != before.ReasoningEffort || got.ApprovalMode != domain.PermissionModeAcceptEdits {
		t.Fatalf("settings after a failed switch = %+v, want the source's %+v", got, before)
	}
	// The restored source runs with the mode it had, not the one it was launched with.
	driver.mu.Lock()
	resumed := driver.resumeCalls[len(driver.resumeCalls)-1]
	driver.mu.Unlock()
	if resumed.Permissions != domain.PermissionModeAcceptEdits {
		t.Fatalf("restored source permissions = %q, want accept-edits", resumed.Permissions)
	}
	if controller, err := h.svc.Controller(testSession); err != nil || controller.Settings().ApprovalMode != domain.PermissionModeAcceptEdits {
		t.Fatalf("restored controller settings = %+v, %v; want accept-edits", controller.Settings(), err)
	}
	if err := h.svc.SwitchProvider(ctx, testSession, request); err == nil || !strings.Contains(err.Error(), "start target provider") {
		t.Fatalf("replayed failed switch = %v, want target-start failure", err)
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	switchActivities := func() []domain.ConversationActivity {
		t.Helper()
		snapshot, err := h.st.LoadConversationSnapshot(ctx, conversation.ID)
		if err != nil {
			t.Fatal(err)
		}
		var activities []domain.ConversationActivity
		for _, activity := range snapshot.Activities {
			if activity.RequestID == request.IdempotencyKey {
				activities = append(activities, activity)
			}
		}
		return activities
	}
	if activities := switchActivities(); len(activities) != 0 {
		t.Fatalf("failed switch replay recorded activities = %+v, want none", activities)
	}
}

func TestProviderSwitchCommitsTheTargetsSettingsAndKeepsItsLaunchWrites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var h *harness
	var driver *editDriverState
	h, _, driver = newEditHarnessWithControllerEnv(t, false, ownerCheckedLaunch(&h))
	if _, err := h.svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{ApprovalMode: domain.PermissionModeAcceptEdits}); err != nil {
		t.Fatalf("SetTurnSettings: %v", err)
	}
	var whileStarting domain.ConversationSettings
	driver.beforeStart = func(ports.ChatStartConfig) {
		conversation, _ := h.st.ConversationForSession(ctx, testSession)
		whileStarting = conversation.Settings
	}
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode, Model: "claude-target-model",
	}); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	// A daemon that dies while the target starts resumes the source with these.
	if whileStarting.Model != "" || whileStarting.ApprovalMode != domain.PermissionModeAcceptEdits {
		t.Fatalf("settings while the target starts = %+v, want the source's", whileStarting)
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil || conversation.Settings.Model != "claude-target-model" {
		t.Fatalf("settings after the switch = %+v, %v; want the target's model", conversation.Settings, err)
	}
	record, _, err := h.st.GetSession(ctx, testSession)
	if err != nil || record.Metadata.BrowserCapabilityVerifier != launchVerifier(2) {
		t.Fatalf("browser verifier after the switch = %q, %v; want the target launch's", record.Metadata.BrowserCapabilityVerifier, err)
	}
}

func TestProviderSwitchQueuedBehindAnotherStartsFromItsResult(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var h *harness
	var driver *editDriverState
	h, _, driver = newEditHarnessWithControllerEnv(t, false, ownerCheckedLaunch(&h))
	second := make(chan error, 1)
	driver.beforeStart = func(ports.ChatStartConfig) {
		driver.mu.Lock()
		driver.beforeStart = nil
		driver.mu.Unlock()
		go func() {
			second <- h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.AgentHarness("opencode")})
		}()
		time.Sleep(50 * time.Millisecond) // the second switch reaches the lock
	}
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode}); err != nil {
		t.Fatalf("first SwitchProvider: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("second SwitchProvider: %v", err)
	}
	record, _, err := h.st.GetSession(ctx, testSession)
	if err != nil || record.Harness != domain.AgentHarness("opencode") {
		t.Fatalf("session after both switches = %q, %v; want opencode", record.Harness, err)
	}
	if _, err := h.svc.Controller(testSession); err != nil {
		t.Fatalf("no controller after both switches: %v", err)
	}
}

func TestProviderSwitchRestoredTargetKeepsItsHandoffNote(t *testing.T) {
	t.Parallel()

	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode}); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	driver.mu.Lock()
	driver.startErr = errProviderSwitchTarget
	driver.resumed[driver.fresh.providerConversationID] = driver.fresh
	driver.mu.Unlock()
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.AgentHarness("opencode")}); err == nil {
		t.Fatal("second SwitchProvider succeeded, want the target to fail")
	}
	driver.mu.Lock()
	resumed := driver.resumeCalls[len(driver.resumeCalls)-1]
	driver.mu.Unlock()
	if !strings.Contains(resumed.SystemPrompt, domain.HandoffNotePrefix) {
		t.Fatalf("restored Claude Code system prompt has no handoff note: %q", resumed.SystemPrompt)
	}
}

// ownerCheckedLaunch stands in for the session manager: each launch writes its
// own browser verifier, and only while the session still has the owner the
// launch expects.
func ownerCheckedLaunch(h **harness) func(context.Context, domain.SessionControllerOwner, domain.AgentHarness) (map[string]string, error) {
	launches := 0
	return func(ctx context.Context, expected domain.SessionControllerOwner, _ domain.AgentHarness) (map[string]string, error) {
		launches++
		if *h == nil {
			return map[string]string{}, nil
		}
		applied, err := (*h).st.UpdateBrowserCapabilityVerifier(ctx, testSession, expected, launchVerifier(launches))
		if err != nil {
			return nil, err
		}
		if !applied {
			return nil, errors.New("session controller ownership changed")
		}
		return map[string]string{}, nil
	}
}

func launchVerifier(launch int) string { return fmt.Sprintf("verifier-%d", launch) }

func TestProviderSwitchPublicationFailureRestoresSourceController(t *testing.T) {
	t.Parallel()

	h, _, _ := newEditHarnessWithStore(t, false, func(st *store.Store) chatsvc.Store {
		return failingProviderSwitchStore{Store: st, err: errors.New("commit unavailable")}
	})

	err := h.svc.SwitchProvider(context.Background(), testSession, domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode,
	})
	if err == nil || !strings.Contains(err.Error(), "start target provider") {
		t.Fatalf("SwitchProvider error = %v, want publication failure", err)
	}
	if _, err := h.svc.Controller(testSession); err != nil {
		t.Fatalf("source controller was not restored after publication failure: %v", err)
	}
}

type failingProviderSwitchStore struct {
	*store.Store
	err error
}

func (s failingProviderSwitchStore) CommitProviderSwitch(context.Context, domain.SessionRecord, domain.ConversationBranch, string, domain.ConversationSettings, domain.ConversationActivity) error {
	return s.err
}

var errProviderSwitchTarget = ports.ErrChatDriverUnavailable

// lastStart is the newest provider start the driver saw.
func lastStart(driver *editDriverState) ports.ChatStartConfig {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return driver.startConfigs[len(driver.startConfigs)-1]
}

func TestProviderSwitchBackToClaudeOnAutoClearsTheSavedCompactionWindow(t *testing.T) {
	t.Parallel()

	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	for _, req := range []domain.ProviderSwitchRequest{
		{TargetHarness: domain.HarnessClaudeCode, IdempotencyKey: "to-claude", AutoCompactWindow: "1m"},
		{TargetHarness: domain.AgentHarness("opencode"), IdempotencyKey: "to-opencode"},
		{TargetHarness: domain.HarnessClaudeCode, IdempotencyKey: "back-on-auto"},
	} {
		if err := h.svc.SwitchProvider(ctx, testSession, req); err != nil {
			t.Fatalf("switch to %s: %v", req.TargetHarness, err)
		}
	}
	if got, ok := lastStart(driver).Env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; ok {
		t.Fatalf("compaction window after switching back on Auto = %q, want it unset", got)
	}
	if conversation, err := h.st.ConversationForSession(ctx, testSession); err != nil || conversation.Settings.AutoCompactWindow != "" {
		t.Fatalf("saved window = %q, %v; want Auto", conversation.Settings.AutoCompactWindow, err)
	}
}

func TestProviderSwitchTargetPermissionMode(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		target  domain.AgentHarness
		refused ports.PermissionMode // a mode the target will not run in, like Kimi
		picked  domain.PermissionMode
		want    ports.PermissionMode
	}{
		{"under the chat's mode", domain.HarnessClaudeCode, "", "", ports.PermissionModeBypassPermissions},
		{"default when it refuses the chat's", domain.AgentHarness("kimi"), ports.PermissionModeBypassPermissions, "", ports.PermissionModeDefault},
		{"the mode picked for it", domain.HarnessClaudeCode, "", domain.PermissionModeAcceptEdits, ports.PermissionModeAcceptEdits},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, _, driver := newEditHarness(t, false)
			ctx := context.Background()
			if _, err := h.svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{
				ApprovalMode: domain.PermissionModeBypassPermissions,
			}); err != nil {
				t.Fatalf("SetTurnSettings: %v", err)
			}
			driver.mu.Lock()
			driver.refusedMode = tt.refused
			driver.mu.Unlock()
			if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{
				TargetHarness: tt.target, PermissionMode: tt.picked,
			}); err != nil {
				t.Fatalf("SwitchProvider: %v", err)
			}
			if got := lastStart(driver).Permissions; got != tt.want {
				t.Fatalf("target permissions = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProviderSwitchStartsATargetThatRunsOnlyBypassedWithPermissionsBypassed(t *testing.T) {
	t.Parallel()

	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	if _, err := h.svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{
		ApprovalMode: domain.PermissionModeAuto,
	}); err != nil {
		t.Fatalf("SetTurnSettings: %v", err)
	}
	// Like Pi, the target cannot ask before acting.
	driver.mu.Lock()
	driver.onlyMode = ports.PermissionModeBypassPermissions
	driver.mu.Unlock()
	modes, err := h.svc.PermissionModes(ctx, domain.AgentHarness("pi"))
	if err != nil || len(modes) != 1 || modes[0] != ports.PermissionModeBypassPermissions {
		t.Fatalf("PermissionModes = %v, %v; want only bypass-permissions", modes, err)
	}

	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.AgentHarness("pi")}); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	if got := lastStart(driver).Permissions; got != ports.PermissionModeBypassPermissions {
		t.Fatalf("target permissions = %q, want the only mode the agent runs in", got)
	}

	// Leaving it goes back to the chat's own mode, not the one Pi needed.
	driver.mu.Lock()
	driver.onlyMode = ""
	driver.mu.Unlock()
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode}); err != nil {
		t.Fatalf("switch back: %v", err)
	}
	if got := lastStart(driver).Permissions; got != ports.PermissionModeAuto {
		t.Fatalf("permissions after leaving Pi = %q, want the chat's auto", got)
	}
}

func TestResumeStartsAnUnansweredSwitchBranchFresh(t *testing.T) {
	t.Parallel()

	h, _, _ := newEditHarness(t, false)
	ctx := context.Background()
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode}); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	// The target never had a turn, so its provider never saved the conversation.
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	record, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	var resumed ports.ChatResumeConfig
	driver := fakeDriver{conv: newFakeConversation()}
	driver.resume = func(cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
		resumed = cfg
		if !cfg.FreshIfMissing {
			return nil, errors.New("provider conversation not found")
		}
		conv := newFakeConversation()
		conv.providerConversationID = "thread-started-fresh"
		return conv, nil
	}
	restarted := newRestartedEditService(t, h, driver, "unanswered-branch-restart")
	if _, err := restarted.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessClaudeCode, WorkspacePath: t.TempDir(),
		ProviderConversationID: record.Metadata.ProviderConversationID,
	}); err != nil {
		t.Fatalf("Start after an unanswered switch: %v", err)
	}
	// The switch marker gives the conversation a sequence, but the target's
	// standing context has not been delivered yet.
	if resumed.HasPriorTurns {
		t.Fatal("resume of an unanswered switch target says it has prior turns, so its preamble is dropped")
	}
	if !resumed.FreshIfMissing || !strings.Contains(resumed.SystemPrompt, domain.HandoffNotePrefix) {
		t.Fatalf("resume = fresh %v, handoff in prompt %v; want a fresh start carrying the handoff",
			resumed.FreshIfMissing, strings.Contains(resumed.SystemPrompt, domain.HandoffNotePrefix))
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	branch, err := h.st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil || branch.ProviderConversationID != "thread-started-fresh" {
		t.Fatalf("active branch provider = %q, %v; want it moved to the fresh conversation", branch.ProviderConversationID, err)
	}
}

// liveModelConversation runs its own model, as Claude Code's chat does when it
// does not offer the one picked for it.
type liveModelConversation struct {
	*fakeConversation
}

func (c liveModelConversation) ListConfigOptions(context.Context) ([]ports.ChatConfigOption, error) {
	return []ports.ChatConfigOption{{
		ID: "model", Category: "model", Type: ports.ChatConfigOptionSelect,
		Current: ports.ChatConfigOptionValue{Select: "opus"},
		Choices: []ports.ChatConfigOptionChoice{{Value: "opus", Name: "Opus 5.5"}, {Value: "sonnet", Name: "Sonnet 5.5"}},
	}}, nil
}

func (c liveModelConversation) SetConfigOption(context.Context, string, ports.ChatConfigOptionValue) ([]ports.ChatConfigOption, error) {
	return c.ListConfigOptions(context.Background())
}

func TestProviderSwitchSavesTheModelATargetRunsWhenThePickDidNotTake(t *testing.T) {
	t.Parallel()

	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	driver.target = func(conv *fakeConversation) ports.ChatConversation { return liveModelConversation{conv} }
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode, Model: "claude-haiku-5-5",
	}); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil || conversation.Settings.Model != "opus" {
		t.Fatalf("saved model = %q, %v; want the opus the target runs", conversation.Settings.Model, err)
	}
}

func TestFailedSwitchRestoresAnUnansweredTargetThatStartsFresh(t *testing.T) {
	t.Parallel()

	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	// A -> B, and B never answers, so its provider never saved the conversation.
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode}); err != nil {
		t.Fatalf("SwitchProvider to B: %v", err)
	}
	record, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	// B -> C fails to start, so B comes back: fresh, with an id of its own.
	restored := newFakeConversation()
	restored.providerConversationID = "thread-b-fresh"
	driver.mu.Lock()
	driver.startErr = errors.New("target failed to start")
	driver.resumed[record.Metadata.ProviderConversationID] = restored
	driver.mu.Unlock()
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessCodex}); err == nil {
		t.Fatal("SwitchProvider to C succeeded, want its start failure")
	}
	if _, err := h.svc.Controller(testSession); err != nil {
		t.Fatalf("no agent after the failed switch: %v", err)
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	branch, err := h.st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil || branch.ProviderConversationID != "thread-b-fresh" {
		t.Fatalf("active branch provider = %q, %v; want B's fresh conversation", branch.ProviderConversationID, err)
	}
}

func TestSwitchWithAMessageKeepsOtherSwitchesOutUntilItIsSent(t *testing.T) {
	t.Parallel()

	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	release := make(chan struct{})
	targetStarting := make(chan struct{})
	var once sync.Once
	driver.mu.Lock()
	claude := driver.fresh
	driver.beforeStart = func(ports.ChatStartConfig) {
		once.Do(func() {
			close(targetStarting)
			<-release
		})
	}
	driver.mu.Unlock()

	first := make(chan error, 1)
	go func() {
		_, _, err := h.svc.SwitchProviderAndSend(ctx, testSession,
			domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode, IdempotencyKey: "first"},
			ports.ChatUserMessage{Text: "Only Claude should receive this", ClientMessageID: "first-message"})
		first <- err
	}()
	<-targetStarting
	second := make(chan error, 1)
	go func() {
		second <- h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.AgentHarness("opencode"), IdempotencyKey: "second"})
	}()
	select {
	case err := <-second:
		t.Fatalf("a second switch ran while the first was under way: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	// It runs after the message reached Claude, which is now working on it.
	var refusal *domain.ProviderSwitchError
	if err := <-second; !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalBusy {
		t.Fatalf("second switch = %v, want it refused while Claude works on the message", err)
	}
	if got := claude.sentTexts(); len(got) != 1 {
		t.Fatalf("Claude received %q, want the message sent with its switch", got)
	}
}

func TestNewMessageToAnAlreadyActiveTargetRunsWithItsPicks(t *testing.T) {
	t.Parallel()

	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	// Another window already brought Claude in, bypassed and on the older model.
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode, IdempotencyKey: "other-window",
		Model: "model-old", PermissionMode: domain.PermissionModeBypassPermissions,
	}); err != nil {
		t.Fatal(err)
	}
	// This window still shows the source; it picks Claude with Accept edits and sends a new message.
	if _, switched, err := h.svc.SwitchProviderAndSend(ctx, testSession, domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode, IdempotencyKey: "new-pick",
		Model: "model-picked", PermissionMode: domain.PermissionModeAcceptEdits,
	}, ports.ChatUserMessage{Text: "Use the selected permissions", ClientMessageID: "new-message"}); err != nil || !switched {
		t.Fatalf("switch and send: switched %v, %v", switched, err)
	}
	driver.mu.Lock()
	claude := driver.fresh
	driver.mu.Unlock()
	sent := claude.sentMessages()
	if len(sent) != 1 || sent[0].Settings.Approval != ports.PermissionModeAcceptEdits || sent[0].Settings.Model != "model-picked" {
		t.Fatalf("delivered %+v; want the picked accept-edits and model-picked", sent)
	}
}

func TestRetryOfADeliveredMessageDoesNotSwitchBack(t *testing.T) {
	t.Parallel()

	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	message := ports.ChatUserMessage{Text: "First message already delivered", ClientMessageID: "replayed-message"}
	if _, _, err := h.svc.SwitchProviderAndSend(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode, IdempotencyKey: "first"}, message); err != nil {
		t.Fatal(err)
	}
	driver.mu.Lock()
	claude := driver.fresh
	driver.mu.Unlock()
	claude.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-101"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-101", TurnState: domain.TurnStateCompleted})
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if snapshot, err := h.svc.Snapshot(ctx, testSession); err == nil && len(snapshot.Turns) == 1 && snapshot.Turns[0].State == domain.TurnStateCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first turn did not finish")
		}
	}
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.AgentHarness("opencode"), IdempotencyKey: "second"}); err != nil {
		t.Fatal(err)
	}
	// The renderer sends a fresh switch key with each attempt, and the same delivery id.
	turn, switched, err := h.svc.SwitchProviderAndSend(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode, IdempotencyKey: "retry"}, message)
	if err != nil || !switched || turn.ID != "" {
		t.Fatalf("retry = %+v, switched %v, %v; want a duplicate", turn, switched, err)
	}
	if rec, _, _ := h.st.GetSession(ctx, testSession); rec.Harness != domain.AgentHarness("opencode") {
		t.Fatalf("harness after the retry = %q, want the later switch's opencode", rec.Harness)
	}
}

type fakeProjects struct {
	denylist []string
	err      error
}

func (f fakeProjects) GetProject(context.Context, string) (domain.ProjectRecord, bool, error) {
	if f.err != nil {
		return domain.ProjectRecord{}, false, f.err
	}
	return domain.ProjectRecord{Config: domain.ProjectConfig{Models: domain.ModelPolicy{Denylist: f.denylist}}}, true, nil
}

func switchTo(h *harness, model string) error {
	return h.svc.SwitchProvider(context.Background(), testSession, domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode, Model: model,
	})
}

// The project model denylist gates the in-chat switch as it gates spawn.
func TestProviderSwitchRefusesDeniedModel(t *testing.T) {
	t.Parallel()
	h, _, _ := newEditHarnessWithControllerEnv(t, false, nil)
	h.svc.SetProjects(fakeProjects{denylist: []string{"blocked-model"}})

	err := switchTo(h, "Blocked-Model")
	var refusal *domain.ProviderSwitchError
	if !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalModelDenied {
		t.Fatalf("switch to a denied model = %v, want a model_denied refusal", err)
	}
	var denied *domain.ModelDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("refusal does not carry the policy error: %v", err)
	}
	if conversation, cerr := h.st.ConversationForSession(context.Background(), testSession); cerr != nil || conversation.Settings.Model == "Blocked-Model" {
		t.Fatalf("the denied switch changed the conversation: %+v %v", conversation.Settings, cerr)
	}
}

func TestProviderSwitchAllowsPermittedModel(t *testing.T) {
	t.Parallel()
	h, _, _ := newEditHarnessWithControllerEnv(t, false, nil)
	h.svc.SetProjects(fakeProjects{denylist: []string{"blocked-model"}})
	if err := switchTo(h, "claude-target-model"); err != nil {
		t.Fatalf("switch to an allowed model: %v", err)
	}
}

// Fail closed: a project whose config cannot be read cannot be switched to a named model.
func TestProviderSwitchRefusesWhenPolicyUnreadable(t *testing.T) {
	t.Parallel()
	h, _, _ := newEditHarnessWithControllerEnv(t, false, nil)
	h.svc.SetProjects(fakeProjects{err: errors.New("db locked")})
	err := switchTo(h, "claude-target-model")
	var refusal *domain.ProviderSwitchError
	if !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalUnavailable || !errors.Is(err, chatsvc.ErrModelPolicyUnavailable) {
		t.Fatalf("switch with unreadable policy = %v, want an unavailable refusal", err)
	}
}

func TestSetTurnSettingsRefusesDeniedModel(t *testing.T) {
	t.Parallel()
	h, _, _ := newEditHarnessWithControllerEnv(t, false, nil)
	h.svc.SetProjects(fakeProjects{denylist: []string{"blocked-model"}})
	_, err := h.svc.SetTurnSettings(context.Background(), testSession, domain.ConversationSettings{Model: "blocked-model"})
	var denied *domain.ModelDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("SetTurnSettings with a denied model = %v", err)
	}
	if _, err := h.svc.SetTurnSettings(context.Background(), testSession, domain.ConversationSettings{Model: "fine-model"}); err != nil {
		t.Fatalf("SetTurnSettings with an allowed model: %v", err)
	}
}

// A reviewer chat is policed against the project its review belongs to.
func TestReviewerChatModelChangeRefusesDeniedModel(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := st.UpsertReview(ctx, domain.Review{ID: "review-policy", SessionID: testSession, ProjectID: testProject, Harness: domain.ReviewerCodex, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation()}},
		Log: slog.New(slog.DiscardHandler), NewID: func() string { return "review-policy-conversation" },
	})
	svc.SetProjects(fakeProjects{denylist: []string{"blocked-model"}})
	owner := domain.ReviewConversationOwner("review-policy")
	t.Cleanup(func() { _ = svc.StopForOwner(ctx, owner) })
	if _, err := svc.Start(ctx, chatsvc.StartConfig{Owner: owner, SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeAuto}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.SetTurnSettingsForOwner(ctx, owner, domain.ConversationSettings{Model: "blocked-model", ApprovalMode: domain.PermissionModeAuto})
	var denied *domain.ModelDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("reviewer chat model change to a denied model = %v", err)
	}
	if _, err := svc.SetTurnSettingsForOwner(ctx, owner, domain.ConversationSettings{Model: "fine-model", ApprovalMode: domain.PermissionModeAuto}); err != nil {
		t.Fatalf("allowed model: %v", err)
	}
}

// A resume that replaced a denied model says so on the timeline.
func TestStartPostsTheModelPolicyNotice(t *testing.T) {
	t.Parallel()
	h, _, _ := newEditHarnessWithControllerEnv(t, false, nil)
	ctx := context.Background()
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	notice := "Model banned is blocked by project policy; resumed on fine"
	ctrl, err := h.svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeAuto, Model: "fine", ModelNotice: notice,
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := h.st.LoadConversationSnapshot(ctx, ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	for _, activity := range snap.Activities {
		if activity.Kind == domain.ActivityKindSystem && activity.Summary == notice {
			return
		}
	}
	t.Fatalf("notice %q not on the timeline: %+v", notice, snap.Activities)
}

// The notice goes on the timeline before a start can fail; with no conversation
// yet it reports false so the start posts it instead.
func TestPostSessionNotice(t *testing.T) {
	t.Parallel()
	h, _, _ := newEditHarnessWithControllerEnv(t, false, nil)
	ctx := context.Background()
	posted, err := h.svc.PostSessionNotice(ctx, "no-such-session", "x")
	if posted || err != nil {
		t.Fatalf("no conversation: posted=%v err=%v, want false/nil", posted, err)
	}
	if posted, err := h.svc.PostSessionNotice(ctx, testSession, "Model a is blocked by project policy; resumed on b"); !posted || err != nil {
		t.Fatalf("posted=%v err=%v", posted, err)
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := h.st.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range snap.Activities {
		if a.Kind == domain.ActivityKindSystem && strings.Contains(a.Summary, "blocked by project policy") {
			return
		}
	}
	t.Fatalf("notice missing from the timeline: %+v", snap.Activities)
}

func TestProviderSwitchToCodexKeepsTheRequestedCompactionWindow(t *testing.T) {
	t.Parallel()

	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	for _, req := range []domain.ProviderSwitchRequest{
		{TargetHarness: domain.HarnessClaudeCode, IdempotencyKey: "to-claude"},
		{TargetHarness: domain.HarnessCodex, IdempotencyKey: "to-codex", AutoCompactWindow: "200k"},
	} {
		if err := h.svc.SwitchProvider(ctx, testSession, req); err != nil {
			t.Fatal(err)
		}
	}
	if got := lastStart(driver).Env[contextwindow.CodexAutoCompactLimitEnv]; got != "200000" {
		t.Fatalf("codex limit = %q, want 200000", got)
	}
	if conversation, err := h.st.ConversationForSession(ctx, testSession); err != nil || conversation.Settings.AutoCompactWindow != "200k" {
		t.Fatalf("saved window = %q, %v; want 200k", conversation.Settings.AutoCompactWindow, err)
	}
}
