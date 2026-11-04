package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// The scheduler's durable run identity must cross the existing spawn boundary
// into the seed row so retries can adopt the same session.
func TestSeedRecordPreservesAutomationRunIdentity(t *testing.T) {
	t.Parallel()

	runID := domain.AutomationRunID("run-1")
	rec := seedRecord(agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "scheduled", Kind: domain.KindWorker,
		AutomationRunID: &runID,
	}, domain.ProjectConfig{}, domain.WorkflowDefaults{}, time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC))
	if rec.AutomationRunID == nil || *rec.AutomationRunID != runID {
		t.Fatalf("automation run id = %v, want %q", rec.AutomationRunID, runID)
	}
}

func TestSpawn_ResolvesProjectConfig(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{
		DefaultBranch: "develop",
		Env:           map[string]string{"FOO": "bar"},
		AgentConfig:   domain.AgentConfig{Model: "base-model"},
		// A worker role override wins over the base agent config for workers.
		Worker: domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "worker-model"}},
	}}
	agent := &recordingAgent{}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if agent.lastConfig.Model != "worker-model" {
		t.Fatalf("launch model = %q, want role override worker-model", agent.lastConfig.Model)
	}
	if rec.Harness != domain.HarnessCodex {
		t.Fatalf("harness = %q, want codex from role override", rec.Harness)
	}
	if ws.lastCfg.BaseBranch != "develop" {
		t.Fatalf("workspace base branch = %q, want develop", ws.lastCfg.BaseBranch)
	}
	if rt.lastCfg.Env["FOO"] != "bar" {
		t.Fatalf("runtime env FOO = %q, want bar", rt.lastCfg.Env["FOO"])
	}
	if rt.lastCfg.Env[EnvSessionID] == "" {
		t.Fatal("runtime env missing TALLPINE_SESSION_ID")
	}

	agent.lastConfig = ports.AgentConfig{}
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID:   "mer",
		Kind:        domain.KindWorker,
		AgentConfig: ports.AgentConfig{Model: "request-model"},
	}); err != nil {
		t.Fatal(err)
	}
	if agent.lastConfig.Model != "request-model" {
		t.Fatalf("launch model = %q, want request model override", agent.lastConfig.Model)
	}

	// A project with no stored config defaults new sessions to Auto permissions.
	st.projects["bare"] = domain.ProjectRecord{ID: "bare"}
	agent.lastConfig = ports.AgentConfig{Model: "stale"}
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "bare", Kind: domain.KindWorker, Harness: domain.HarnessCodex}); err != nil {
		t.Fatal(err)
	}
	if agent.lastConfig != (ports.AgentConfig{Permissions: ports.PermissionModeAuto}) {
		t.Fatalf("launch config = %#v, want Auto permissions for project without config", agent.lastConfig)
	}
	if got := ws.lastCfg.BaseBranch; got != "" {
		t.Fatalf("automatic workspace base branch = %q, want empty for adapter inference", got)
	}
}

func TestSpawn_IgnoresNonOrchestratorParent(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	st.sessions["mer-0"] = domain.SessionRecord{ID: "mer-0", ProjectID: "mer", Kind: domain.KindWorker}
	st.conversations["mer-0"] = domain.ConversationRecord{
		SessionID: "mer-0", Settings: domain.ConversationSettings{ApprovalMode: domain.PermissionModeBypassPermissions},
	}

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, ParentSessionID: "mer-0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.lastCfg.Env[EnvPermissionMode]; got != string(domain.PermissionModeAuto) {
		t.Fatalf("worker permission environment = %q, want project default %q", got, domain.PermissionModeAuto)
	}
}

func TestSpawnHarnessGateUsesResolvedProjectDefault(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	project := st.projects["mer"]
	project.Config.Worker.Harness = domain.HarnessKiro
	st.projects["mer"] = project
	gate := &rejectingHarnessUseGate{}
	m.SetHarnessUseGate(gate)

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if !errors.Is(err, ErrHarnessInstallActive) {
		t.Fatalf("Spawn error = %v, want ErrHarnessInstallActive", err)
	}
	if gate.harness != domain.HarnessKiro {
		t.Fatalf("gated harness = %q, want resolved Kiro default", gate.harness)
	}
	if rt.created != 0 {
		t.Fatal("runtime was created while Kiro installer owned the gate")
	}
}

// A project saved before a harness was removed still names it as the role
// default. Spawning without an explicit harness must resolve to claude-code
// instead of failing, and an explicit request for the removed harness is still
// refused.
func TestSpawnFallsBackWhenProjectDefaultHarnessWasRemoved(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	project := st.projects["mer"]
	project.Config.Worker.Harness = domain.AgentHarness("droid")
	st.projects["mer"] = project
	gate := &rejectingHarnessUseGate{}
	m.SetHarnessUseGate(gate)

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); !errors.Is(err, ErrHarnessInstallActive) {
		t.Fatalf("Spawn error = %v, want ErrHarnessInstallActive (spawn reached the fallback harness)", err)
	}
	if gate.harness != domain.HarnessClaudeCode {
		t.Fatalf("gated harness = %q, want the claude-code fallback", gate.harness)
	}
}

func TestSpawnRejectsExplicitRemovedHarness(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: testAgentRegistry{domain.HarnessClaudeCode: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.AgentHarness("droid")})
	if !errors.Is(err, ErrUnknownHarness) {
		t.Fatalf("Spawn error = %v, want ErrUnknownHarness", err)
	}
}

// TestSpawnModelValidation asserts spawn rejects models a fixed-catalog harness
// cannot honor, while harnesses that accept arbitrary model ids pass through.
func TestSpawnModelValidation(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	agent := &recordingAgent{}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	// Codex accepts arbitrary custom model ids.
	st.projects["codex-proj"] = domain.ProjectRecord{ID: "codex-proj", Config: domain.ProjectConfig{
		Worker: domain.RoleOverride{Harness: domain.HarnessCodex},
	}}
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "codex-proj", Kind: domain.KindWorker, AgentConfig: ports.AgentConfig{Model: "custom-snapshot-id"}}); err != nil {
		t.Fatalf("codex spawn with custom model failed: %v", err)
	}
}

