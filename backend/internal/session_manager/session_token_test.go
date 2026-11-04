package sessionmanager

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	browsersvc "tallpine.eu/tallpine/backend/internal/service/browser"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
)

func TestChatSpawnInjectsScopedSessionTokenNotProjectValue(t *testing.T) {
	t.Parallel()

	launcher := &recordingLauncher{}
	mgr, store, _ := newChatManager(launcher)
	tokens := sessiontoken.New("install-token")
	mgr.sessionTokens = tokens
	mgr.runFilePath = "/home/u/.tallpine/running.json"
	mgr.browserCapabilities = &scriptedBrowserCapabilities{issues: []browserCapabilityIssue{{
		token: "chat-token", verifier: "chat-verifier",
	}}}
	project := store.projects[string(chatTestProject)]
	project.Config.Env = map[string]string{EnvSessionToken: "project-token"}
	store.projects[string(chatTestProject)] = project
	launcher.beforeStart = func(cfg ChatStart) {
		stored := store.sessions[cfg.SessionID]
		if !tokens.VerifySession(cfg.Env[EnvSessionToken], cfg.SessionID, stored.Metadata.BrowserCapabilityVerifier) {
			t.Fatalf("session token %q was not valid when the controller started", cfg.Env[EnvSessionToken])
		}
	}

	rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex, RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := launcher.started[0].Env[EnvSessionToken]; got != tokens.IssueSession(rec.ID, "chat-verifier") {
		t.Fatalf("controller session token = %q", got)
	}
	// The token names the daemon that issued it, so tallpine never sends it to
	// another daemon.
	if got := launcher.started[0].Env[EnvSessionTokenRunFile]; got != "/home/u/.tallpine/running.json" {
		t.Fatalf("%s = %q, want the issuing daemon's run file", EnvSessionTokenRunFile, got)
	}
	// The same pair under names a harness's credential scrub (Codex, DeepSeek
	// Harness drop KEY/TOKEN/SECRET/PASSWORD) leaves alone.
	if got := launcher.started[0].Env[EnvSessionAuth]; got != launcher.started[0].Env[EnvSessionToken] || got == "" {
		t.Fatalf("%s = %q, want the session token", EnvSessionAuth, got)
	}
	if got := launcher.started[0].Env[EnvSessionAuthRunFile]; got != "/home/u/.tallpine/running.json" {
		t.Fatalf("%s = %q, want the issuing daemon's run file", EnvSessionAuthRunFile, got)
	}
	for _, server := range launcher.started[0].MCPServers {
		if _, ok := server.Env[EnvSessionToken]; ok {
			t.Fatalf("tool server env carries a launch credential: %v", server.Env)
		}
	}
}

func TestResumeChatRotatesSessionToken(t *testing.T) {
	t.Parallel()

	launcher := &recordingLauncher{}
	mgr, store, _ := newChatManager(launcher)
	seedChatResumeSession(store, domain.ActivityExited)
	rec := store.sessions["mer-1"]
	authority := browsersvc.NewAuthority()
	_, oldVerifier, err := authority.Issue(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec.Metadata.BrowserCapabilityVerifier = oldVerifier
	store.sessions[rec.ID] = rec
	mgr.browserCapabilities = &recordingBrowserAuthority{authority: authority}
	tokens := sessiontoken.New("install-token")
	mgr.sessionTokens = tokens
	mgr.runFilePath = "/home/u/.tallpine/running.json"
	oldToken := tokens.IssueSession(rec.ID, oldVerifier)

	result, err := mgr.ResumeAgentWithMode(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("ResumeAgentWithMode: %v", err)
	}
	newVerifier := result.Session.Metadata.BrowserCapabilityVerifier
	newToken := launcher.started[0].Env[EnvSessionToken]
	if tokens.VerifySession(oldToken, rec.ID, newVerifier) {
		t.Fatal("the previous launch's session token stayed valid after resume")
	}
	if !tokens.VerifySession(newToken, rec.ID, newVerifier) {
		t.Fatal("the resumed launch's session token is not valid")
	}
}

func TestRuntimeEnvNeverCarriesAProjectSessionToken(t *testing.T) {
	t.Parallel()

	mgr, _, _ := newChatManager(&recordingLauncher{})
	mgr.sessionTokens = sessiontoken.New("install-token")
	env := mgr.runtimeEnv("s-1", chatTestProject, "", map[string]string{EnvSessionToken: "project-token"})
	if got, ok := env[EnvSessionToken]; ok {
		t.Fatalf("base runtime env carries %s=%q; only a launch mints one", EnvSessionToken, got)
	}
}
