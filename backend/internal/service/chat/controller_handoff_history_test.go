package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/lifecycle"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

func TestOrdinaryResumeRejectsProviderHandleOutsideActiveBranch(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	record, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	record.Metadata.ProviderConversationID = "active-provider-thread"
	if err := st.UpdateSession(context.Background(), record); err != nil {
		t.Fatalf("seed active provider handle: %v", err)
	}
	if _, err := st.CreateConversation(context.Background(), "ordinary-resume-conversation",
		domain.ConversationScopeSession, testProject, testSession, now); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	resumeCalls := 0
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{
			conv: newFakeConversation(),
			resume: func(ports.ChatResumeConfig) (ports.ChatConversation, error) {
				resumeCalls++
				return newFakeConversation(), nil
			},
		}},
		Log:   slog.New(slog.DiscardHandler),
		NewID: func() string { return "unused-ordinary-resume-conversation" },
	})

	_, err = svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ProviderConversationID: "unowned-provider-thread",
		HistoryMode:            ports.ChatHistoryDeferred,
	})
	if err == nil || !strings.Contains(err.Error(), "does not match session handle") {
		t.Fatalf("ordinary resume error = %v, want active-branch handle mismatch", err)
	}
	if resumeCalls != 0 {
		t.Fatalf("driver resume calls = %d, want mismatch rejected before provider launch", resumeCalls)
	}
}

func TestFreshProjectControllerReadyFailurePreservesSourceHeadAndOwner(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 21, 15, 0, 0, 0, time.UTC)
	before, sourceBranch := seedProjectConversationWithProviderHistory(
		t, st, "failed-fresh-project-ready-conversation", now)
	readyErr := errors.New("lifecycle ownership commit failed")
	var reservedBoundary *domain.ConversationBranch
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: func() *fakeConversation {
			conversation := newFakeConversation()
			conversation.providerConversationID = "fresh-provider-thread"
			return conversation
		}()}},
		Log:   slog.New(slog.DiscardHandler),
		NewID: func() string { return "failed-ready-provider-boundary" },
		Now:   func() time.Time { return now.Add(2 * time.Second) },
	})

	_, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			reservedBoundary = result.ProviderBoundary
			return chatsvc.ControllerCommit{}, readyErr
		},
	})
	if !errors.Is(err, readyErr) {
		t.Fatalf("Start error = %v, want ControllerReady failure", err)
	}
	if reservedBoundary == nil || reservedBoundary.ID == "" ||
		reservedBoundary.ID == sourceBranch.ProviderScopeID ||
		reservedBoundary.ProviderScopeID != reservedBoundary.ID {
		t.Fatalf("reserved provider boundary = %+v, source scope %q",
			reservedBoundary, sourceBranch.ProviderScopeID)
	}
	after, err := st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession after callback failure: %v", err)
	}
	if after.ActiveBranchID != before.ActiveBranchID {
		t.Fatalf("active branch after callback failure = %q, want source %q",
			after.ActiveBranchID, before.ActiveBranchID)
	}
	record, found, err := st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession after callback failure: found=%v err=%v", found, err)
	}
	if record.Metadata.ProviderConversationID != "source-provider-thread" ||
		record.Metadata.ControllerGeneration != "source-generation" {
		t.Fatalf("provider owner after callback failure = handle %q generation %q",
			record.Metadata.ProviderConversationID, record.Metadata.ControllerGeneration)
	}
	if _, err := st.ConversationBranch(ctx, before.ID, reservedBoundary.ID); !errors.Is(err, domain.ErrNoConversationBranch) {
		t.Fatalf("failed callback boundary lookup error = %v, want ErrNoConversationBranch", err)
	}
}