// TestSpawnAmpModelOverrideLaunchArgv uses the real Amp adapter to verify that
// a spawn model override for Amp ends up as `--mode <value>` in the actual
// launch argv instead of silently being dropped because Amp reads Config.Mode.
func TestSpawnModelPersisted(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{
		AgentConfig: domain.AgentConfig{Model: "project-model"},
		Worker:      domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "role-model"}},
	}}
	agent := &recordingAgent{}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, AgentConfig: ports.AgentConfig{Model: "spawn-model"}})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	if rec.Metadata.Model != "spawn-model" {
		t.Fatalf("spawn metadata model = %q, want spawn-model", rec.Metadata.Model)
	}
	if agent.lastConfig.Model != "spawn-model" {
		t.Fatalf("launch model = %q, want spawn-model", agent.lastConfig.Model)
	}

	stored, ok, err := st.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !ok {
		t.Fatalf("session %s not found", rec.ID)
	}
	if stored.Metadata.Model != "spawn-model" {
		t.Fatalf("stored metadata model = %q, want spawn-model", stored.Metadata.Model)
	}
}

// A spawn that names a harness other than the role override's must not inherit
// that role's model: it was tuned for the other agent and would reach the
// selected harness as an unknown provider alias. The harness picks its own
// default instead, while harness-neutral permissions still carry over.
// Both roles resolve through the same override, so both leak the same way; the
// orchestrator case is asserted explicitly rather than left implied.
func TestSpawn_DropsRoleModelOnHarnessMismatch(t *testing.T) {
	t.Parallel()

	roleConfig := domain.RoleOverride{
		Harness:     domain.AgentHarness("opencode"),
		AgentConfig: domain.AgentConfig{Model: "custom/gpt-5.5", Permissions: domain.PermissionModeAuto},
	}
	for _, tc := range []struct {
		name string
		kind domain.SessionKind
		cfg  domain.ProjectConfig
	}{
		{"worker", domain.KindWorker, domain.ProjectConfig{Worker: roleConfig}},
		{"orchestrator", domain.KindOrchestrator, domain.ProjectConfig{Orchestrator: roleConfig}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: tc.cfg}
			agent := &recordingAgent{}
			m := New(Deps{
				Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{},
				Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
				LookPath: func(string) (string, error) { return "/bin/true", nil },
			})

			rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
				ProjectID: "mer", Kind: tc.kind, Harness: domain.HarnessCodex,
			})
			if err != nil {
				t.Fatal(err)
			}
			if agent.lastConfig.Model != "" {
				t.Fatalf("launch model = %q, want empty so codex uses its own default", agent.lastConfig.Model)
			}
			if agent.lastConfig.Permissions != domain.PermissionModeAuto {
				t.Fatalf("launch permissions = %q, want auto (harness-neutral)", agent.lastConfig.Permissions)
			}
			// The mismatched model must not survive into the record the API
			// and UI read back either.
			stored, ok, err := st.GetSession(ctx, rec.ID)
			if err != nil || !ok {
				t.Fatalf("get session: err=%v ok=%v", err, ok)
			}
			if stored.Metadata.Model != "" {
				t.Fatalf("persisted metadata model = %q, want empty", stored.Metadata.Model)
			}
		})
	}
}

func TestSpawnRecordsDiffBaseForSingleRepoSessions(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	repo := newManagerGitRepo(t)
	cfg := testRoleAgents()
	cfg.DefaultBranch = "main"
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: repo, Config: cfg}
	ws.path = repo

	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	wantBase := strings.TrimSpace(runManagerGit(t, repo, "rev-parse", "main"))
	if rec.Metadata.DiffBaseSHA != wantBase || rec.Metadata.DiffBaseRef != "main" {
		t.Fatalf("spawn diff base = sha:%q ref:%q, want %s main", rec.Metadata.DiffBaseSHA, rec.Metadata.DiffBaseRef, wantBase)
	}
}

func TestSpawnAutoRecordsResolvedDiffBaseInsteadOfFeatureTip(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	repo := newManagerGitRepo(t)
	wantBase := strings.TrimSpace(runManagerGit(t, repo, "rev-parse", "main"))
	runManagerGit(t, repo, "switch", "-c", "feature/temporary")
	if err := os.WriteFile(filepath.Join(repo, "existing-feature.txt"), []byte("already here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runManagerGit(t, repo, "add", "existing-feature.txt")
	runManagerGit(t, repo, "commit", "-m", "existing feature work")
	featureTip := strings.TrimSpace(runManagerGit(t, repo, "rev-parse", "HEAD"))

	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: repo, Config: testRoleAgents()}
	ws.path = repo
	ws.baseRef = "refs/heads/main"
	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Metadata.DiffBaseSHA != wantBase || rec.Metadata.DiffBaseRef != "refs/heads/main" {
		t.Fatalf("auto diff base = sha:%q ref:%q, want %s refs/heads/main", rec.Metadata.DiffBaseSHA, rec.Metadata.DiffBaseRef, wantBase)
	}
	if rec.Metadata.DiffBaseSHA == featureTip {
		t.Fatal("auto diff base incorrectly collapsed to the existing feature tip")
	}
}

func TestSpawnRecordsRemoteTrackingDiffBaseWhenLocalDefaultBranchLags(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	repo := newManagerGitRepo(t)
	localMain := strings.TrimSpace(runManagerGit(t, repo, "rev-parse", "HEAD"))
	runManagerGit(t, repo, "switch", "-c", "upstream-main")
	if err := os.WriteFile(filepath.Join(repo, "upstream.txt"), []byte("upstream\n"), 0o644); err != nil {
		t.Fatalf("write upstream file: %v", err)
	}
	runManagerGit(t, repo, "add", "upstream.txt")
	runManagerGit(t, repo, "commit", "-m", "upstream change")
	originMain := strings.TrimSpace(runManagerGit(t, repo, "rev-parse", "HEAD"))
	runManagerGit(t, repo, "update-ref", "refs/remotes/origin/main", originMain)
	runManagerGit(t, repo, "switch", "-c", "tp/work")
	runManagerGit(t, repo, "branch", "-f", "main", localMain)
	cfg := testRoleAgents()
	cfg.DefaultBranch = "main"
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: repo, Config: cfg}
	ws.path = repo

	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Metadata.DiffBaseSHA != originMain || rec.Metadata.DiffBaseRef != "origin/main" {
		t.Fatalf("spawn diff base = sha:%q ref:%q, want %s origin/main", rec.Metadata.DiffBaseSHA, rec.Metadata.DiffBaseRef, originMain)
	}
}

