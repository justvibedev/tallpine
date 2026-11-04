package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/lifecycle"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

func TestSuccessfulChatProbeIsReusedByStart(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	probes := 0
	nextID := 0
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{
			conv: newFakeConversation(),
			probe: func() error {
				probes++
				return nil
			},
		}},
		Log: slog.New(slog.DiscardHandler),
		NewID: func() string {
			nextID++
			return fmt.Sprintf("probe-cache-%d", nextID)
		},
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	if err := svc.PreflightChat(context.Background(), domain.HarnessCodex, ports.PermissionModeDefault, agentpolicy.Decision{}); err != nil {
		t.Fatalf("PreflightChat: %v", err)
	}
	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if probes != 1 {
		t.Fatalf("Probe calls = %d, want 1 successful probe reused by Start", probes)
	}
}

func TestFailedChatProbeCanBeRetriedThenCached(t *testing.T) {
	t.Parallel()

	attempts := 0
	driver := fakeDriver{probe: func() error {
		attempts++
		if attempts == 1 {
			return errors.New("provider still installing")
		}
		return nil
	}}
	svc := chatsvc.New(chatsvc.Options{Drivers: fakeRegistry{driver: driver}})

	if err := svc.PreflightChat(context.Background(), domain.HarnessCodex, ports.PermissionModeDefault, agentpolicy.Decision{}); err == nil {
		t.Fatal("first PreflightChat must surface the transient probe failure")
	}
	if err := svc.PreflightChat(context.Background(), domain.HarnessCodex, ports.PermissionModeDefault, agentpolicy.Decision{}); err != nil {
		t.Fatalf("second PreflightChat: %v", err)
	}
	if err := svc.PreflightChat(context.Background(), domain.HarnessCodex, ports.PermissionModeDefault, agentpolicy.Decision{}); err != nil {
		t.Fatalf("cached PreflightChat: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("Probe attempts = %d, want one failed attempt plus one cached success", attempts)
	}
}

func TestCapabilityCacheEvaluatesEveryRequestedPermissionMode(t *testing.T) {
	t.Parallel()

	probes := 0
	driver := fakeDriver{
		caps: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilityResume:    true,
		},
		probe: func() error {
			probes++
			return nil
		},
	}
	svc := chatsvc.New(chatsvc.Options{Drivers: fakeRegistry{driver: driver}})

	if err := svc.PreflightChat(context.Background(), domain.HarnessCodex, ports.PermissionModeDefault, agentpolicy.Decision{}); !errors.Is(err, ports.ErrChatUnsupported) {
		t.Fatalf("default preflight error = %v, want ErrChatUnsupported", err)
	}
	if err := svc.PreflightChat(context.Background(), domain.HarnessCodex, ports.PermissionModeBypassPermissions, agentpolicy.Decision{}); err != nil {
		t.Fatalf("bypass preflight: %v", err)
	}
	if err := svc.PreflightChat(context.Background(), domain.HarnessCodex, ports.PermissionModeDefault, agentpolicy.Decision{}); !errors.Is(err, ports.ErrChatUnsupported) {
		t.Fatalf("cached default preflight error = %v, want ErrChatUnsupported", err)
	}
	if probes != 1 {
		t.Fatalf("Probe calls = %d, want one raw capability probe reused across permission modes", probes)
	}
}

