package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type notReadyStub struct {
	readyStub
	notInstalled bool
	unauthorized bool
	probeErr     error
}

func (s notReadyStub) EnsureAgentReadiness(_ context.Context, id string, _ domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error) {
	if s.probeErr != nil {
		return domain.AgentReadinessSnapshot{}, s.probeErr
	}
	snap := domain.AgentReadinessSnapshot{ID: id, EffectiveReadiness: domain.AgentReadinessNotReady}
	if s.unauthorized {
		snap.Installation.State = domain.AgentInstallationInstalled
		snap.Authentication.State = domain.AgentAuthenticationUnauthorized
	}
	if s.notInstalled {
		snap.Installation.State = domain.AgentInstallationNotInstalled
	}
	return snap, nil
}

// A project-configured agent that is not ready fails fast with the fix named;
// an explicit --agent is not subject to this check.
func TestSpawnConfiguredAgentNotReadyFailsClearly(t *testing.T) {
	t.Parallel()
	launcher := &recordingLauncher{}
	mgr, store, _ := newChatManager(launcher)
	project := store.projects[string(chatTestProject)]
	project.Config.Worker.Harness = domain.HarnessClaudeCode
	store.projects[string(chatTestProject)] = project
	mgr.SetAgentReadiness(notReadyStub{readyStub: readyStub{t: t, ready: map[string]bool{"codex": true}}, notInstalled: true})

	_, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{ProjectID: chatTestProject, Kind: domain.KindWorker})
	if !errors.Is(err, ErrAgentNotReady) {
		t.Fatalf("err = %v, want ErrAgentNotReady", err)
	}
	for _, want := range []string{"claude-code", "is not installed", "Ready agents: codex", "--agent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestSpawnAgentReadinessDoesNotBlock(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		stub     notReadyStub
		explicit domain.AgentHarness
	}{
		"explicit agent": {stub: notReadyStub{notInstalled: true}, explicit: domain.HarnessClaudeCode},
		"unauthorized":   {stub: notReadyStub{unauthorized: true}},
		"probe error":    {stub: notReadyStub{probeErr: errors.New("probe failed")}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			mgr, store, _ := newChatManager(&recordingLauncher{})
			project := store.projects[string(chatTestProject)]
			project.Config.Worker.Harness = domain.HarnessClaudeCode
			store.projects[string(chatTestProject)] = project
			tc.stub.readyStub = readyStub{t: t}
			mgr.SetAgentReadiness(tc.stub)
			_, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: tc.explicit})
			if errors.Is(err, ErrAgentNotReady) {
				t.Fatalf("blocked: %v", err)
			}
		})
	}
}