func TestSpawnDiffBaseRefCandidatesPreferRemoteTrackingDefault(t *testing.T) {
	t.Parallel()

	got := spawnDiffBaseRefCandidates("main")
	want := []string{"origin/main", "refs/remotes/origin/main", "main"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("spawn diff candidates = %#v, want %#v", got, want)
	}
}

func TestSpawn_WrapsSupervisedAgentAndPersistsGeneration(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "--model", "gpt-5"}}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		Executable:  func() (string, error) { return "/opt/tallpine", nil },
		NewLaunchID: func() string { return "launch-7" },
	})

	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex})
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := []string{"/opt/tallpine", "agent-process", "supervise", "--session", "mer-1", "--launch", "launch-7", "--", "codex", "--model", "gpt-5"}
	if !reflect.DeepEqual(rt.lastCfg.Argv, wantArgv) {
		t.Fatalf("runtime argv = %#v, want %#v", rt.lastCfg.Argv, wantArgv)
	}
	if got := rt.lastCfg.Env[EnvRuntimeLaunchID]; got != "launch-7" {
		t.Fatalf("runtime launch env = %q, want launch-7", got)
	}
	if rec.Metadata.RuntimeLaunchID != "launch-7" {
		t.Fatalf("stored launch id = %q, want launch-7", rec.Metadata.RuntimeLaunchID)
	}
}

func TestSpawn_ExplicitHarnessWinsWithoutProjectRoleHarness(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].Harness; got != domain.HarnessCodex {
		t.Fatalf("explicit harness = %q, want %q", got, domain.HarnessCodex)
	}
}

func TestSpawn_AssignsIDAndGoesIdle(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	s, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "mer-1" {
		t.Fatalf("got %q", s.ID)
	}
	if s.Activity.State != domain.ActivityIdle {
		t.Fatalf("fresh session records idle, got %q", s.Activity.State)
	}
	if rt.created != 1 {
		t.Fatal("runtime not created")
	}
	if st.sessions["mer-1"].Metadata.RuntimeHandleID != "h1" {
		t.Fatal("handle not folded")
	}
}

func TestSpawnAutomationAdoptsOnlyCompletedLaunch(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	runID := domain.AutomationRunID("run-1")
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Prompt: "do it", AutomationRunID: &runID}
	first, _, _, err := m.Spawn(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !st.sessions[first.ID].AutomationLaunchCompleted {
		t.Fatal("successful automation spawn did not persist launch completion")
	}
	adopted, _, _, err := m.Spawn(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.ID != first.ID || rt.created != 1 {
		t.Fatalf("retry session=%q runtime creates=%d, want %q and 1", adopted.ID, rt.created, first.ID)
	}
}

func TestSpawnClientRequestReplaysCommittedWorkerAndRejectsConflictOrIncomplete(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Prompt: "do it", ClientRequestID: "draft-1", ClientRequestHash: "v1:first"}
	first, _, _, err := m.Spawn(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	replay, _, _, err := m.Spawn(ctx, cfg)
	if err != nil || replay.ID != first.ID || rt.created != 1 {
		t.Fatalf("replay = %q, runtime creates = %d, err = %v", replay.ID, rt.created, err)
	}
	cfg.ClientRequestHash = "v1:changed"
	if _, _, _, err := m.Spawn(ctx, cfg); !errors.Is(err, ErrClientRequestConflict) || rt.created != 1 {
		t.Fatalf("changed payload: runtime creates = %d, err = %v", rt.created, err)
	}
	cfg.ClientRequestHash = "v1:first"
	rec := st.sessions[first.ID]
	rec.ClientRequestCommitted = false
	st.sessions[first.ID] = rec
	if _, _, _, err := m.Spawn(ctx, cfg); !errors.Is(err, ErrClientRequestIncomplete) || rt.created != 1 {
		t.Fatalf("incomplete retry: runtime creates = %d, err = %v", rt.created, err)
	}
}

func TestSpawnClientRequestRetriesAfterStorageFullWithoutDuplicateWorker(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	st.createClientRequestErr = syscall.ENOSPC
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Prompt: "do it", ClientRequestID: "draft-1", ClientRequestHash: "v1:first"}
	if _, _, _, err := m.Spawn(ctx, cfg); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("full-storage spawn error = %v, want ENOSPC", err)
	}
	if len(st.sessions) != 0 || ws.createCount != 0 || rt.created != 0 {
		t.Fatalf("failed write left sessions=%d workspaces=%d runtimes=%d", len(st.sessions), ws.createCount, rt.created)
	}
	created, _, _, err := m.Spawn(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	replayed, _, _, err := m.Spawn(ctx, cfg)
	if err != nil || replayed.ID != created.ID || len(st.sessions) != 1 || ws.createCount != 1 || rt.created != 1 {
		t.Fatalf("retry/replay: created=%q replayed=%q sessions=%d workspaces=%d runtimes=%d err=%v", created.ID, replayed.ID, len(st.sessions), ws.createCount, rt.created, err)
	}
}

