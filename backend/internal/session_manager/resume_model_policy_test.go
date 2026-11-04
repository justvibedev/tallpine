package sessionmanager

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	browsersvc "tallpine.eu/tallpine/backend/internal/service/browser"
)

// resumeRecorder records the full starts (resumes) the manager asks for.
type resumeRecorder struct {
	*deadHostLauncher
	rmu     sync.Mutex
	resumed []ChatStart
}

func (r *resumeRecorder) StartChat(ctx context.Context, cfg ChatStart) (ChatStarted, error) {
	if !cfg.ReconnectOnly {
		r.rmu.Lock()
		r.resumed = append(r.resumed, cfg)
		r.rmu.Unlock()
	}
	return r.deadHostLauncher.StartChat(ctx, cfg)
}

func (r *resumeRecorder) starts() []ChatStart {
	r.rmu.Lock()
	defer r.rmu.Unlock()
	return append([]ChatStart(nil), r.resumed...)
}

// stoppedChat is a Codex Chat that resumes with model, under project config cfg.
func stoppedChat(t *testing.T, model string, cfg domain.ProjectConfig) (*Manager, *fakeStore, *resumeRecorder, domain.SessionRecord) {
	t.Helper()
	m, st, _, _ := newLifecycleManager()
	launcher := &resumeRecorder{deadHostLauncher: newDeadHostLauncher()}
	m.chat = launcher
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	rec := deadHostChat("mer-resume")
	rec.Activity.State = domain.ActivityExited
	rec.Metadata.Model = model
	st.sessions[rec.ID] = rec
	return m, st, launcher, rec
}

func TestResumeFallsBackFromADeniedModelAndSaysSo(t *testing.T) {
	t.Parallel()
	cfg := testRoleAgents()
	cfg.AgentConfig.Model = "project-default"
	cfg.Worker.Harness = domain.HarnessCodex
	cfg.Models.Denylist = []string{"banned-model"}
	m, st, launcher, rec := stoppedChat(t, "Banned-Model", cfg)

	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	starts := launcher.starts()
	if len(starts) != 1 || starts[0].Model != "project-default" {
		t.Fatalf("resumed on %+v, want the project default", starts)
	}
	if n := starts[0].ModelNotice; !strings.Contains(n, "Banned-Model") || !strings.Contains(n, "project-default") || !strings.Contains(n, "blocked by project policy") {
		t.Fatalf("notice = %q", n)
	}
	if got := st.sessions[rec.ID].Metadata.Model; got != "project-default" {
		t.Fatalf("persisted model = %q, want the fallback", got)
	}
}

func TestResumeFallsBackToTheHarnessDefaultWhenTheProjectHasNone(t *testing.T) {
	t.Parallel()
	cfg := testRoleAgents()
	cfg.Models.Denylist = []string{"banned-model"}
	m, _, launcher, rec := stoppedChat(t, "banned-model", cfg)
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if starts := launcher.starts(); len(starts) != 1 || starts[0].Model != domain.DefaultCodexModel {
		t.Fatalf("resumed on %+v, want the Codex default", starts)
	}
}

func TestResumeKeepsAnAllowedModel(t *testing.T) {
	t.Parallel()
	cfg := testRoleAgents()
	cfg.AgentConfig.Model = "project-default"
	cfg.Worker.Harness = domain.HarnessCodex
	cfg.Models.Denylist = []string{"banned-model"}
	m, st, launcher, rec := stoppedChat(t, "fine-model", cfg)
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	starts := launcher.starts()
	if len(starts) != 1 || starts[0].Model != "fine-model" || starts[0].ModelNotice != "" {
		t.Fatalf("an allowed model changed: %+v", starts)
	}
	if got := st.sessions[rec.ID].Metadata.Model; got != "fine-model" {
		t.Fatalf("persisted model = %q", got)
	}
}