func TestResumeUsesPersistedBypassPermissionForCapabilityAdmission(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	conversation, err := st.CreateConversation(
		ctx, "persisted-bypass-conversation", domain.ConversationScopeProject,
		testProject, testSession, now,
	)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.SetConversationSettings(ctx, conversation.ID, domain.ConversationSettings{
		Model:           "gpt-5.6-luna",
		ReasoningEffort: "high",
		ApprovalMode:    domain.PermissionModeBypassPermissions,
	}, now); err != nil {
		t.Fatalf("SetConversationSettings: %v", err)
	}

	conv := newFakeConversation()
	conv.providerConversationID = "thread-bypass"
	var resumed ports.ChatResumeConfig
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{
			conv: conv, resumeCfg: &resumed,
			caps: ports.ChatCapabilities{
				ports.ChatCapabilityStreaming: true,
				ports.ChatCapabilityInterrupt: true,
				ports.ChatCapabilityResume:    true,
			},
		}},
		Log:   slog.New(slog.DiscardHandler),
		NewID: func() string { return "persisted-bypass-start" },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-bypass",
		Permissions: ports.PermissionModeDefault,
	}); err != nil {
		t.Fatalf("Start resume: %v", err)
	}
	if resumed.Permissions != ports.PermissionModeBypassPermissions {
		t.Fatalf("resume permissions = %q, want persisted bypass", resumed.Permissions)
	}
	if resumed.Model != "gpt-5.6-luna" {
		t.Fatalf("resume model = %q, want persisted model", resumed.Model)
	}
	if resumed.Effort != "high" {
		t.Fatalf("resume effort = %q, want persisted effort", resumed.Effort)
	}
	controller, err := svc.Controller(testSession)
	if err != nil {
		t.Fatalf("Controller: %v", err)
	}
	if settings := controller.Settings(); settings.Model != "gpt-5.6-luna" || settings.ReasoningEffort != "high" {
		t.Fatalf("controller settings = %+v, want persisted model and effort", settings)
	}
}

func TestServicePassesRecomputedSystemPromptToResume(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	existing, err := st.CreateConversation(context.Background(), "conversation-resume",
		domain.ConversationScopeSession, testProject, testSession, time.Now())
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.SetConversationSettings(context.Background(), existing.ID, domain.ConversationSettings{
		Model: "gpt-test", ReasoningEffort: "high",
	}, time.Now()); err != nil {
		t.Fatalf("SetConversationSettings: %v", err)
	}
	conv := newFakeConversation()
	var resumed ports.ChatResumeConfig
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv, resumeCfg: &resumed}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "conversation-resume" },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	workspace := t.TempDir()
	dataDir := t.TempDir()
	_, err = svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		DataDir: dataDir, WorkspacePath: workspace, ProviderConversationID: "thread-1",
		SystemPrompt: "Recomputed Tallpine orchestrator instructions",
	})
	if err != nil {
		t.Fatalf("Start resume: %v", err)
	}
	if resumed.ProviderConversationID != "thread-1" || resumed.DataDir != dataDir || resumed.WorkspacePath != workspace ||
		resumed.SystemPrompt != "Recomputed Tallpine orchestrator instructions" || resumed.Model != "gpt-test" ||
		resumed.Effort != "high" {
		t.Fatalf("resume config = %#v", resumed)
	}
	snapshot, err := st.LoadConversationSnapshot(context.Background(), "conversation-resume")
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	if snapshot.Conversation.Settings.Model != "gpt-test" || snapshot.Conversation.Settings.ReasoningEffort != "high" {
		t.Fatalf("persisted settings = %#v", snapshot.Conversation.Settings)
	}
}

func TestServiceResumePreservesExplicitProviderDefaultTuning(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	existing, err := st.CreateConversation(context.Background(), "conversation-provider-defaults",
		domain.ConversationScopeSession, testProject, testSession, time.Now())
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.SetConversationSettings(context.Background(), existing.ID, domain.ConversationSettings{}, time.Now()); err != nil {
		t.Fatalf("SetConversationSettings: %v", err)
	}

	var resumed ports.ChatResumeConfig
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation(), resumeCfg: &resumed}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "conversation-provider-defaults" },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	_, err = svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1",
		Effort: "high",
	})
	if err != nil {
		t.Fatalf("Start resume: %v", err)
	}
	if resumed.Effort != "" {
		t.Fatalf("resume effort = %q, want persisted provider default", resumed.Effort)
	}
}

func TestServicePersistsAndPassesInitialModelTuningBeforeProviderStart(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	conv := newFakeConversation()
	var started ports.ChatStartConfig
	driver := fakeDriver{start: func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
		started = cfg
		snapshot, err := st.LoadConversationSnapshot(context.Background(), "conversation-start")
		if err != nil {
			return nil, err
		}
		settings := snapshot.Conversation.Settings
		if settings.Model != "gpt-test" || settings.ReasoningEffort != "high" {
			return nil, fmt.Errorf("settings were not durable before provider start: %#v", settings)
		}
		return conv, nil
	}}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, Drivers: fakeRegistry{driver: driver},
		Log: slog.New(slog.DiscardHandler), NewID: func() string { return "conversation-start" },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	_, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), Model: "gpt-test", Effort: "high",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if started.Model != "gpt-test" || started.Effort != "high" {
		t.Fatalf("provider start config = %#v", started)
	}
}