func TestSpawnPreparedClientRequestRetriesAfterStorageFullWithoutDuplicateWorker(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	m.runBackground = func(work func()) { work() }
	token, err := m.PrepareTaskWorkspace(ctx, st.projects["mer"])
	if err != nil {
		t.Fatal(err)
	}
	st.promoteTaskErr = syscall.ENOSPC
	st.deletePrepErr = syscall.ENOSPC
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Prompt: "do it", TaskPreparation: token, ClientRequestID: "draft-1", ClientRequestHash: "v1:first"}
	if _, _, _, err := m.Spawn(ctx, cfg); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("full-storage promotion error = %v, want ENOSPC", err)
	}
	if len(st.sessions) != 1 || ws.createCount != 1 || ws.destroyed != 1 || rt.created != 0 {
		t.Fatalf("failed promotion left sessions=%d workspaces=%d destroyed=%d runtimes=%d", len(st.sessions), ws.createCount, ws.destroyed, rt.created)
	}
	st.deletePrepErr = nil // storage recovered; retry must not reclaim the failed preparation
	created, _, _, err := m.Spawn(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	replayed, _, _, err := m.Spawn(ctx, cfg)
	if err != nil || replayed.ID != created.ID || len(st.sessions) != 2 || !st.sessions[domain.SessionID(token)].IsTaskPreparation || ws.createCount != 2 || rt.created != 1 {
		t.Fatalf("retry/replay: created=%q replayed=%q sessions=%d workspaces=%d runtimes=%d err=%v", created.ID, replayed.ID, len(st.sessions), ws.createCount, rt.created, err)
	}
	if err := m.CancelTaskPreparation(ctx, token); err != nil || len(st.sessions) != 1 {
		t.Fatalf("recovered cleanup: sessions=%d err=%v", len(st.sessions), err)
	}
}

func TestSpawn_PromptDeliveryStrategyFailureCleansUpWorkspaceProjectRows(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{
		ID:     "mer",
		Path:   "/repo/mer",
		Kind:   domain.ProjectKindWorkspace,
		Config: testRoleAgents(),
	}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	m := New(Deps{
		Runtime:   rt,
		Agents:    singleAgent{agent: promptStrategyErrorAgent{recordingAgent: &recordingAgent{}, err: errors.New("strategy unsupported")}},
		Workspace: ws,
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "fix the button"})
	if err == nil || !strings.Contains(err.Error(), "prompt delivery") {
		t.Fatalf("Spawn err = %v, want prompt delivery failure", err)
	}
	if rt.created != 0 {
		t.Fatalf("runtime created = %d, want 0", rt.created)
	}
	if ws.projectDestroyed != 1 {
		t.Fatalf("workspace project destroy calls = %d, want 1", ws.projectDestroyed)
	}
	if ws.destroyed != 0 {
		t.Fatalf("single-workspace destroy calls = %d, want 0", ws.destroyed)
	}
	if _, present := st.sessions["mer-1"]; present {
		t.Fatal("seed row should be deleted after prompt strategy failure")
	}
	if rows := st.worktrees["mer-1"]; len(rows) != 0 {
		t.Fatalf("stale session worktree rows = %#v, want deleted", rows)
	}
}

// TestSpawn_StampsUTCTimestamps locks the default clock to UTC so spawn-stamped
// CreatedAt/UpdatedAt match every other session write (rename, activity), which
// all use time.Now().UTC(). A local default produced mixed-timezone timestamps
// in `tallpine session get` (created in local time, updated in UTC).
func TestSpawn_StampsUTCTimestamps(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}
	rec := st.sessions["mer-1"]
	if loc := rec.CreatedAt.Location(); loc != time.UTC {
		t.Fatalf("CreatedAt location = %v, want UTC", loc)
	}
	if loc := rec.UpdatedAt.Location(); loc != time.UTC {
		t.Fatalf("UpdatedAt location = %v, want UTC", loc)
	}
}

func TestSpawn_RollsBackOnRuntimeFailure(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	project := st.projects["mer"]
	project.Config.Env = map[string]string{"PROJECT_TOKEN": "runtime-secret"}
	st.projects["mer"] = project
	m.runtime = &fakeRuntime{createErr: errors.New("boom with runtime-secret")}
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer"}); err == nil || strings.Contains(err.Error(), "runtime-secret") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("Spawn error = %v, want redacted runtime failure", err)
	}
	if ws.destroyed != 1 {
		t.Fatal("workspace should roll back")
	}
	if rec, present := st.sessions["mer-1"]; present {
		t.Fatalf("seed row must be deleted before a runtime handle is live, got %+v", rec)
	}
}

func TestSpawn_AgentRuntimeEnvAugmenterReachesRuntime(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	agent := envAugmentingAgent{key: "AGENT_DATA_DIR", value: "agent"}
	m := New(Deps{
		Runtime:    rt,
		Agents:     singleAgent{agent: agent},
		Workspace:  &fakeWorkspace{path: "/ws/mer-1"},
		Store:      st,
		Messenger:  &fakeMessenger{},
		Lifecycle:  &fakeLCM{store: st},
		DataDir:    "/tallpine/data",
		LookPath:   func(string) (string, error) { return "/bin/true", nil },
		Executable: func() (string, error) { return "/daemon/tallpine", nil },
	})

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got, want := rt.lastCfg.Env["AGENT_DATA_DIR"], filepath.Join("/tallpine/data", "agent"); got != want {
		t.Fatalf("runtime env AGENT_DATA_DIR = %q, want %q", got, want)
	}
}

