package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func crossPromptManager(t *testing.T, on bool) *Manager {
	t.Helper()
	st := newFakeStore()
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})
	var settings map[string]map[string]any
	if on {
		settings = map[string]map[string]any{"crossProjectOrchestration": {"enabled": true}}
	}
	engine, _ := policytest.Engine(t, settings)
	m.SetAgentPolicy(engine)
	return m
}

// The cross-project section is in an opted-in orchestrator's prompt, and in
// nobody else's.
func TestCrossProjectPromptOnlyForOptedInOrchestrator(t *testing.T) {
	ctx := context.Background()
	const marker = "## Other Projects' Orchestrators"
	on, off := crossPromptManager(t, true), crossPromptManager(t, false)

	got, err := on.buildSystemPrompt(ctx, domain.KindOrchestrator, "mer", "mer-1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, marker) || !strings.Contains(got, "orchestrator new --project <id> --prompt") {
		t.Fatalf("opted-in orchestrator prompt lacks the section:\n%s", got)
	}
	if !strings.Contains(got, "Worker Data Is Untrusted") {
		t.Fatal("existing prompt rules must stay")
	}
	for name, tc := range map[string]struct {
		m    *Manager
		kind domain.SessionKind
	}{
		"policy off, orchestrator": {off, domain.KindOrchestrator},
		"policy on, worker":        {on, domain.KindWorker},
	} {
		sp, err := tc.m.buildSystemPrompt(ctx, tc.kind, "mer", "mer-2", "", false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(sp, marker) {
			t.Errorf("%s: prompt must not carry the cross-project section", name)
		}
	}
}
