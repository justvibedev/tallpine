package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// A replacement orchestrator can run a different harness than its predecessor.
// Tool-server failures and thread state reported by the old provider (a Codex plugin needing
// login) must not stay on the conversation, because the new provider never
// reports them and the chat would show the banner forever.
func TestProjectConversationRebindClearsPreviousOwnersToolServers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "project-mcp")
	newOrchestrator := func() domain.SessionRecord {
		record := sampleRecord("project-mcp")
		record.Kind = domain.KindOrchestrator
		record.Mode = domain.SessionModeChat
		created, err := s.CreateSession(ctx, record)
		if err != nil {
			t.Fatalf("create orchestrator: %v", err)
		}
		return created
	}
	first, second := newOrchestrator(), newOrchestrator()
	conversation, err := s.CreateConversation(ctx, "project-conversation", domain.ConversationScopeProject,
		"project-mcp", first.ID, histClock)
	if err != nil {
		t.Fatalf("create project conversation: %v", err)
	}
	failed := []domain.ConversationMCPServer{{Name: "RevenueCat", Status: "failed", FailureReason: "reauthenticationRequired"}}
	if err := s.RecordMCPServers(ctx, conversation.ID, failed); err != nil {
		t.Fatalf("record mcp servers: %v", err)
	}
	closed := domain.ConversationThreadState{Status: domain.ThreadStatusClosed}
	if err := s.RecordThreadState(ctx, conversation.ID, closed); err != nil {
		t.Fatalf("record thread state: %v", err)
	}
	// Re-binding to the same owner keeps the state its live controller reported.
	if _, err := s.CreateConversation(ctx, "x", domain.ConversationScopeProject, "project-mcp", first.ID, histClock.Add(time.Second)); err != nil {
		t.Fatalf("same-owner rebind: %v", err)
	}
	same, err := s.ConversationForSession(ctx, first.ID)
	if err != nil || len(same.MCPServers) != 1 {
		t.Fatalf("same owner servers = %+v, %v; want kept", same.MCPServers, err)
	}
	returned, err := s.CreateConversation(ctx, "x", domain.ConversationScopeProject, "project-mcp", second.ID, histClock.Add(time.Minute))
	if err != nil {
		t.Fatalf("replacement rebind: %v", err)
	}
	// The returned record seeds the new controller; stale values there are written back.
	if returned.ThreadState != nil || len(returned.MCPServers) != 0 {
		t.Fatalf("rebind returned stale state: thread=%+v servers=%+v", returned.ThreadState, returned.MCPServers)
	}
	got, err := s.ConversationForSession(ctx, second.ID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ThreadState != nil {
		t.Fatalf("replacement owner inherited thread state %+v, want none", got.ThreadState)
	}
	if len(got.MCPServers) != 0 {
		t.Fatalf("replacement owner inherited tool servers %+v, want none", got.MCPServers)
	}
}

// An in-place provider switch (Codex to Claude) keeps the session and the
// conversation, so the source provider's reports must be dropped when the
// switch commits, and only then: a failed switch leaves the source state.
func TestCommitProviderSwitchClearsSourceToolServersAndThreadState(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	failed := []domain.ConversationMCPServer{{Name: "RevenueCat", Status: "failed", FailureReason: "reauthenticationRequired"}}
	if err := s.RecordMCPServers(ctx, conversation.ID, failed); err != nil {
		t.Fatalf("record mcp servers: %v", err)
	}
	if err := s.RecordThreadState(ctx, conversation.ID, domain.ConversationThreadState{Status: domain.ThreadStatusClosed}); err != nil {
		t.Fatalf("record thread state: %v", err)
	}
	branch := domain.ConversationBranch{
		ID: "branch-switched", ConversationID: conversation.ID, SessionID: session.ID,
		ProviderConversationID: "thread-switched", ParentBranchID: conversation.ActiveBranchID,
	}
	rec := session
	rec.UpdatedAt = testNow.Add(time.Minute)
	activity := domain.ConversationActivity{
		ID: "activity-switch", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
		Summary: "switched", ProviderItemID: "switch-1",
	}
	if err := s.CommitProviderSwitch(ctx, rec, branch, "generation-switched", domain.ConversationSettings{}, activity); err != nil {
		t.Fatalf("CommitProviderSwitch: %v", err)
	}
	got, err := s.ConversationForSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ThreadState != nil || len(got.MCPServers) != 0 {
		t.Fatalf("switch kept source state: thread=%+v servers=%+v", got.ThreadState, got.MCPServers)
	}
}

// A terminal orchestrator converting to Chat adopts the project conversation
// from its retired predecessor, which may have run another harness. The commit
// drops the predecessor's reports; a same-session conversion would keep them.
func TestCommitChatSpawnCrossSessionHandoffClearsPredecessorState(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "handoff-mcp")
	orchestrator := func(generation, thread string, createdAt time.Time) domain.SessionRecord {
		record := sampleRecord("handoff-mcp")
		record.Kind = domain.KindOrchestrator
		record.Mode = domain.SessionModeChat
		record.CreatedAt = createdAt
		record.Metadata.ControllerGeneration = generation
		record.Metadata.ProviderConversationID = thread
		created, err := s.CreateSession(ctx, record)
		if err != nil {
			t.Fatalf("create orchestrator: %v", err)
		}
		return created
	}
	source := orchestrator("generation-source", "thread-source", histClock)
	conversation, err := s.CreateConversation(ctx, "project-conversation", domain.ConversationScopeProject,
		"handoff-mcp", source.ID, histClock)
	if err != nil {
		t.Fatalf("create project conversation: %v", err)
	}
	if err := s.RecordMCPServers(ctx, conversation.ID, []domain.ConversationMCPServer{{Name: "RevenueCat", Status: "failed"}}); err != nil {
		t.Fatalf("record mcp servers: %v", err)
	}
	if err := s.RecordThreadState(ctx, conversation.ID, domain.ConversationThreadState{Status: domain.ThreadStatusClosed}); err != nil {
		t.Fatalf("record thread state: %v", err)
	}
	source.IsTerminated = true
	if err := s.UpdateSession(ctx, source); err != nil {
		t.Fatalf("retire source: %v", err)
	}
	target := orchestrator("generation-target", "thread-target", histClock.Add(time.Hour))
	branch := domain.ConversationBranch{
		ID: "branch-handoff", ConversationID: conversation.ID, SessionID: target.ID,
		ProviderConversationID: "thread-target", ProviderScopeID: "branch-handoff",
		ParentBranchID: conversation.ActiveBranchID, CreatedAt: histClock.Add(time.Hour),
	}
	handoff := &domain.ChatProviderHandoff{
		BoundaryID: branch.ID, ConversationID: conversation.ID, PreviousSessionID: source.ID,
		PreviousBranchID: conversation.ActiveBranchID, PreviousSequence: conversation.LatestSequence,
		ExpectedControllerOwner: target.ControllerOwner(),
	}
	prepare := func(context.Context) error { return nil }
	if err := s.CommitChatSpawnPrepared(ctx, target, branch, handoff, prepare); err != nil {
		t.Fatalf("CommitChatSpawnPrepared: %v", err)
	}
	got, err := s.ConversationForSession(ctx, target.ID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != conversation.ID || got.ThreadState != nil || len(got.MCPServers) != 0 {
		t.Fatalf("handoff kept predecessor state: %+v", got)
	}
}