func TestSpawn_DefaultsBranchFromSessionID(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	s, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	// An empty SpawnConfig.Branch defaults to a unique per-session root branch
	// under a namespace that can also hold sibling PR branches.
	if got := st.sessions[s.ID].Metadata.Branch; got != "tp/mer-1/root" {
		t.Fatalf("default branch = %q, want tp/mer-1/root", got)
	}
	if !st.sessions[s.ID].AutoInjectReview {
		t.Fatal("automatic review injection must default to enabled")
	}
	if st.sessions[s.ID].AutoReviewEnabled {
		t.Fatal("auto review must default to disabled when project config does not enable it")
	}
}

func TestSpawn_FetchesDefaultBranchBeforeCreatingWorkerWorktree(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	cfg := testRoleAgents()
	cfg.DefaultBranch = "main"
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Config: cfg}
	var calls []string
	ws.sharedLog = &calls

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}

	if got, want := ws.fetches, []fetchDefaultBranchCall{{repoPath: "/repo/mer", remote: "origin", branch: "main", baseRef: "refs/remotes/origin/main"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fetches = %#v, want %#v", got, want)
	}
	if got, want := ws.lastCfg.BaseRef, "refs/remotes/origin/main"; got != want {
		t.Fatalf("create base ref = %q, want %q", got, want)
	}
	if got, want := calls[:2], []string{"FetchDefaultBranch:origin/main", "Create"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace call order = %#v, want %#v", calls, want)
	}
}

func TestSpawn_FetchesWorkspaceChildDefaultBranchesBeforeCreatingProject(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api", DefaultBranch: "release/2026"}}

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}
	want := []fetchDefaultBranchCall{
		{repoPath: "/repo/mer", remote: "origin", branch: "main", baseRef: "refs/remotes/origin/main"},
		{repoPath: filepath.Join("/repo/mer", "api"), remote: "origin", branch: "release/2026", baseRef: "refs/remotes/origin/release/2026"},
	}
	if !reflect.DeepEqual(ws.fetches, want) {
		t.Fatalf("fetches = %#v, want %#v", ws.fetches, want)
	}
	if got, want := ws.resolves[0], (resolveDefaultBranchCall{repoPath: "/repo/mer", configuredBranch: ""}); got != want {
		t.Fatalf("root resolution = %#v, want %#v", got, want)
	}
	if got, want := ws.lastProjectCfg.Repos[0].BaseRef, "refs/remotes/origin/release/2026"; got != want {
		t.Fatalf("child create base ref = %q, want %q", got, want)
	}
}

func TestSpawn_InfersEmptyWorkspaceChildDefaultBeforeFetchAndCreate(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	childPath := filepath.Join("/repo/mer", "api")
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	ws.resolved = map[string]ports.WorkspaceDefaultBranch{
		childPath: {Remote: "origin", Branch: "dev", BaseRef: "refs/remotes/origin/dev"},
	}

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}

	if got, want := ws.resolves[1], (resolveDefaultBranchCall{repoPath: childPath, configuredBranch: ""}); got != want {
		t.Fatalf("child resolution = %#v, want %#v", got, want)
	}
	if got, want := ws.fetches[1], (fetchDefaultBranchCall{repoPath: childPath, remote: "origin", branch: "dev", baseRef: "refs/remotes/origin/dev"}); got != want {
		t.Fatalf("child fetch = %#v, want %#v", got, want)
	}
	if got, want := ws.lastProjectCfg.Repos[0].BaseRef, "refs/remotes/origin/dev"; got != want {
		t.Fatalf("child create base ref = %q, want %q", got, want)
	}
	var persistedChildBase string
	for _, row := range st.worktrees["mer-1"] {
		if row.RepoName == "api" {
			persistedChildBase = row.BaseSHA
			break
		}
	}
	if got, want := persistedChildBase, "api-base"; got != want {
		t.Fatalf("persisted child BaseSHA = %q, want materializer result %q", got, want)
	}
}

func TestSpawn_SkipsNeedsInitWorkspaceChildrenDuringRefreshAndCreate(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	projectPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(projectPath, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectPath, "unborn", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: projectPath, Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{
		{Name: "api", RelativePath: "api", DefaultBranch: "main", GitStatus: domain.GitStatusReady},
		{Name: "unborn", RelativePath: "unborn", GitStatus: domain.GitStatusNeedsInit},
		{Name: "assets", RelativePath: "assets", GitStatus: domain.GitStatusNeedsInit},
	}

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}

	if got, want := len(ws.fetches), 2; got != want {
		t.Fatalf("fetch calls = %d, want root plus ready child (%d)", got, want)
	}
	if got, want := len(ws.lastProjectCfg.Repos), 1; got != want {
		t.Fatalf("materialized child configs = %d, want only ready child (%d)", got, want)
	}
	if got, want := ws.lastProjectCfg.Repos[0].Name, "api"; got != want {
		t.Fatalf("materialized child = %q, want %q", got, want)
	}
	if got, want := len(ws.lastProjectCfg.Assets), 1; got != want {
		t.Fatalf("materialized asset configs = %d, want %d", got, want)
	}
	if got, want := ws.lastProjectCfg.Assets[0].RelativePath, "assets"; got != want {
		t.Fatalf("materialized asset = %q, want %q", got, want)
	}
}

func TestRefreshDefaultBranchesUsesOneOverallFetchBudget(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	project := domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("child-%d", i)
		st.workspaceRepo["mer"] = append(st.workspaceRepo["mer"], domain.WorkspaceRepoRecord{Name: name, RelativePath: name, DefaultBranch: "main"})
	}
	m.defaultBranchRefreshTimeout = 25 * time.Millisecond
	ws.fetchFunc = func(ctx context.Context, _ string, _ ports.WorkspaceDefaultBranch) error {
		<-ctx.Done()
		return ctx.Err()
	}

	started := time.Now()
	baseRefs := m.refreshDefaultBranchesBestEffort(context.Background(), project)
	elapsed := time.Since(started)

	if elapsed > 250*time.Millisecond {
		t.Fatalf("workspace refresh took %s, want one shared 25ms budget", elapsed)
	}
	if got, want := len(ws.fetches), 6; got != want {
		t.Fatalf("fetch calls = %d, want root plus five children (%d)", got, want)
	}
	if got, want := len(baseRefs), 6; got != want {
		t.Fatalf("resolved base refs = %d, want root plus five children (%d)", got, want)
	}
}