// With no allowed fallback the resume is refused cleanly: nothing starts, the
// saved model is kept, and the startup retry ladder does not try again.
func TestResumeRefusesWhenNoAllowedFallbackExistsAndDoesNotRetry(t *testing.T) {
	t.Parallel()
	cfg := testRoleAgents()
	cfg.Models.CodexDefault = "banned-model" // the harness default is itself denied
	cfg.Models.Denylist = []string{"banned-model"}
	m, st, launcher, rec := stoppedChat(t, "banned-model", cfg)

	_, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
	if !errors.Is(err, ErrResumeModelDenied) {
		t.Fatalf("resume = %v, want ErrResumeModelDenied", err)
	}
	var denied *domain.ModelDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("refusal does not carry the policy error: %v", err)
	}
	if len(launcher.starts()) != 0 {
		t.Fatalf("a refused resume still started: %+v", launcher.starts())
	}
	if got := st.sessions[rec.ID].Metadata.Model; got != "banned-model" {
		t.Fatalf("a refused resume changed the saved model to %q", got)
	}

	// The auto-resume after a restart treats the refusal as final.
	m.setAgentExitReason(context.Background(), rec.ID, domain.AgentExitHostLost)
	done, err := m.resumeStartupChatOnce(context.Background(), rec.ID)
	if !done || !errors.Is(err, ErrResumeModelDenied) {
		t.Fatalf("auto-resume done=%v err=%v; a denied model must end the retry ladder", done, err)
	}
}

func TestResumeFailsClosedWhenTheProjectCannotBeRead(t *testing.T) {
	t.Parallel()
	m, st, _, rec := stoppedChat(t, "any-model", testRoleAgents())
	st.getProjectErr = errors.New("db locked")
	_, _, err := m.enforceResumeModel(context.Background(), rec, ports.AgentConfig{Model: "any-model"})
	if !errors.Is(err, ErrResumeModelPolicyUnavailable) {
		t.Fatalf("unreadable project = %v, want ErrResumeModelPolicyUnavailable", err)
	}
}

// modelWrites records the models persisted through UpdateSessionModel. The fake
// lifecycle's MarkSpawned replaces session metadata wholesale, unlike the real
// one (which keeps the stored model), so the persisted value is observed at the
// store instead of on the final record.
type modelWrites struct {
	*fakeStore
	models []string
}

func (s *modelWrites) UpdateSessionModel(ctx context.Context, id domain.SessionID, model string) (bool, error) {
	s.models = append(s.models, model)
	return s.fakeStore.UpdateSessionModel(ctx, id, model)
}

// terminalRestore restores a terminated terminal session saved on model, under
// project config cfg, through the real RestoreWithMode path.
func terminalRestore(t *testing.T, harness domain.AgentHarness, model string, cfg domain.ProjectConfig) (*Manager, *modelWrites, *fakeRuntime, *recordingAgent) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x", Model: model})
	rec := st.sessions["mer-1"]
	rec.Harness = harness
	st.sessions["mer-1"] = rec
	agent := &recordingAgent{}
	rt := &fakeRuntime{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	writes := &modelWrites{fakeStore: st}
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: writes, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})
	return m, writes, rt, agent
}

// A terminal restore re-checks the saved model the way a Chat resume does. A
// terminal has no timeline, so the replacement is logged and persisted.
func TestTerminalRestoreFallsBackFromADeniedModel(t *testing.T) {
	t.Parallel()
	cfg := domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "project-default-model"}, Models: domain.ModelPolicy{Denylist: []string{"banned-model"}}}
	m, st, _, agent := terminalRestore(t, domain.HarnessClaudeCode, "Banned-Model", cfg)

	if _, err := m.RestoreWithMode(context.Background(), "mer-1"); err != nil {
		t.Fatalf("RestoreWithMode: %v", err)
	}
	if agent.lastConfig.Model != "project-default-model" {
		t.Fatalf("restored on %q, want the project default", agent.lastConfig.Model)
	}
	if len(st.models) != 1 || st.models[0] != "project-default-model" {
		t.Fatalf("persisted models = %v, want the fallback once", st.models)
	}
}

