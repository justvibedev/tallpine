package chat_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// fencingDriver is a chat driver that carries the state fence on macOS, as
// the Claude ACP driver does.
type fencingDriver struct{ fakeDriver }

func (fencingDriver) FencesState(goos string) bool { return claudecode.FencesState(goos) }

// Under the hardened profile a chat launch is admitted only when its driver can
// fence it on this OS: on preflight and on start alike.
func TestChatFenceAdmissionUnderProfile(t *testing.T) {
	hardened := policytest.Hardened(t)
	for _, tc := range []struct {
		name    string
		driver  ports.ChatDriver
		goos    string
		refused bool
	}{
		{"unfenceable driver darwin", fakeDriver{conv: newFakeConversation()}, "darwin", true},
		{"claude linux", fencingDriver{fakeDriver{conv: newFakeConversation()}}, "linux", true},
		{"claude windows", fencingDriver{fakeDriver{conv: newFakeConversation()}}, "windows", true},
		{"claude darwin", fencingDriver{fakeDriver{conv: newFakeConversation()}}, "darwin", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer chatsvc.SetFenceGOOSForTest(tc.goos)()
			st := openStore(t)
			svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, Drivers: fakeRegistry{driver: tc.driver}, Log: slog.New(slog.DiscardHandler), NewID: func() string { return "fence-" + tc.goos }})
			t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
			err := svc.PreflightChat(context.Background(), domain.HarnessCodex, ports.PermissionModeAuto, hardened)
			if tc.refused != agentpolicy.IsFenceRequired(err) {
				t.Fatalf("preflight err = %v, refused want %v", err, tc.refused)
			}
			_, err = svc.Start(context.Background(), chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
				WorkspacePath: t.TempDir(), Policy: hardened, Permissions: ports.PermissionModeAuto,
			})
			if tc.refused != agentpolicy.IsFenceRequired(err) {
				t.Fatalf("start err = %v, refused want %v", err, tc.refused)
			}
		})
	}
}

// Without the profile no chat launch is refused for lacking a fence.
func TestChatFenceAdmissionWithoutProfile(t *testing.T) {
	defer chatsvc.SetFenceGOOSForTest("linux")()
	svc := chatsvc.New(chatsvc.Options{Drivers: fakeRegistry{driver: fakeDriver{}}})
	on := policytest.Decision(t, map[string]map[string]any{"protectState": {"mode": "on"}})
	if err := svc.PreflightChat(context.Background(), domain.HarnessCodex, ports.PermissionModeAuto, on); agentpolicy.IsFenceRequired(err) {
		t.Fatalf("no profile refused: %v", err)
	}
}

// modeFencingDriver fences only the listed modes, like Codex, which fences
// only launches that run no Codex sandbox.
type modeFencingDriver struct {
	fakeDriver
	fences ports.PermissionMode
}

func (d modeFencingDriver) FencesLaunch(goos string, mode ports.PermissionMode) bool {
	return goos == "darwin" && mode == d.fences
}

// Admission asks the driver about the mode the chat launches in after the
// permission cap: hardened caps bypass to auto before the question.
func TestChatFenceAdmissionAsksAboutPostCapMode(t *testing.T) {
	defer chatsvc.SetFenceGOOSForTest("darwin")()
	hardened := policytest.Hardened(t)
	for _, tc := range []struct {
		name    string
		fences  ports.PermissionMode
		mode    ports.PermissionMode
		refused bool
	}{
		{"auto fenced", ports.PermissionModeAuto, ports.PermissionModeAuto, false},
		{"bypass capped to auto", ports.PermissionModeAuto, ports.PermissionModeBypassPermissions, false},
		{"bypass-only driver, capped", ports.PermissionModeBypassPermissions, ports.PermissionModeBypassPermissions, true},
		{"accept-edits not fenced", ports.PermissionModeAuto, ports.PermissionModeAcceptEdits, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := chatsvc.New(chatsvc.Options{Drivers: fakeRegistry{driver: modeFencingDriver{fakeDriver{conv: newFakeConversation()}, tc.fences}}})
			err := svc.PreflightChat(context.Background(), domain.HarnessCodex, tc.mode, hardened)
			if tc.refused != agentpolicy.IsFenceRequired(err) {
				t.Fatalf("err = %v, refused want %v", err, tc.refused)
			}
		})
	}
}

// A resumed chat launches in its stored mode, so admission is re-asked for it:
// the caller's auto is fenceable, the stored accept-edits is not.
func TestChatFenceAdmissionUsesStoredResumeMode(t *testing.T) {
	defer chatsvc.SetFenceGOOSForTest("darwin")()
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	conversation, err := st.CreateConversation(ctx, "stored-mode-conversation", domain.ConversationScopeProject, testProject, testSession, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetConversationSettings(ctx, conversation.ID, domain.ConversationSettings{ApprovalMode: domain.PermissionModeAcceptEdits}, now); err != nil {
		t.Fatal(err)
	}
	conv := newFakeConversation()
	conv.providerConversationID = "thread-stored"
	driver := modeFencingDriver{fakeDriver{conv: conv, caps: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true, ports.ChatCapabilityInterrupt: true, ports.ChatCapabilityResume: true}}, ports.PermissionModeAuto}
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, Drivers: fakeRegistry{driver: driver}, Log: slog.New(slog.DiscardHandler), NewID: func() string { return "stored-mode" }})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	_, err = svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ProviderConversationID: "thread-stored", Policy: policytest.Hardened(t), Permissions: ports.PermissionModeAuto,
	})
	if !agentpolicy.IsFenceRequired(err) {
		t.Fatalf("err = %v, want refusal for the stored accept-edits mode", err)
	}
}