func TestSpawn_FetchesQualifiedDefaultBranchRemote(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	cfg := testRoleAgents()
	cfg.DefaultBranch = "upstream/main"
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Config: cfg}
	ws.resolved = map[string]ports.WorkspaceDefaultBranch{
		"/repo/mer": {Remote: "upstream", Branch: "main", BaseRef: "refs/remotes/upstream/main"},
	}

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}

	if got, want := ws.resolves, []resolveDefaultBranchCall{{repoPath: "/repo/mer", configuredBranch: "upstream/main"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolutions = %#v, want %#v", got, want)
	}
	if got, want := ws.fetches, []fetchDefaultBranchCall{{repoPath: "/repo/mer", remote: "upstream", branch: "main", baseRef: "refs/remotes/upstream/main"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fetches = %#v, want %#v", got, want)
	}
	if got, want := ws.lastCfg.BaseRef, "refs/remotes/upstream/main"; got != want {
		t.Fatalf("create base ref = %q, want %q", got, want)
	}
}

func TestSpawn_SlashDefaultBranchWithoutKnownRemoteFetchesFromOrigin(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	cfg := testRoleAgents()
	cfg.DefaultBranch = "release/2026"
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Config: cfg}

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}

	if got, want := ws.resolves, []resolveDefaultBranchCall{{repoPath: "/repo/mer", configuredBranch: "release/2026"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolutions = %#v, want %#v", got, want)
	}
	if got, want := ws.fetches, []fetchDefaultBranchCall{{repoPath: "/repo/mer", remote: "origin", branch: "release/2026", baseRef: "refs/remotes/origin/release/2026"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fetches = %#v, want %#v", got, want)
	}
}

func TestSpawn_DefaultBranchFetchFailureDoesNotBlockWorkerSpawn(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Config: testRoleAgents()}
	ws.fetchErr = errors.New("network unavailable")

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}
	if len(ws.fetches) != 1 {
		t.Fatalf("fetches = %d, want 1", len(ws.fetches))
	}
	if ws.lastCfg.Branch != "tp/mer-1/root" {
		t.Fatalf("created branch = %q, want tp/mer-1/root", ws.lastCfg.Branch)
	}
}

func TestSpawn_DefaultsBranchUnderDevNamespaceForDevDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m, st, _, _ := newManager()
	m.dataDir = filepath.Join(home, ".tallpine", "dev", "data")

	worker, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[worker.ID].Metadata.Branch; got != "tp/dev/mer-1/root" {
		t.Fatalf("worker branch = %q, want tp/dev/mer-1/root", got)
	}

	orchestrator, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[orchestrator.ID].Metadata.Branch; got != "tp/dev/mer-orchestrator" {
		t.Fatalf("orchestrator branch = %q, want tp/dev/mer-orchestrator", got)
	}
}

func TestSpawn_ExplicitBranchBypassesDevNamespace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m, st, _, _ := newManager()
	m.dataDir = filepath.Join(home, ".tallpine", "dev", "data")

	s, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Branch: "tp/custom"})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[s.ID].Metadata.Branch; got != "tp/custom" {
		t.Fatalf("explicit branch = %q, want tp/custom", got)
	}
}

// TestSpawn_TUICodexEffortIsResolvedAndPersisted asserts a TUI codex spawn
// with an explicit effort override resolves and validates it against the model
// catalog and persists the resolved selection durably, so a later rebuild can
// keep the choice instead of reverting to the project default.
func TestSpawn_TUICodexEffortIsResolvedAndPersisted(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	agent := &recordingAgent{}
	m.agents = singleAgent{agent: agent}
	m.SetModelCatalog(tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "gpt-5.6-sol", IsDefault: true, Efforts: []string{"low", "medium", "high"}},
	}}})

	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID:      "mer",
		Kind:           domain.KindWorker,
		Harness:        domain.HarnessCodex,
		RequestedMode:  domain.SessionModeTUI,
		Prompt:         "do the work",
		AgentConfig:    ports.AgentConfig{Effort: "high"},
		EffortOverride: true,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("runtime creates = %d, want 1", rt.created)
	}
	if agent.lastConfig.Effort != "high" {
		t.Fatalf("launch effort = %q, want high", agent.lastConfig.Effort)
	}
	stored, ok := st.sessions[rec.ID]
	if !ok {
		t.Fatal("spawned session row missing")
	}
	if stored.Metadata.Effort != "high" {
		t.Fatalf("persisted effort = %q, want high (explicit override must be durable for a TUI rebuild)", stored.Metadata.Effort)
	}
}

// TestSpawnAndRestore_PinHookPATHToDaemonBinary covers the activity-tracking
// fix: the spawned session's PATH must put the daemon executable's directory
// first, so the bare `tallpine` in the workspace hook commands resolves to the
// daemon that installed them, not a foreign `tallpine` earlier on the user's PATH
// (e.g. the legacy TypeScript CLI, which has no `hooks` command and silently
// kills activity tracking).
func TestSpawnAndRestore_PinHookPATHToDaemonBinary(t *testing.T) {
	daemonExe := fakeDaemonExe(t)
	want := filepath.Dir(daemonExe) + string(os.PathListSeparator) + "/usr/bin"
	executable := func() (string, error) { return daemonExe, nil }

	cases := []struct {
		name   string
		launch func(m *Manager, st *fakeStore) error
	}{
		{
			name: "spawn",
			launch: func(m *Manager, _ *fakeStore) error {
				_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
				return err
			},
		},
		{
			name: "restore",
			launch: func(m *Manager, st *fakeStore) error {
				seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
				_, err := m.RestoreWithMode(ctx, "mer-1")
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", "/usr/bin")
			m, st, rt, _ := pathPinManager(executable)
			if err := tc.launch(m, st); err != nil {
				t.Fatal(err)
			}
			if got := rt.lastCfg.Env["PATH"]; got != want {
				t.Fatalf("runtime env PATH = %q, want %q", got, want)
			}
		})
	}
}