func TestTerminalRestoreKeepsAnAllowedModel(t *testing.T) {
	t.Parallel()
	cfg := domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "project-default-model"}, Models: domain.ModelPolicy{Denylist: []string{"banned-model"}}}
	m, st, _, agent := terminalRestore(t, domain.HarnessClaudeCode, "fine-model", cfg)
	if _, err := m.RestoreWithMode(context.Background(), "mer-1"); err != nil {
		t.Fatalf("RestoreWithMode: %v", err)
	}
	if agent.lastConfig.Model != "fine-model" || len(st.models) != 0 {
		t.Fatalf("an allowed model changed: restored on %q, persisted %v", agent.lastConfig.Model, st.models)
	}
}

// With no allowed fallback the restore is refused before any runtime exists,
// and the session stays as it was: stopped, with its saved model.
func TestTerminalRestoreRefusesWhenNoAllowedFallbackExists(t *testing.T) {
	t.Parallel()
	cfg := domain.ProjectConfig{Models: domain.ModelPolicy{CodexDefault: "banned-model", Denylist: []string{"banned-model"}}}
	m, st, rt, agent := terminalRestore(t, domain.HarnessCodex, "banned-model", cfg)

	_, err := m.RestoreWithMode(context.Background(), "mer-1")
	if !errors.Is(err, ErrResumeModelDenied) {
		t.Fatalf("RestoreWithMode = %v, want ErrResumeModelDenied", err)
	}
	rec := st.sessions["mer-1"]
	if rt.created != 0 || agent.restoreCalls != 0 || len(st.models) != 0 {
		t.Fatalf("a refused restore started something: runtimes=%d restores=%d", rt.created, agent.restoreCalls)
	}
	if !rec.IsTerminated || rec.Activity.State != domain.ActivityExited || rec.Metadata.Model != "banned-model" {
		t.Fatalf("a refused restore changed the session: terminated=%v state=%q model=%q", rec.IsTerminated, rec.Activity.State, rec.Metadata.Model)
	}
}

// failedAsyncSpawn leaves a Codex Chat whose first start failed before any
// provider conversation existed, saved on model, ready for a retry.
func failedAsyncSpawn(t *testing.T, model string) (*Manager, *fakeStore, *recordingLauncher, *[]func(), domain.SessionRecord) {
	t.Helper()
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	m.dataDir = t.TempDir()
	m.browserCapabilities = browsersvc.NewAuthority()
	m.modelCatalog = tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "gpt-test", Efforts: []string{"low", "high"}}, {ID: "gpt-default", Efforts: []string{"low", "high"}},
	}}}
	deferred := deferredBackground(m)
	ws := m.workspace.(*fakeWorkspace)
	ws.createErr = errors.New("temporary git failure")
	cfg := asyncChatSpawnConfig("do the thing")
	cfg.AgentConfig = ports.AgentConfig{Model: model}
	rec, _, _, err := m.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	(*deferred)[0]()
	if st.sessions[rec.ID].ProvisionState != domain.SessionProvisionFailed {
		t.Fatal("initial start did not fail")
	}
	ws.createErr = nil
	return m, st, launcher, deferred, rec
}

// The retry of a start that failed before a conversation existed re-checks the
// saved model: it was allowed when the session was created, denied since.
func TestAsyncRetryFallsBackFromAModelDeniedSinceTheFirstAttempt(t *testing.T) {
	t.Parallel()
	m, st, launcher, deferred, rec := failedAsyncSpawn(t, "gpt-test")
	project := st.projects[string(chatTestProject)]
	project.Config.AgentConfig.Model = "gpt-default"
	project.Config.Worker.Harness = domain.HarnessCodex
	project.Config.Models.Denylist = []string{"gpt-test"}
	st.projects[string(chatTestProject)] = project

	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	(*deferred)[1]()
	if len(launcher.started) != 1 {
		t.Fatalf("controllers started = %d, want 1", len(launcher.started))
	}
	got := launcher.started[0]
	if got.Model != "gpt-default" || !strings.Contains(got.ModelNotice, "gpt-test") || !strings.Contains(got.ModelNotice, "gpt-default") {
		t.Fatalf("retry started on %q with notice %q, want the project default and a notice", got.Model, got.ModelNotice)
	}
	if st.sessions[rec.ID].Metadata.Model != "gpt-default" {
		t.Fatalf("persisted model = %q", st.sessions[rec.ID].Metadata.Model)
	}
}