func TestResumeCanSkipNativeHistoryImportWithoutStartingFresh(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	historyReads := 0
	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events: []ports.ChatEvent{{
			Kind: ports.ChatEventTurnStarted, ProviderTurnID: "old-target-turn",
		}},
		onRead: func(int) { historyReads++ },
	}
	conv.providerConversationID = "target-native-thread"
	var started ports.ChatStartConfig
	var resumed ports.ChatResumeConfig
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{
			conv: conv, startCfg: &started, resumeCfg: &resumed,
		}},
		Log:   slog.New(slog.DiscardHandler),
		NewID: func() string { return "skip-native-history-import" },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	controller, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath:          t.TempDir(),
		Model:                  "selected-target-model",
		ProviderConversationID: "target-native-thread",
		HistoryMode:            ports.ChatHistoryDeferred,
	})
	if err != nil {
		t.Fatalf("Start resume without history import: %v", err)
	}
	if resumed.ProviderConversationID != "target-native-thread" {
		t.Fatalf("resume config = %#v, want target native thread", resumed)
	}
	if resumed.Model != "selected-target-model" {
		t.Fatalf("resume model = %q, want selected-target-model", resumed.Model)
	}
	if started.SessionID != "" {
		t.Fatalf("fresh start was used instead of resume: %#v", started)
	}
	if historyReads != 0 {
		t.Fatalf("native history reads = %d, want none before provider boundary commit", historyReads)
	}
	if got := controller.ProviderConversationID(); got != "target-native-thread" {
		t.Fatalf("controller provider conversation = %q, want resumed target", got)
	}
}

func TestResumeImportsNativeHistoryBeforeTheChatControllerStarts(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	existing, err := st.CreateConversation(context.Background(), "existing-conversation",
		domain.ConversationScopeSession, testProject, testSession, now)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.ClaimChatControllerGeneration(context.Background(), testSession, "old-generation"); err != nil {
		t.Fatalf("ClaimChatControllerGeneration: %v", err)
	}
	created, err := st.AppendUserMessage(context.Background(), existing.ID, testSession, "old-generation",
		domain.ConversationMessage{
			ID: "existing-user", Text: "What changed?", Origin: domain.MessageOriginHuman,
			ClientMessageID: "original-chat-client-id",
		}, "existing-turn", now)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage: created=%v err=%v", created, err)
	}
	if err := st.BindTurnToProvider(context.Background(), "existing-turn", "native-turn-1", now); err != nil {
		t.Fatalf("BindTurnToProvider: %v", err)
	}
	if err := st.SettleAssistantMessage(context.Background(), existing.ID,
		"native-answer-1", "native-turn-1", "Nothing is dirty.", "existing-answer", now); err != nil {
		t.Fatalf("SettleAssistantMessage: %v", err)
	}
	if err := st.UpsertActivity(context.Background(), existing.ID, "native-turn-1",
		domain.ConversationActivity{
			ID: "existing-command", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted,
			Summary: "Ran git status", Detail: json.RawMessage(`{"command":"git status"}`), ProviderItemID: "native-command-1",
		}, now); err != nil {
		t.Fatalf("UpsertActivity: %v", err)
	}
	if err := st.SettleTurn(context.Background(), existing.ID, "native-turn-1", domain.TurnStateCompleted, "", now); err != nil {
		t.Fatalf("SettleTurn: %v", err)
	}

	base := newFakeConversation()
	conv := &nativeHistoryConversation{
		fakeConversation: base,
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "history-start", ProviderTurnID: "native-turn-1"},
			{
				Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "history-user",
				ProviderTurnID: "native-turn-1", ProviderItemID: "history-item-1",
				ClientMessageID: "native-client-1", Text: "What changed?",
			},
			{
				Kind: ports.ChatEventActivityCompleted, ProviderEventID: "history-command",
				ProviderTurnID: "native-turn-1", ProviderItemID: "history-item-2",
				ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusCompleted,
				Summary: "Ran git status", Detail: json.RawMessage(`{"command":"git status"}`),
			},
			{
				Kind: ports.ChatEventActivityCompleted, ProviderEventID: "history-new-command",
				ProviderTurnID: "native-turn-1", ProviderItemID: "history-item-new-command",
				ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusCompleted,
				Summary: "Ran git diff", Detail: json.RawMessage(`{"command":"git diff"}`),
			},
			{
				Kind: ports.ChatEventMessageCompleted, ProviderEventID: "history-answer",
				ProviderTurnID: "native-turn-1", ProviderItemID: "history-item-3", Text: "Nothing is dirty.",
			},
			{
				Kind: ports.ChatEventTurnCompleted, ProviderEventID: "history-complete",
				ProviderTurnID: "native-turn-1", TurnState: domain.TurnStateRecovered,
			},
		},
	}

	var idMu sync.Mutex
	nextID := 0
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
			rows, err := st.LoadConversationSnapshot(ctx, conversationID)
			if err != nil {
				return chatsvc.ConversationRows{}, err
			}
			return chatsvc.ConversationRows{
				Conversation: rows.Conversation,
				Turns:        rows.Turns,
				Messages:     rows.Messages,
				Activities:   rows.Activities,
			}, nil
		}),
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			idMu.Lock()
			defer idMu.Unlock()
			nextID++
			return fmt.Sprintf("history-id-%d", nextID)
		},
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if err != nil {
		t.Fatalf("Start resume: %v", err)
	}
	snapshot, err := st.LoadConversationSnapshot(context.Background(), ctrl.ConversationID())
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	// The turn already existed from an earlier Chat interval. Codex can omit its
	// persisted item ids, so the replay uses synthetic item ids even though the
	// live assistant message used native-answer-1. Stable turn identity and the
	// settled content keep the replay from duplicating either message, while the
	// command Tallpine already knew is deduplicated too, while the new command that Tallpine
	// had not seen yet is still imported.
	if len(snapshot.Messages) != 2 || snapshot.Messages[0].Text != "What changed?" || snapshot.Messages[1].Text != "Nothing is dirty." {
		t.Fatalf("imported messages = %#v", snapshot.Messages)
	}
	if len(snapshot.Activities) != 2 || snapshot.Activities[0].Summary != "Ran git status" || snapshot.Activities[1].Summary != "Ran git diff" {
		t.Fatalf("imported activities = %#v", snapshot.Activities)
	}
	if len(snapshot.Turns) != 1 || snapshot.Turns[0].State != domain.TurnStateCompleted {
		t.Fatalf("imported turns = %#v", snapshot.Turns)
	}
	if snapshot.Turns[0].ProviderTurnID != "native-turn-1" {
		t.Fatalf("provider turn = %q, want durable native-turn-1", snapshot.Turns[0].ProviderTurnID)
	}
	if snapshot.Turns[0].CompletedAt == nil || !snapshot.Turns[0].CompletedAt.Equal(now) {
		t.Fatalf("replayed completion = %v, want original %s", snapshot.Turns[0].CompletedAt, now)
	}
}