// TestSpawn_HookPATHPinUnavailable asserts the degraded path is loud, not
// silent: when the daemon executable cannot anchor `tallpine` resolution, PATH is
// left to the runtime's inherited default and a warning is logged.
func TestSpawn_HookPATHPinUnavailable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		executable func() (string, error)
	}{
		{"executable unresolvable", func() (string, error) { return "", errors.New("no exe") }},
		{"executable not named tallpine", func() (string, error) { return "/opt/aod/tallpine-daemon", nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, rt, logBuf := pathPinManager(tc.executable)
			if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
				t.Fatal(err)
			}
			if got, ok := rt.lastCfg.Env["PATH"]; ok {
				t.Fatalf("runtime env PATH = %q, want unset when the pin cannot be applied", got)
			}
			if !strings.Contains(logBuf.String(), "not pinned") {
				t.Fatalf("expected a 'not pinned' warning in the log, got %q", logBuf.String())
			}
		})
	}
}

// TestSpawn_ProjectPATHIsPinBase asserts a project's PATH override survives the
// pin as its base rather than being clobbered or clobbering: the daemon dir
// still comes first.
func TestValidateSpawnModelOnlyRejectsUnknownStaticChoices(t *testing.T) {
	t.Parallel()

	for _, harness := range []domain.AgentHarness{"claude-code", "codex", "kiro"} {
		if err := validateSpawnModel(harness, "agent-owned-model"); err != nil {
			t.Fatalf("%s dynamic model should be validated by the agent: %v", harness, err)
		}
	}
}

func TestSpawn_ProjectPATHIsPinBase(t *testing.T) {
	t.Parallel()

	daemonExe := fakeDaemonExe(t)
	m, st, rt, _ := pathPinManager(func() (string, error) { return daemonExe, nil })
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{
		Env:    map[string]string{"PATH": "/proj/bin"},
		Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode},
	}}
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}
	want := filepath.Dir(daemonExe) + string(os.PathListSeparator) + "/proj/bin"
	if got := rt.lastCfg.Env["PATH"]; got != want {
		t.Fatalf("runtime env PATH = %q, want %q", got, want)
	}
}

func TestSpawnAndRestore_PrependsResolvedBinaryAndNodeDirsToRuntimePATH(t *testing.T) {
	daemonExe := fakeDaemonExe(t)
	home := t.TempDir()
	binDir := filepath.Join(home, ".npm-global", "bin")
	nodeDir := filepath.Join(home, ".nvm", "versions", "node", "v22.23.1", "bin")
	agentBin := filepath.Join(binDir, "kimi")
	for _, path := range []string{
		agentBin,
		filepath.Join(home, ".nvm", "versions", "node", "v18.20.0", "bin", "node"),
		filepath.Join(nodeDir, "node"),
		filepath.Join(home, ".volta", "bin", "node"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		contents := "#!/bin/sh\n"
		if path == agentBin {
			contents = "#!/usr/bin/env node\n"
		}
		if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The daemon-dir pin stays at the HEAD: the agent binary is launched by
	// absolute path and does not need its directory first, but a bare `tallpine` in
	// the session must resolve to this daemon (see restorePinnedDir).
	want := strings.Join([]string{filepath.Dir(daemonExe), binDir, nodeDir, "/usr/bin"}, string(os.PathListSeparator))

	for _, operation := range []string{"spawn", "restore"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("PATH", "/usr/bin")
			t.Setenv("VOLTA_HOME", filepath.Join(home, ".volta"))
			t.Setenv("FNM_DIR", filepath.Join(home, ".fnm"))
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
			rt := &fakeRuntime{}
			agent := launchArgvAgent{argv: []string{agentBin}}
			m := New(Deps{
				Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{path: "/ws/mer-1"},
				Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
				LookPath: func(name string) (string, error) {
					if name == "node" {
						return "", exec.ErrNotFound
					}
					return agentBin, nil
				},
				Executable: func() (string, error) { return daemonExe, nil },
			})
			if operation == "spawn" {
				_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
				if err != nil {
					t.Fatalf("Spawn: %v", err)
				}
			} else {
				seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
				if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
					t.Fatalf("Restore: %v", err)
				}
			}
			if got := rt.lastCfg.Env["PATH"]; got != want {
				t.Fatalf("runtime env PATH = %q, want %q", got, want)
			}
		})
	}
}

// TestSpawn_LaunchBinaryDirDoesNotShadowDaemonTallpine is issue #3562: the agent CLI
// and a stale `tallpine` can live in the SAME directory (the legacy npm package
// installs `tallpine` into the same global bin the agent CLIs use). Prepending the
// launch binary's directory must not push the daemon-dir pin down, or that
// stale `tallpine` wins every bare `tallpine` inside the session.
func TestSpawn_LaunchBinaryDirDoesNotShadowDaemonTallpine(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	sharedBin := filepath.Join(t.TempDir(), "npm-global", "bin")
	agentBin := filepath.Join(sharedBin, "claude")
	daemonExe := fakeDaemonExe(t, "daemon")

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: launchArgvAgent{argv: []string{agentBin}}},
		Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath:   func(string) (string, error) { return agentBin, nil },
		Executable: func() (string, error) { return daemonExe, nil },
	})
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	want := strings.Join([]string{filepath.Dir(daemonExe), sharedBin, "/usr/bin"}, string(os.PathListSeparator))
	if got := rt.lastCfg.Env["PATH"]; got != want {
		t.Fatalf("runtime env PATH = %q, want %q", got, want)
	}
}

