package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	claudeagent "tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// B678 (web-4): a handoff successor's first prompt never reached Claude, so
// the provider id it was given was never saved. Its project conversation holds
// the previous orchestrator's turns and the session recorded the brief, so no
// proof says the id was never used, and every resume got ACP -32002. An
// orchestrator in that state goes on in a fresh conversation, seeded with a
// note, and the resume succeeds; a worker, or any other failure, still refuses.
func TestResumeOrchestratorWhoseSavedConversationIsGone(t *testing.T) {
	const lost = "20f3c167-93f7-46ce-8d95-9ce14c1fbffe"
	const brief = "Read handoff.md and continue. You replaced orchestrator web-1."
	missing := fmt.Errorf("%w: %w: ACP session/load: Resource not found", ports.ErrChatResumeFailed, ports.ErrChatConversationMissing)
	otherFailure := fmt.Errorf("%w: ACP session/load: transcript replay failed", ports.ErrChatResumeFailed)
	for _, tc := range []struct {
		name     string
		kind     domain.SessionKind
		scope    domain.ConversationScope
		startErr error
		// transcript leaves Claude's file on disk: the -32002 came from the
		// environment, not from a lost conversation.
		transcript bool
		// accountErr leaves the session's account environment incomplete: the
		// failed launch and the probe would look in the same wrong config dir.
		accountErr bool
		wantFresh  bool
	}{
		{"orchestrator whose conversation is gone", domain.KindOrchestrator, domain.ConversationScopeProject, missing, false, false, true},
		{"worker whose conversation is gone", domain.KindWorker, domain.ConversationScopeSession, missing, false, false, false},
		{"orchestrator with another resume failure", domain.KindOrchestrator, domain.ConversationScopeProject, otherFailure, false, false, false},
		{"orchestrator whose transcript is still on disk", domain.KindOrchestrator, domain.ConversationScopeProject, missing, true, false, false},
		{"orchestrator whose account environment is incomplete", domain.KindOrchestrator, domain.ConversationScopeProject, missing, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configDir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", configDir)
			if tc.transcript {
				dir := filepath.Join(configDir, "projects", "workspace")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, lost+".jsonl"), []byte(`{"type":"user"}`+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			store := sqlitetest.MustOpenAt(t, t.TempDir())
			if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "proj", Path: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			created, err := store.CreateSession(ctx, domain.SessionRecord{
				ProjectID: "proj", Kind: tc.kind, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
				Metadata: domain.SessionMetadata{
					WorkspacePath: t.TempDir(), Branch: "tp/session", Prompt: brief,
					LatestUserPrompt: brief, LatestUserPromptAt: now,
					ProviderConversationID: lost, ControllerGeneration: "chat-generation",
				},
				Activity:      domain.Activity{State: domain.ActivityExited, LastActivityAt: now},
				FirstSignalAt: now, CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			conversation, err := store.CreateConversation(ctx, "conversation-1", tc.scope, "proj", created.ID, now)
			if err != nil {
				t.Fatal(err)
			}
			// Recorded history: B673's never-persisted proof does not hold.
			if err := store.AdoptProviderTurn(ctx, conversation.ID, created.ID, "chat-generation",
				"turn-1", "provider-turn-1", now); err != nil {
				t.Fatal(err)
			}
			launcher := &recordingLauncher{
				startErr: tc.startErr,
				providerBoundary: &domain.ConversationBranch{
					ID: "boundary-1", ConversationID: conversation.ID, SessionID: created.ID,
					ProviderConversationID: "thread-1", ParentBranchID: conversation.ActiveBranchID,
					ProviderScopeID: "boundary-1", ProviderIDsScoped: true, CreatedAt: now,
				},
			}
			// The agent loses the conversation, so only the first start fails.
			launcher.beforeStart = func(ChatStart) {
				if len(launcher.started) > 1 {
					launcher.startErr = nil
				}
			}
			manager := New(Deps{
				Runtime: &fakeRuntime{}, Agents: singleAgent{agent: claudeagent.New()}, Workspace: &fakeWorkspace{},
				Store: store, Messenger: &fakeMessenger{}, Chat: launcher,
				Lifecycle: &sqliteTransitionLifecycle{store: store},
				LookPath:  func(string) (string, error) { return "/bin/true", nil },
				DataDir:   t.TempDir(),
			})
			if tc.accountErr {
				manager.SetAgentAccounts(unresolvedAccounts{})
			}

			_, err = manager.ResumeAgentWithMode(ctx, created.ID)

			if first := launcher.started[0]; first.ProviderConversationID != lost || first.FreshIfProviderConversationMissing {
				t.Fatalf("first start = id %q fresh-if-missing %v, want a plain resume of %q",
					first.ProviderConversationID, first.FreshIfProviderConversationMissing, lost)
			}
			if !tc.wantFresh {
				if len(launcher.started) != 1 || !errors.Is(err, ports.ErrChatResumeFailed) {
					t.Fatalf("starts = %d err = %v, want one refused resume", len(launcher.started), err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResumeAgentWithMode: %v", err)
			}
			if len(launcher.started) != 2 {
				t.Fatalf("started %d controllers, want the failed resume and one fresh start", len(launcher.started))
			}
			fresh := launcher.started[1]
			if fresh.ProviderConversationID != "" || fresh.FreshIfProviderConversationMissing {
				t.Fatalf("fresh start = id %q fresh-if-missing %v, want a new conversation",
					fresh.ProviderConversationID, fresh.FreshIfProviderConversationMissing)
			}
			for _, want := range []string{"tallpine session history " + string(created.ID), "tallpine status", brief, string(created.ID)} {
				if !strings.Contains(fresh.ContextResetNote, want) {
					t.Fatalf("seed note lacks %q:\n%s", want, fresh.ContextResetNote)
				}
			}
			current, _, err := store.GetSession(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Metadata.ProviderConversationID != "thread-1" {
				t.Fatalf("session provider = %q, want the fresh thread-1", current.Metadata.ProviderConversationID)
			}
		})
	}
}

// unresolvedAccounts fails to resolve the session's account login, as a
// timed-out or broken account lookup does.
type unresolvedAccounts struct{ *transitionAccounts }

func (unresolvedAccounts) SessionLaunchEnv(context.Context, domain.SessionID) (map[string]string, error) {
	return nil, errors.New("account lookup timed out")
}
