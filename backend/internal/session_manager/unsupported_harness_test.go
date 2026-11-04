package sessionmanager

import (
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A session saved under a removed harness has no adapter. Every operation that
// would run its agent is refused with the read-only explanation; killing it
// stays possible.
func TestRemovedHarnessSessionIsReadOnly(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo"}
	st.sessions["mer-1"] = pastStartupGate(domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.AgentHarness("aider"),
		Mode: domain.SessionModeTUI, Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", RuntimeHandleID: "h1"},
	})
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: testAgentRegistry{domain.HarnessClaudeCode: &recordingAgent{}},
		Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	checks := map[string]error{
		"send": m.SendWithOptions(ctx, "mer-1", "hello", nil, ports.MessageDeliveryOptions{AuthoredByUser: true}),
	}
	rec := st.sessions["mer-1"]
	rec.IsTerminated = true
	st.sessions["mer-1"] = rec
	_, restoreErr := m.RestoreWithMode(ctx, "mer-1")
	checks["restore"] = restoreErr
	_, resumeErr := m.ResumeAgentWithMode(ctx, "mer-1")
	checks["resume"] = resumeErr

	for name, err := range checks {
		if !errors.Is(err, ErrUnsupportedHarness) {
			t.Errorf("%s error = %v, want ErrUnsupportedHarness", name, err)
			continue
		}
		if !strings.Contains(err.Error(), `"aider"`) || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s error %q does not explain the read-only session", name, err)
		}
	}
}

// restore-all runs at every daemon start. A removed harness must be skipped, not
// crash the loop or block the sessions after it.
func TestRestoreAllSkipsRemovedHarnessSessions(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo"}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.AgentHarness("aider"),
		IsTerminated: true, Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1"},
	}
	ws := &fakeWorkspace{}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: testAgentRegistry{domain.HarnessClaudeCode: &recordingAgent{}},
		Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
	})
	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}
	if st.sessions["mer-1"].Harness != domain.AgentHarness("aider") {
		t.Fatal("restore-all rewrote the stored harness")
	}
}