func TestOrdinaryResumeAllowsACPContextWithoutHistoryReplay(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		err:              ports.ErrChatHistoryUnavailable,
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("context-only-%d", time.Now().UnixNano()) },
	})

	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1",
	}); err != nil {
		t.Fatalf("ordinary resume with provider context: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
}

func TestFreshProjectControllerRecordsNativeContextBoundary(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	conversation, err := st.CreateConversation(ctx, "project-conversation",
		domain.ConversationScopeProject, testProject, testSession, now)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.UpsertActivity(ctx, conversation.ID, "", domain.ConversationActivity{
		ID: "old-activity", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
		Summary: "Earlier project history", ProviderItemID: "old-project-history",
	}, now); err != nil {
		t.Fatalf("seed project history: %v", err)
	}

	const replacement = domain.SessionID("p1-2")
	if _, err := st.CreateSession(ctx, domain.SessionRecord{
		ID:        replacement,
		ProjectID: testProject,
		Kind:      domain.KindOrchestrator,
		Harness:   domain.HarnessCodex,
		Mode:      domain.SessionModeChat,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata:  domain.SessionMetadata{Branch: "feat/replacement", WorkspacePath: t.TempDir()},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed replacement session: %v", err)
	}

	var idMu sync.Mutex
	nextID := 0
	driver := fakeDriver{}
	driver.start = func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
		conv := newFakeConversation()
		conv.providerConversationID = "thread-" + cfg.ProviderScopeID
		return conv, nil
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: driver},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			idMu.Lock()
			defer idMu.Unlock()
			nextID++
			return fmt.Sprintf("boundary-id-%d", nextID)
		},
		Now: func() time.Time { return now.Add(time.Minute) },
	})
	start := chatsvc.StartConfig{
		SessionID: replacement, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
	}
	if _, err := svc.Start(ctx, start); err != nil {
		t.Fatalf("Start replacement: %v", err)
	}

	rebound, err := st.ConversationForSession(ctx, replacement)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	if rebound.ID != conversation.ID {
		t.Fatalf("conversation = %q, want project narrative %q", rebound.ID, conversation.ID)
	}
	snapshot, err := st.LoadConversationSnapshot(ctx, rebound.ID)
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	if len(snapshot.Activities) != 2 {
		t.Fatalf("activities = %#v, want old history plus context boundary", snapshot.Activities)
	}
	firstScope := snapshot.ActiveBranch.ProviderScopeID
	boundary := snapshot.Activities[1]
	if boundary.Kind != domain.ActivityKindSystem ||
		boundary.ProviderItemID != domain.ConversationContextResetProviderItemID(replacement) {
		t.Fatalf("boundary = %#v", boundary)
	}
	var detail map[string]string
	if err := json.Unmarshal(boundary.Detail, &detail); err != nil {
		t.Fatalf("decode boundary detail: %v", err)
	}
	if detail["event"] != "context.reset" {
		t.Fatalf("boundary event = %q", detail["event"])
	}
	firstTurn, err := svc.Send(ctx, replacement, ports.ChatUserMessage{
		Text: "first prompt in fresh context", ClientMessageID: "fresh-context-first-prompt",
	})
	if err != nil {
		t.Fatalf("Send first fresh-context prompt: %v", err)
	}
	anchor, err := st.ConversationEditAnchor(ctx, rebound.ID, firstTurn.ID)
	if err != nil {
		t.Fatalf("ConversationEditAnchor first fresh-context prompt: %v", err)
	}
	if anchor.HasPriorContext {
		t.Fatalf("first fresh-context prompt sees display-only reset marker as prior context: %+v", anchor)
	}

	if err := svc.Stop(ctx, replacement); err != nil {
		t.Fatalf("Stop first replacement: %v", err)
	}
	if _, err := svc.Start(ctx, start); err != nil {
		t.Fatalf("Start second fresh provider: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), replacement) })
	secondSnapshot, err := st.LoadConversationSnapshot(ctx, rebound.ID)
	if err != nil {
		t.Fatalf("LoadConversationSnapshot second boundary: %v", err)
	}
	if len(secondSnapshot.Activities) != 2 {
		t.Fatalf("activities after second start = %#v, want the session-scoped boundary to remain unique",
			secondSnapshot.Activities)
	}
	secondScope := secondSnapshot.ActiveBranch.ProviderScopeID
	if secondScope == firstScope {
		t.Fatalf("second provider scope = %q, reused first scope", secondScope)
	}
	if secondSnapshot.Activities[1].ProviderItemID != boundary.ProviderItemID {
		t.Fatalf("session boundary changed across provider scopes: second = %#v, first = %#v",
			secondSnapshot.Activities[1], boundary)
	}
}

func TestFreshProjectControllerStartFailureKeepsPreviousHistoryHidden(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	conversation, err := st.CreateConversation(ctx, "project-conversation",
		domain.ConversationScopeProject, testProject, testSession, now)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if _, err := st.AppendUserMessage(ctx, conversation.ID, testSession, "old-generation",
		domain.ConversationMessage{
			ID: "old-message", Text: "old orchestrator history", Origin: domain.MessageOriginHuman,
		}, "old-turn", now.Add(time.Second)); err != nil {
		t.Fatalf("seed old history: %v", err)
	}

	replacementRecord, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject,
		Kind:      domain.KindOrchestrator,
		Harness:   domain.HarnessCodex,
		Mode:      domain.SessionModeChat,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata:  domain.SessionMetadata{Branch: "feat/replacement", WorkspacePath: t.TempDir()},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed replacement session: %v", err)
	}
	replacement := replacementRecord.ID

	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{
			start: func(ports.ChatStartConfig) (ports.ChatConversation, error) {
				return nil, errors.New("provider failed")
			},
		}},
		Log: slog.New(slog.DiscardHandler),
		NewID: func() string {
			return "boundary-id"
		},
		Now: func() time.Time { return now.Add(2 * time.Second) },
	})
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: replacement, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
	}); err == nil || !strings.Contains(err.Error(), "provider failed") {
		t.Fatalf("Start replacement error = %v, want provider failure", err)
	}

	snapshot, err := st.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	if len(snapshot.Activities) != 1 ||
		snapshot.Activities[0].ProviderItemID != domain.ConversationContextResetProviderItemID(replacement) {
		t.Fatalf("reset boundary after failed start = %#v", snapshot.Activities)
	}
	page, err := st.LoadConversationSnapshotPage(ctx, conversation.ID, 0, 10)
	if err != nil {
		t.Fatalf("LoadConversationSnapshotPage: %v", err)
	}
	if len(page.Messages) != 0 || len(page.Activities) != 0 || len(page.Turns) != 0 || page.HasMoreBefore {
		t.Fatalf("page after failed start = messages %#v activities %#v turns %#v hasMore %v, want empty",
			page.Messages, page.Activities, page.Turns, page.HasMoreBefore)
	}
}