// Codex resolves its saved pair separately, and that path must not skip the policy.
func TestAsyncRetryRefusesWhenNoAllowedFallbackExists(t *testing.T) {
	t.Parallel()
	m, st, launcher, deferred, rec := failedAsyncSpawn(t, "gpt-test")
	project := st.projects[string(chatTestProject)]
	project.Config.Models.CodexDefault = "gpt-test" // the harness default is itself denied
	project.Config.Models.Denylist = []string{"gpt-test"}
	st.projects[string(chatTestProject)] = project

	_, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
	if !errors.Is(err, ErrResumeModelDenied) {
		t.Fatalf("retry = %v, want ErrResumeModelDenied", err)
	}
	if len(*deferred) != 1 || len(launcher.started) != 0 {
		t.Fatalf("a refused retry started something: background=%d started=%d", len(*deferred), len(launcher.started))
	}
	if _, statErr := os.Stat(m.systemPromptDir(rec.ID)); !os.IsNotExist(statErr) {
		t.Fatalf("a refused retry left its system prompt dir behind: %v", statErr)
	}
	stored := st.sessions[rec.ID]
	if stored.ProvisionState != domain.SessionProvisionFailed || stored.Metadata.Model != "gpt-test" {
		t.Fatalf("a refused retry changed the session: %q model %q", stored.ProvisionState, stored.Metadata.Model)
	}
}

// A project the session points at that cannot be found is not "no policy".
func TestResumeFailsClosedWhenTheProjectRowIsMissing(t *testing.T) {
	t.Parallel()
	m, st, _, rec := stoppedChat(t, "any-model", testRoleAgents())
	delete(st.projects, "mer")
	_, _, err := m.enforceResumeModel(context.Background(), rec, ports.AgentConfig{Model: "any-model"})
	if !errors.Is(err, ErrResumeModelPolicyUnavailable) {
		t.Fatalf("missing project = %v, want ErrResumeModelPolicyUnavailable", err)
	}
}

// A fallback that cannot be saved refuses with its own reason.
func TestResumeFallbackSaveFailureIsItsOwnError(t *testing.T) {
	t.Parallel()
	cfg := testRoleAgents()
	cfg.Models.Denylist = []string{"banned-model"}
	m, st, _, rec := stoppedChat(t, "banned-model", cfg)
	st.updateSessionErr = errors.New("disk full")
	_, _, err := m.enforceResumeModel(context.Background(), rec, ports.AgentConfig{Model: "banned-model"})
	if !errors.Is(err, ErrResumeFallbackNotSaved) || errors.Is(err, ErrResumeModelPolicyUnavailable) {
		t.Fatalf("save failure = %v, want ErrResumeFallbackNotSaved only", err)
	}
}

// postingLauncher can post to the timeline before a start, so a start that then
// fails still leaves the notice.
type postingLauncher struct {
	*resumeRecorder
	posted []string
}

func (p *postingLauncher) PostSessionNotice(_ context.Context, _ domain.SessionID, summary string) (bool, error) {
	p.posted = append(p.posted, summary)
	return true, nil
}

func TestResumeNoticeIsPostedBeforeTheStartAndSurvivesItsFailure(t *testing.T) {
	t.Parallel()
	cfg := testRoleAgents()
	cfg.AgentConfig.Model = "project-default"
	cfg.Worker.Harness = domain.HarnessCodex
	cfg.Models.Denylist = []string{"banned-model"}
	m, _, launcher, rec := stoppedChat(t, "banned-model", cfg)
	launcher.failures = 1 // the start itself fails
	poster := &postingLauncher{resumeRecorder: launcher}
	m.chat = poster

	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err == nil {
		t.Fatal("the failing start succeeded")
	}
	if len(poster.posted) != 1 || !strings.Contains(poster.posted[0], "banned-model") {
		t.Fatalf("notice posted = %v, want it on the timeline even though the start failed", poster.posted)
	}
	for _, start := range launcher.starts() {
		if start.ModelNotice != "" {
			t.Fatalf("a posted notice is carried a second time on the start: %q", start.ModelNotice)
		}
	}
}