func TestReviewerModelSettingsStayWithReviewOwner(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	worker, err := st.CreateConversation(ctx, "worker-conversation", domain.ConversationScopeProject, testProject, testSession, now)
	if err != nil {
		t.Fatal(err)
	}
	workerSettings := domain.ConversationSettings{Model: "worker-model", ApprovalMode: domain.PermissionModeAuto}
	if err := st.SetConversationSettings(ctx, worker.ID, workerSettings, now); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertReview(ctx, domain.Review{ID: "review-models", SessionID: testSession, ProjectID: testProject, Harness: domain.ReviewerCodex, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	provider := reviewerModelConversation{newFakeConversation()}
	var synced bool
	var ids atomic.Uint64
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, Log: slog.New(slog.DiscardHandler), NewID: func() string { return fmt.Sprintf("review-model-%d", ids.Add(1)) }, OnModelChanged: func(domain.SessionID, string) { synced = true }})
	owner := domain.ReviewConversationOwner("review-models")
	t.Cleanup(func() { _ = svc.StopForOwner(context.Background(), owner) })
	if _, err := svc.Start(ctx, chatsvc.StartConfig{Owner: owner, SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeAuto}); err != nil {
		t.Fatal(err)
	}
	models, settings, err := svc.ModelsForOwner(ctx, owner)
	if err != nil || len(models) != 1 || models[0].ID != "review-model" {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	settings.Model = "review-model"
	settings.ReasoningEffort = "high"
	if _, err := svc.SetTurnSettingsForOwner(ctx, owner, settings); err != nil {
		t.Fatal(err)
	}
	reviewConversation, err := st.ConversationForReview(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reviewConversation.Settings.Model != "review-model" || reviewConversation.Settings.ReasoningEffort != "high" {
		t.Fatalf("review settings=%+v", reviewConversation.Settings)
	}
	workerAfter, err := st.LoadConversationSnapshot(ctx, worker.ID)
	if err != nil || workerAfter.Conversation.Settings != workerSettings || synced {
		t.Fatalf("worker settings=%+v synced=%v err=%v", workerAfter.Conversation.Settings, synced, err)
	}
	if _, err := svc.SendForOwner(ctx, owner, ports.ChatUserMessage{Text: "verify reviewer settings"}); err != nil {
		t.Fatal(err)
	}
	sent := provider.sentMessages()
	if len(sent) != 1 || sent[0].Settings.Model != "review-model" || sent[0].Settings.Effort != "high" {
		t.Fatalf("provider turn settings = %+v", sent)
	}
	settings.ApprovalMode = domain.PermissionModeBypassPermissions
	if _, err := svc.SetTurnSettingsForOwner(ctx, owner, settings); !errors.Is(err, chatsvc.ErrReviewerPermissionsFixed) {
		t.Fatalf("changed fixed reviewer permissions: %v", err)
	}
}

func TestReviewerChatUsesItsOwnProviderHost(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	now := time.Now().UTC()
	if err := st.UpsertReview(context.Background(), domain.Review{
		ID: "review-1", SessionID: testSession, ProjectID: testProject,
		Harness: domain.ReviewerCodex, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertReview: %v", err)
	}

	provider := newFakeConversation()
	var closed atomic.Bool
	provider.onClose = func() { closed.Store(true) }
	var started ports.ChatStartConfig
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider, startCfg: &started}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "review-conversation" },
	})
	owner := domain.ReviewConversationOwner("review-1")
	t.Cleanup(func() { _ = svc.StopForOwner(context.Background(), owner) })

	_, err := svc.Start(context.Background(), chatsvc.StartConfig{
		Owner: owner, SessionID: testSession, ProjectID: testProject,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start reviewer chat: %v", err)
	}
	if started.SessionID != "review-review-1" {
		t.Fatalf("reviewer provider host = %q, want review-review-1", started.SessionID)
	}
	if !started.ReadOnly {
		t.Fatal("reviewer provider was not launched read-only")
	}
	svc.StopAll(context.Background())
	if !closed.Load() {
		t.Fatal("StopAll did not close the review-owned controller")
	}
}