func TestSpawn_DoesNotAddNodeRuntimeForNativeBinary(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	home := t.TempDir()
	binDir := filepath.Join(home, "native", "bin")
	agentBin := filepath.Join(binDir, "agent")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentBin, []byte("native executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	nodeLookups := 0
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: launchArgvAgent{argv: []string{agentBin}}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(name string) (string, error) {
			if name == "node" {
				nodeLookups++
			}
			return agentBin, nil
		},
		Executable: func() (string, error) { return "/tallpine/bin/tallpine", nil },
	})
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if nodeLookups != 0 {
		t.Fatalf("node LookPath calls = %d, want 0 for native binary", nodeLookups)
	}
	want := strings.Join([]string{"/tallpine/bin", binDir, "/usr/bin"}, string(os.PathListSeparator))
	if got := rt.lastCfg.Env["PATH"]; got != want {
		t.Fatalf("runtime env PATH = %q, want %q", got, want)
	}
}

func TestSpawn_KeepsExplicitBranch(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	s, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/x"})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[s.ID].Metadata.Branch; got != "feature/x" {
		t.Fatalf("explicit branch = %q, want feature/x", got)
	}
}

func TestSpawn_ScratchUsesBranchlessWorkspace(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch, Config: testRoleAgents()}

	s, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "scratch", Kind: domain.KindWorker})
	if err != nil {
		t.Fatalf("Spawn scratch: %v", err)
	}
	if s.Metadata.Branch != "" {
		t.Fatalf("scratch session branch = %q, want empty", s.Metadata.Branch)
	}
	if ws.lastCfg.Branch != "" || ws.lastCfg.BaseBranch != "" {
		t.Fatalf("workspace branch/base = %q/%q, want empty", ws.lastCfg.Branch, ws.lastCfg.BaseBranch)
	}
	if rows := st.worktrees[s.ID]; len(rows) != 0 {
		t.Fatalf("scratch spawn must not write session_worktrees rows, got %#v", rows)
	}
}

func TestSpawn_StandaloneUsesBranchlessWorkspaceWithoutProjectLookup(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()

	s, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		Kind:    domain.KindWorker,
		Harness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("Spawn standalone: %v", err)
	}
	if s.ID != "standalone-1" || s.ProjectID != "" {
		t.Fatalf("standalone identity = %q project %q", s.ID, s.ProjectID)
	}
	if s.Metadata.Branch != "" || ws.lastCfg.Branch != "" || ws.lastCfg.BaseBranch != "" {
		t.Fatalf("standalone branch/session workspace = %q/%q/%q, want empty", s.Metadata.Branch, ws.lastCfg.Branch, ws.lastCfg.BaseBranch)
	}
	if rows := st.worktrees[s.ID]; len(rows) != 0 {
		t.Fatalf("standalone spawn must not write session_worktrees rows, got %#v", rows)
	}
}

func TestSpawn_ScratchRejectsExplicitBranchBeforeSessionRow(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch, Config: testRoleAgents()}

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "scratch", Kind: domain.KindWorker, Branch: "feature/x"})
	if !errors.Is(err, ErrScratchBranchUnsupported) {
		t.Fatalf("Spawn scratch explicit branch err = %v, want ErrScratchBranchUnsupported", err)
	}
	if len(st.sessions) != 0 {
		t.Fatalf("scratch explicit branch must not create a session row, got %d rows", len(st.sessions))
	}
}

// ---- SaveAndTeardownAll / RestoreAll tests ----

func TestOrchestratorWorkspaceBranchCollision(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		kind         domain.SessionKind
		explicit     bool
		wantFallback bool
	}{
		{name: "generated orchestrator branch", kind: domain.KindOrchestrator, wantFallback: true},
		{name: "explicit orchestrator branch", kind: domain.KindOrchestrator, explicit: true},
		{name: "worker branch", kind: domain.KindWorker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _, repo := newGitTaskPreparationManager(t)
			project := st.projects["mer"]
			branch := "tp/mer-orchestrator"
			occupied := filepath.Join(t.TempDir(), "occupied")
			runManagerGit(t, repo, "worktree", "add", "-b", branch, occupied, "main")
			runManagerGit(t, repo, "branch", branch+"-2", "main")
			dirtyPath := filepath.Join(occupied, "README.md")
			if err := os.WriteFile(dirtyPath, []byte("existing agent work\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg := ports.SpawnConfig{ProjectID: "mer", Kind: tc.kind}
			if tc.explicit {
				cfg.Branch = branch
			}
			ws, _, err := m.createSessionWorkspace(ctx, project, cfg, "mer-1", branch, nil)
			if tc.wantFallback {
				if err != nil {
					t.Fatal(err)
				}
				if ws.Branch != branch+"-3" || ws.Path == occupied {
					t.Fatalf("new workspace = %+v", ws)
				}
				reused, _, err := m.createSessionWorkspace(ctx, project, cfg, "mer-2", branch, nil)
				if err != nil || reused.Path != ws.Path || reused.Branch != ws.Branch {
					t.Fatalf("reuse workspace = %+v, err = %v", reused, err)
				}
			} else if !errors.Is(err, ports.ErrWorkspaceBranchCheckedOutElsewhere) {
				t.Fatalf("error = %v, want branch conflict", err)
			}
			if got := strings.TrimSpace(runManagerGit(t, occupied, "branch", "--show-current")); got != branch {
				t.Fatalf("occupied branch changed to %q", got)
			}
			if content, err := os.ReadFile(dirtyPath); err != nil || string(content) != "existing agent work\n" {
				t.Fatalf("existing work changed: %q, %v", content, err)
			}
		})
	}
}