/* ---- harness ----------------------------------------------------------- */

// Publishing a reserved provider branch preserves its predecessor's ownership.
func TestReservedBoundaryAdoptsSuccessorHandleWithoutRewritingHistory(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 4, 41, 0, 0, time.UTC)
	const (
		successor = "01a08d27-878b-7db0-9e84-9a921099f4a8"
		boundary  = "handoff-309:provider"
	)
	before, sourceBranch := seedProjectConversationWithProviderHistory(
		t, st, "forked-handle-conversation", now)
	ancestor := sourceBranch.ProviderConversationID
	if ancestor == "" || ancestor == successor {
		t.Fatalf("seeded ancestor handle = %q, want a distinct recorded handle", ancestor)
	}

	resumedConversation := newFakeConversation()
	resumedConversation.providerConversationID = successor
	var resumed ports.ChatResumeConfig
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: resumedConversation, resumeCfg: &resumed}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "forked-handle-generation" },
		Now:     func() time.Time { return now.Add(time.Minute) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	lifecycleManager := lifecycle.New(st, nil)

	var boundaryBranch domain.ConversationBranch
	_, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ProviderConversationID: successor,
		ProviderScopeID:        boundary,
		HistoryMode:            ports.ChatHistoryDeferred,
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			if result.ProviderBoundary == nil {
				return chatsvc.ControllerCommit{}, errors.New("successor boundary was not reserved")
			}
			boundaryBranch = *result.ProviderBoundary
			if err := lifecycleManager.MarkChatSpawned(ctx, testSession, domain.SessionMetadata{
				ProviderConversationID: result.ProviderConversationID,
				ControllerGeneration:   result.ControllerGeneration,
			}, boundaryBranch); err != nil {
				return chatsvc.ControllerCommit{}, err
			}
			committed := result.Conversation
			committed.ActiveBranchID = boundaryBranch.ID
			committed.UpdatedAt = boundaryBranch.CreatedAt
			return chatsvc.ControllerCommit{Conversation: committed}, nil
		},
	})
	if err != nil {
		t.Fatalf("Start successor handle in reserved boundary: %v", err)
	}
	if resumed.ProviderConversationID != successor {
		t.Fatalf("resumed handle = %q, want the successor thread", resumed.ProviderConversationID)
	}
	if boundaryBranch.ID != boundary || boundaryBranch.ParentBranchID != sourceBranch.ID ||
		boundaryBranch.ProviderConversationID != successor ||
		boundaryBranch.ForkAfterSequence != before.LatestSequence {
		t.Fatalf("successor boundary = %+v, want %q chained onto %q at sequence %d",
			boundaryBranch, boundary, sourceBranch.ID, before.LatestSequence)
	}
	keptSource, err := st.ConversationBranch(ctx, before.ID, sourceBranch.ID)
	if err != nil {
		t.Fatalf("ancestor ConversationBranch: %v", err)
	}
	if keptSource.ProviderConversationID != ancestor ||
		keptSource.ProviderScopeID != sourceBranch.ProviderScopeID {
		t.Fatalf("ancestor branch = %+v, want the recorded handle untouched", keptSource)
	}
}