func TestPendingProviderHandoffFreshStartUsesReservedProviderScope(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	record, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	record.Metadata.ProviderConversationID = "source-provider-thread"
	if err := st.UpdateSession(context.Background(), record); err != nil {
		t.Fatalf("seed source provider handle: %v", err)
	}
	if _, err := st.CreateConversation(context.Background(), "switch-fresh-conversation",
		domain.ConversationScopeSession, testProject, testSession, now); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	var started ports.ChatStartConfig
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation(), startCfg: &started}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "unused-switch-fresh-conversation" },
		Now:     func() time.Time { return now.Add(time.Minute) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	_, err = svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ProviderScopeID: "switch-fresh:provider",
	})
	if err != nil {
		t.Fatalf("Start pending fresh target: %v", err)
	}
	if started.ProviderScopeID != "switch-fresh:provider" {
		t.Fatalf("fresh target provider scope = %q, want reserved switch boundary", started.ProviderScopeID)
	}
}

func TestPendingProviderHandoffResumeUsesReservedProviderScope(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	now := time.Date(2026, 8, 21, 11, 0, 0, 0, time.UTC)
	record, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	record.Metadata.ProviderConversationID = "source-provider-thread"
	if err := st.UpdateSession(context.Background(), record); err != nil {
		t.Fatalf("seed source provider handle: %v", err)
	}
	if _, err := st.CreateConversation(context.Background(), "switch-resume-conversation",
		domain.ConversationScopeSession, testProject, testSession, now); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	resumedConversation := newFakeConversation()
	resumedConversation.providerConversationID = "target-provider-thread"
	var resumed ports.ChatResumeConfig
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: resumedConversation, resumeCfg: &resumed}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "unused-switch-resume-conversation" },
		Now:     func() time.Time { return now.Add(time.Minute) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	_, err = svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ProviderConversationID: "target-provider-thread",
		ProviderScopeID:        "switch-resume:provider",
		HistoryMode:            ports.ChatHistoryDeferred,
	})
	if err != nil {
		t.Fatalf("Start pending resumed target: %v", err)
	}
	if resumed.ProviderConversationID != "target-provider-thread" ||
		resumed.ProviderScopeID != "switch-resume:provider" {
		t.Fatalf("resumed target config = %#v, want target handle in reserved switch scope", resumed)
	}
}

