package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// TestAgentAccountUsageFollowsBindingHistory pins the per-account statistics:
// each usage event counts for the account the session was on when it happened,
// so a session moved from one account to another splits its usage between them.
func TestAgentAccountUsageFollowsBindingHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	session := seedUsageSession(t, s, domain.HarnessClaudeCode)
	source := seedUsageSource(t, s, session, t0)
	mustNoError(t, s.CreateAgentAccount(ctx, domain.AgentAccount{
		ID: "acct-b", Provider: domain.AgentAccountProviderClaude, Home: "/tmp/acct-b", Email: "b@example.com", CreatedAt: t0,
	}))
	mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, domain.AgentAccountProviderClaude, "device-claude", t0))
	mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, domain.AgentAccountProviderClaude, "acct-b", t0.Add(24*time.Hour)))

	before := anthropicUsageEvent("before-move", 10, 0, 0, 5)
	before.CreatedAt = t0.Add(time.Hour)
	after := anthropicUsageEvent("after-move", 20, 0, 0, 7)
	after.CreatedAt = t0.Add(25 * time.Hour)
	mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{
		ByteOffset: 100, State: domain.UsageSourceActive, ParserStateJSON: `{}`, UpdatedAt: t0,
	}, []domain.ModelUsageEvent{before, after}))

	rows, err := s.AgentAccountUsageSince(ctx, t0.Add(-time.Hour))
	mustNoError(t, err)
	got := map[string]domain.AgentAccountUsage{}
	for _, row := range rows {
		got[row.AccountID] = row
	}
	if len(rows) != 2 || got["device-claude"].InputTokens != 10 || got["device-claude"].Day != "2026-10-14" ||
		got["acct-b"].InputTokens != 20 || got["acct-b"].OutputTokens != 7 || got["acct-b"].Day != "2026-10-15" {
		t.Fatalf("usage rows = %+v", rows)
	}

	current, bound, err := s.GetSessionAgentAccount(ctx, session.ID, domain.AgentAccountProviderClaude)
	mustNoError(t, err)
	if !bound || current != "acct-b" {
		t.Fatalf("current binding = %q bound=%v, want acct-b", current, bound)
	}

	// Removing the account keeps the session's history attributable and drops
	// any default that named it.
	mustNoError(t, s.SetAgentAccountDefault(ctx, domain.AgentAccountProviderClaude, "acct-b"))
	mustNoError(t, s.DeleteAgentAccount(ctx, "acct-b"))
	if _, ok, err := s.GetAgentAccountDefault(ctx, domain.AgentAccountProviderClaude); err != nil || ok {
		t.Fatalf("default after delete: ok=%v err=%v", ok, err)
	}
}

// A controller handoff must change the mode or the account: an account move is
// a same-mode transition that names both accounts.
func TestInterfaceTransitionsAllowSameModeOnlyForAnAccountMove(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	session := seedUsageSession(t, s, domain.HarnessClaudeCode)
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	move := domain.SessionInterfaceTransition{
		ID: "same-mode", SessionID: session.ID, SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeTUI,
		Policy: domain.SessionInterfaceTransitionDrain, HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict,
		Phase: domain.SessionInterfaceTransitionRequested, CreatedAt: now, UpdatedAt: now,
	}
	if _, _, err := s.CreateSessionInterfaceTransition(ctx, move); err == nil {
		t.Fatal("a same-mode transition without an account change was accepted")
	}
	move.ID, move.SourceAccountID, move.TargetAccountID = "account-move", "device-claude", "acct-b"
	created, ok, err := s.CreateSessionInterfaceTransition(ctx, move)
	mustNoError(t, err)
	if !ok || created.SourceAccountID != "device-claude" || created.TargetAccountID != "acct-b" || !created.AccountMove() {
		t.Fatalf("created = %+v", created)
	}
	active, found, err := s.GetActiveSessionInterfaceTransition(ctx, session.ID)
	mustNoError(t, err)
	if !found || active.TargetAccountID != "acct-b" {
		t.Fatalf("active = %+v", active)
	}
}