func TestFreshProjectStartPersistsNewProviderScopeForSubsequentResume(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 21, 13, 0, 0, 0, time.UTC)
	before, sourceBranch := seedProjectConversationWithProviderHistory(
		t, st, "fresh-project-conversation", now)
	targetSession, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("Create target session: %v", err)
	}

	freshConversation := newFakeConversation()
	freshConversation.providerConversationID = "fresh-provider-thread"
	var started ports.ChatStartConfig
	nextID := 0
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: freshConversation, startCfg: &started}},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			nextID++
			return fmt.Sprintf("fresh-project-%d", nextID)
		},
		Now: func() time.Time { return now.Add(2 * time.Second) },
	})
	lifecycleManager := lifecycle.New(st, nil)

	controller, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: targetSession.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			if result.ProviderBoundary == nil {
				return chatsvc.ControllerCommit{}, errors.New("fresh provider boundary was not reserved")
			}
			if err := lifecycleManager.MarkChatSpawned(ctx, targetSession.ID, domain.SessionMetadata{
				ProviderConversationID: result.ProviderConversationID,
				ControllerGeneration:   result.ControllerGeneration,
			}, *result.ProviderBoundary); err != nil {
				return chatsvc.ControllerCommit{}, err
			}
			committed := result.Conversation
			committed.ActiveBranchID = result.ProviderBoundary.ID
			committed.UpdatedAt = result.ProviderBoundary.CreatedAt
			return chatsvc.ControllerCommit{Conversation: committed}, nil
		},
	})
	if err != nil {
		t.Fatalf("Start fresh project provider: %v", err)
	}
	if started.ProviderScopeID == "" || started.ProviderScopeID == sourceBranch.ProviderScopeID {
		t.Fatalf("fresh provider scope = %q, want new scope distinct from source %q",
			started.ProviderScopeID, sourceBranch.ProviderScopeID)
	}
	after, err := st.ConversationForSession(ctx, targetSession.ID)
	if err != nil {
		t.Fatalf("ConversationForSession after fresh start: %v", err)
	}
	if after.ActiveBranchID == before.ActiveBranchID || after.ActiveBranchID != started.ProviderScopeID {
		t.Fatalf("active branch = %q, want reserved provider scope %q (source %q)",
			after.ActiveBranchID, started.ProviderScopeID, before.ActiveBranchID)
	}
	freshBranch, err := st.ConversationBranch(ctx, after.ID, after.ActiveBranchID)
	if err != nil {
		t.Fatalf("fresh ConversationBranch: %v", err)
	}
	if freshBranch.ProviderScopeID != started.ProviderScopeID ||
		freshBranch.ProviderConversationID != "fresh-provider-thread" ||
		freshBranch.ParentBranchID != before.ActiveBranchID ||
		freshBranch.SessionID != targetSession.ID {
		t.Fatalf("fresh provider boundary = %+v", freshBranch)
	}
	if controller.ProviderConversationID() != "fresh-provider-thread" {
		t.Fatalf("fresh controller provider handle = %q", controller.ProviderConversationID())
	}
	if err := svc.Stop(ctx, targetSession.ID); err != nil {
		t.Fatalf("Stop fresh controller: %v", err)
	}

	resumedConversation := newFakeConversation()
	resumedConversation.providerConversationID = "fresh-provider-thread"
	var resumed ports.ChatResumeConfig
	restarted := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: resumedConversation, resumeCfg: &resumed}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "fresh-project-resume-generation" },
		Now:     func() time.Time { return now.Add(3 * time.Second) },
	})
	t.Cleanup(func() { _ = restarted.Stop(context.Background(), targetSession.ID) })
	if _, err := restarted.Start(ctx, chatsvc.StartConfig{
		SessionID: targetSession.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ProviderConversationID: "fresh-provider-thread",
		HistoryMode:            ports.ChatHistoryDeferred,
	}); err != nil {
		t.Fatalf("Resume fresh project provider: %v", err)
	}
	if resumed.ProviderScopeID != freshBranch.ProviderScopeID {
		t.Fatalf("resumed provider scope = %q, want persisted %q",
			resumed.ProviderScopeID, freshBranch.ProviderScopeID)
	}
}

func TestFreshProjectProviderStartFailurePreservesSourceHeadAndOwner(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 21, 14, 0, 0, 0, time.UTC)
	before, sourceBranch := seedProjectConversationWithProviderHistory(
		t, st, "failed-fresh-project-conversation", now)
	providerErr := errors.New("provider unavailable")
	var attemptedScope string
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{
			start: func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
				attemptedScope = cfg.ProviderScopeID
				return nil, providerErr
			},
		}},
		Log:   slog.New(slog.DiscardHandler),
		NewID: func() string { return "failed-fresh-project-boundary" },
		Now:   func() time.Time { return now.Add(2 * time.Second) },
	})

	_, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
	})
	if !errors.Is(err, providerErr) {
		t.Fatalf("Start error = %v, want provider failure", err)
	}
	if attemptedScope == "" || attemptedScope == sourceBranch.ProviderScopeID {
		t.Fatalf("attempted provider scope = %q, want reserved fresh scope", attemptedScope)
	}
	after, err := st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession after failure: %v", err)
	}
	if after.ActiveBranchID != before.ActiveBranchID {
		t.Fatalf("active branch after provider failure = %q, want source %q",
			after.ActiveBranchID, before.ActiveBranchID)
	}
	record, found, err := st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession after failure: found=%v err=%v", found, err)
	}
	if record.Metadata.ProviderConversationID != "source-provider-thread" ||
		record.Metadata.ControllerGeneration != "source-generation" {
		t.Fatalf("provider owner after failure = handle %q generation %q",
			record.Metadata.ProviderConversationID, record.Metadata.ControllerGeneration)
	}
}