// TestAgentAccountKeepsOrganizationAndLabel pins that the organization a login
// belongs to and the owner's label survive a reload, and that naming an account
// that is not saved is refused.
func TestAgentAccountKeepsOrganizationAndLabel(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustNoError(t, s.CreateAgentAccount(ctx, domain.AgentAccount{
		ID: "acct-t", Provider: domain.AgentAccountProviderClaude, Home: "/tmp/acct-t", Email: "t@example.com",
		OrgID: "org-1", OrgName: "Acme", CreatedAt: time.Now(),
	}))
	mustNoError(t, s.UpdateAgentAccountOrg(ctx, "acct-t", "org-2", "Beta"))
	mustNoError(t, s.SetAgentAccountLabel(ctx, "acct-t", "Work"))
	accounts, err := s.ListAgentAccounts(ctx)
	mustNoError(t, err)
	if len(accounts) != 1 || accounts[0].OrgID != "org-2" || accounts[0].OrgName != "Beta" || accounts[0].Label != "Work" {
		t.Fatalf("accounts = %+v", accounts)
	}
	if err := s.SetAgentAccountLabel(ctx, "missing", "x"); err == nil {
		t.Fatal("labelling an unsaved account succeeded")
	}
}

// A Codex account keeps the file-registry descriptor fields through the store.
func TestAgentAccountKeepsCodexRegistryFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	verified := created.Add(time.Hour)
	mustNoError(t, s.CreateAgentAccount(ctx, domain.AgentAccount{
		ID: "9b2f6c1e-3f7a-4c52-9a1d-0e8f5b7c1a23", Provider: domain.AgentAccountProviderCodex, Home: "/tmp/codex-a", CreatedAt: created,
		Source: "managed", AuthMethod: "chatgpt", ProviderAccountID: "provider-1", VerifiedAt: &verified,
	}))
	mustNoError(t, s.CreateAgentAccount(ctx, domain.AgentAccount{
		ID: "acct-claude", Provider: domain.AgentAccountProviderClaude, Home: "/tmp/claude-a", CreatedAt: created,
	}))
	accounts, err := s.ListAgentAccounts(ctx)
	mustNoError(t, err)
	byID := map[string]domain.AgentAccount{}
	for _, a := range accounts {
		byID[a.ID] = a
	}
	codex := byID["9b2f6c1e-3f7a-4c52-9a1d-0e8f5b7c1a23"]
	if codex.Source != "managed" || codex.AuthMethod != "chatgpt" || codex.ProviderAccountID != "provider-1" || codex.VerifiedAt == nil || !codex.VerifiedAt.Equal(verified) {
		t.Fatalf("codex account lost registry fields: %+v", codex)
	}
	if claude := byID["acct-claude"]; claude.Source != "" || claude.VerifiedAt != nil {
		t.Fatalf("claude account gained registry fields: %+v", claude)
	}
}

func TestUpdateAgentAccountCodexDescriptorOnlyTouchesCodexAccounts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	mustNoError(t, s.CreateAgentAccount(ctx, domain.AgentAccount{ID: "codex-1", Provider: domain.AgentAccountProviderCodex, Home: "/tmp/c1", CreatedAt: created, Source: "managed"}))
	mustNoError(t, s.CreateAgentAccount(ctx, domain.AgentAccount{ID: "claude-1", Provider: domain.AgentAccountProviderClaude, Home: "/tmp/cl1", CreatedAt: created}))
	verified := created.Add(time.Hour)
	mustNoError(t, s.UpdateAgentAccountCodexDescriptor(ctx, "codex-1", "p@example.com", "apiKey", "prov-9", verified))
	if err := s.UpdateAgentAccountCodexDescriptor(ctx, "claude-1", "x@example.com", "", "", verified); err == nil {
		t.Fatal("updating a Claude account through the Codex descriptor query succeeded")
	}
	if err := s.UpdateAgentAccountCodexDescriptor(ctx, "nope", "", "", "", verified); err == nil {
		t.Fatal("updating an unknown account succeeded")
	}
	accounts, err := s.ListAgentAccounts(ctx)
	mustNoError(t, err)
	for _, a := range accounts {
		switch a.ID {
		case "codex-1":
			if a.Email != "p@example.com" || a.AuthMethod != "apiKey" || a.ProviderAccountID != "prov-9" || a.VerifiedAt == nil || !a.VerifiedAt.Equal(verified) {
				t.Fatalf("codex account = %+v", a)
			}
		case "claude-1":
			if a.Email != "" || a.VerifiedAt != nil {
				t.Fatalf("claude account changed: %+v", a)
			}
		}
	}
}
