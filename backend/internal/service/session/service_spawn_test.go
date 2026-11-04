package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestCleanupMapsManagerResult: the service forwards both reclaimed and
// skipped sessions, with non-nil slices so the wire shape stays stable.
func TestCleanupMapsManagerResult(t *testing.T) {
	t.Parallel()

	svc := &Service{manager: &fakeCommander{}}
	out, err := svc.Cleanup(context.Background(), "mer")
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(out.Cleaned) != 1 || out.Cleaned[0] != "mer-1" {
		t.Fatalf("cleaned = %#v", out.Cleaned)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].SessionID != "mer-2" || out.Skipped[0].Reason != "workspace has uncommitted changes" {
		t.Fatalf("skipped = %#v", out.Skipped)
	}
}

func TestTeardownProjectKillsActiveSessionsThenCleansProject(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", IsTerminated: true}
	st.sessions["other-1"] = domain.SessionRecord{ID: "other-1", ProjectID: "other"}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if err := svc.TeardownProject(context.Background(), "mer"); err != nil {
		t.Fatalf("TeardownProject: %v", err)
	}
	if len(fc.killed) != 1 || fc.killed[0] != "mer-1" {
		t.Fatalf("killed = %#v, want only mer-1", fc.killed)
	}
	if len(fc.cleanupProjects) != 1 || fc.cleanupProjects[0] != "mer" {
		t.Fatalf("cleanup projects = %#v, want [mer]", fc.cleanupProjects)
	}
}

// Project removal must tear live sessions down in parallel: the per-session
// cost (agent/runtime shutdown, controller teardown) dominates and is
// independent, which is exactly what makes removing a many-session project
// slow when kills run one after another. Stalling one session's kill must not
// delay the others.
func TestTeardownProjectKillsActiveSessionsConcurrently(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer"}
	st.sessions["other-1"] = domain.SessionRecord{ID: "other-1", ProjectID: "other"}
	mer1Entered := make(chan struct{})
	mer2Killed := make(chan struct{})
	releaseMer1 := make(chan struct{})
	fc := &fakeCommander{killFunc: func(id domain.SessionID) {
		switch id {
		case "mer-1":
			close(mer1Entered)
			<-releaseMer1
		case "mer-2":
			close(mer2Killed)
		}
	}}
	svc := &Service{manager: fc, store: st}

	done := make(chan error, 1)
	go func() { done <- svc.TeardownProject(context.Background(), "mer") }()

	select {
	case <-mer1Entered:
	case <-time.After(5 * time.Second):
		t.Fatal("kill of mer-1 never started")
	}
	select {
	case <-mer2Killed:
		// mer-2 was killed while mer-1's teardown was still blocked.
	case <-time.After(5 * time.Second):
		t.Fatal("kill of mer-2 waited for mer-1's teardown to finish; kills are serial, not parallel")
	}
	close(releaseMer1)
	if err := <-done; err != nil {
		t.Fatalf("TeardownProject: %v", err)
	}

	fc.killMu.Lock()
	killed := append([]domain.SessionID(nil), fc.killed...)
	fc.killMu.Unlock()
	if len(killed) != 2 {
		t.Fatalf("killed = %#v, want mer-1 and mer-2", killed)
	}
	for _, id := range killed {
		if id != "mer-1" && id != "mer-2" {
			t.Fatalf("unexpected session killed: %s", id)
		}
	}
	if len(fc.cleanupProjects) != 1 || fc.cleanupProjects[0] != "mer" {
		t.Fatalf("cleanup projects = %#v, want [mer]", fc.cleanupProjects)
	}
}

func TestTeardownProjectStopsOnKillError(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	boom := errors.New("boom")
	fc := &fakeCommander{killErr: boom}
	svc := &Service{manager: fc, store: st}

	err := svc.TeardownProject(context.Background(), "mer")
	if !errors.Is(err, boom) {
		t.Fatalf("TeardownProject err = %v, want boom", err)
	}
	if len(fc.cleanupProjects) != 0 {
		t.Fatalf("cleanup projects = %#v, want none after kill failure", fc.cleanupProjects)
	}
}

func TestSpawnOrchestratorCleanRetiresActiveOrchestratorsBeforeSpawn(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	// Two active orchestrators plus an unrelated worker and a terminated
	// orchestrator that must be left alone.
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-3"] = domain.SessionRecord{ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker}
	st.sessions["mer-4"] = domain.SessionRecord{ID: "mer-4", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true}

	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", true, "", ""); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}

	if len(fc.retired) != 2 {
		t.Fatalf("retired = %v, want the two active orchestrators", fc.retired)
	}
	if len(fc.sent) != 2 {
		t.Fatalf("retire notices = %v, want the two active orchestrators", fc.sent)
	}
	if !fc.spawned || fc.killsAtSpawn != 2 {
		t.Fatalf("spawn must run after both retirements: spawned=%v retirementsAtSpawn=%d", fc.spawned, fc.killsAtSpawn)
	}
	if len(fc.killed) != 0 {
		t.Fatalf("interactive Kill must not be used for replacement: killed=%v", fc.killed)
	}
}

func TestSpawnOrchestratorCleanContinuesWhenRetireNoticeFails(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
	fc := &fakeCommander{sendErr: errors.New("pane closed")}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", true, "", ""); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if len(fc.retired) != 1 || fc.retired[0] != "mer-1" {
		t.Fatalf("retired = %v, want mer-1 despite retire notice failure", fc.retired)
	}
	if !fc.spawned {
		t.Fatal("replacement should still spawn when retire notice delivery fails")
	}
}

func TestSpawnOrchestratorCleanPreservesCancellationForReplacement(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fc := &fakeCommander{retireFunc: func(retireCtx context.Context, _ domain.SessionID) error {
		cancel()
		if err := retireCtx.Err(); err != nil {
			t.Fatalf("retirement must finish after request cancellation: %v", err)
		}
		return nil
	}}
	svc := &Service{manager: fc, store: st}
	if _, err := svc.SpawnOrchestrator(ctx, "mer", true, "", ""); err != nil {
		t.Fatal(err)
	}
	if fc.spawnCtx == nil || !errors.Is(fc.spawnCtx.Err(), context.Canceled) {
		t.Fatal("replacement spawn must receive the cancelled request context")
	}
}

// A clean replacement runs the retired orchestrator's Cleanup before the
// spawn extends the request's budget; the request used to die at its base
// budget while the Cleanup ran.
func TestSpawnOrchestratorCleanBudgetCoversTheRetiredOrchestratorsCleanup(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{PreRemove: []string{"make clean"}}}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
	ctx, _, release := ports.NewSpawnRequestContext(context.Background(), 30*time.Millisecond)
	defer release()
	var spawnErr error
	fc := &fakeCommander{
		retireFunc: func(context.Context, domain.SessionID) error {
			time.Sleep(80 * time.Millisecond)
			return nil
		},
		spawnHook: func(ctx context.Context) { spawnErr = ctx.Err() },
	}
	svc := &Service{manager: fc, store: st}
	if _, err := svc.SpawnOrchestrator(ctx, "mer", true, "", ""); err != nil {
		t.Fatal(err)
	}
	if spawnErr != nil {
		t.Fatalf("replacement spawn context Err() = %v, want live after the retirement's Cleanup", spawnErr)
	}
}

// The spawn's own extension, from its own start, is not capped by the
// orchestrator start's earlier one.
func TestSpawnOrchestratorLeavesTheSpawnsOwnBudgetExtensionUncapped(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	ctx, _, release := ports.NewSpawnRequestContext(context.Background(), 30*time.Millisecond)
	defer release()
	var spawnErr error
	fc := &fakeCommander{spawnHook: func(ctx context.Context) {
		ctx, cancel := ports.ExtendSpawnRequestBudget(ctx, 300*time.Millisecond)
		defer cancel()
		time.Sleep(100 * time.Millisecond)
		spawnErr = ctx.Err()
	}}
	svc := &Service{manager: fc, store: st}
	if _, err := svc.SpawnOrchestrator(ctx, "mer", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if spawnErr != nil {
		t.Fatalf("spawn context Err() = %v, want live within the spawn's own extension", spawnErr)
	}
}

func TestSpawnOrchestratorCleanPreservesPersistedMode(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Mode: domain.SessionModeChat, CreatedAt: time.Unix(100, 0).UTC(),
	}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", true, "", ""); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if fc.spawnedCfg.RequestedMode != domain.SessionModeChat {
		t.Fatalf("replacement mode = %q, want persisted chat", fc.spawnedCfg.RequestedMode)
	}
}

func TestSpawnOrchestratorCleanHonorsExplicitReplacementMode(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Mode: domain.SessionModeTUI, CreatedAt: time.Unix(100, 0).UTC(),
	}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", true, domain.SessionModeChat, ""); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if fc.spawnedCfg.RequestedMode != domain.SessionModeChat {
		t.Fatalf("replacement mode = %q, want explicit chat", fc.spawnedCfg.RequestedMode)
	}
}

func TestSpawnOrchestratorUsesExplicitModeForNewProjectOrchestrator(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", false, domain.SessionModeChat, ""); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if !fc.spawnedCfg.Async {
		t.Fatal("orchestrator startup must publish a session before Chat launches")
	}
	if fc.spawnedCfg.RequestedMode != domain.SessionModeChat {
		t.Fatalf("requested mode = %q, want chat", fc.spawnedCfg.RequestedMode)
	}
}

func TestSpawnOrchestratorPassesApprovalOverrideToSpawn(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", false, domain.SessionModeChat, domain.PermissionModeBypassPermissions); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if fc.spawnedCfg.AgentConfig.Permissions != domain.PermissionModeBypassPermissions {
		t.Fatalf("spawn permissions = %q, want bypass", fc.spawnedCfg.AgentConfig.Permissions)
	}
}

func TestSpawnOrchestratorCleanRetireNoticeIsBranchNeutral(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch}
	st.sessions["scratch-1"] = domain.SessionRecord{ID: "scratch-1", ProjectID: "scratch", Kind: domain.KindOrchestrator}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "scratch", true, "", ""); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if len(fc.sentMessages) != 1 {
		t.Fatalf("retire messages = %d, want 1", len(fc.sentMessages))
	}
	if strings.Contains(strings.ToLower(fc.sentMessages[0]), "branch") {
		t.Fatalf("retire notice must be branch-neutral, got %q", fc.sentMessages[0])
	}
}

// TestSpawnUnknownProjectReturns404 covers Bug 1: an HTTP spawn for an
// unregistered projectId must surface PROJECT_NOT_FOUND (apierr.NotFound)
// BEFORE any session row is created, so no orphan terminated row is left
// behind under `--include-terminated`.
func TestSpawnUnknownProjectReturns404(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	_, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "ghost", Kind: domain.KindWorker})
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "PROJECT_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr.NotFound PROJECT_NOT_FOUND", err)
	}
	if fc.spawned {
		t.Fatal("manager.Spawn must NOT be invoked for an unknown project")
	}
}

func TestSpawnStandaloneWorkerDoesNotRequireProject(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	fc := &fakeCommander{spawnRecord: domain.SessionRecord{
		ID: "standalone-1", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
	}}
	svc := &Service{manager: fc, store: st}

	session, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{
		Kind:    domain.KindWorker,
		Harness: domain.HarnessCodex,
		Prompt:  "Research release options",
	})
	if err != nil {
		t.Fatalf("Spawn standalone: %v", err)
	}
	if session.ID != "standalone-1" || session.ProjectID != "" {
		t.Fatalf("standalone session = %#v", session)
	}
	if fc.spawnedCfg.ProjectID != "" || fc.spawnedCfg.Kind != domain.KindWorker {
		t.Fatalf("manager config = %#v", fc.spawnedCfg)
	}
}

func TestSpawnStandaloneRejectsProjectFeatures(t *testing.T) {
	t.Parallel()

	for _, cfg := range []ports.SpawnConfig{
		{Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex},
		{Kind: domain.KindWorker, Harness: domain.HarnessCodex, Branch: "feature/x"},
		{Kind: domain.KindWorker, Harness: domain.HarnessCodex, IssueID: "42"},
		{Kind: domain.KindWorker},
	} {
		fc := &fakeCommander{}
		svc := &Service{manager: fc, store: newFakeStore()}
		if _, _, _, err := svc.Spawn(context.Background(), cfg); err == nil {
			t.Fatalf("Spawn(%#v) succeeded, want validation error", cfg)
		}
		if fc.spawned {
			t.Fatalf("manager called for invalid standalone config %#v", cfg)
		}
	}
}

func TestSpawnBlocksDefinitelyMissingHarnessBeforeManager(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	fc := &fakeCommander{}
	readiness := &fakeAgentReadiness{snapshot: domain.AgentReadinessSnapshot{
		ID: "codex", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationNotInstalled},
		Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationUnknown},
	}}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, AgentReadiness: readiness})

	_, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: "codex"})
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Code != "AGENT_BINARY_NOT_FOUND" {
		t.Fatalf("Spawn error = %v, want AGENT_BINARY_NOT_FOUND", err)
	}
	if fc.spawnCalls != 0 {
		t.Fatal("manager.Spawn ran before a definitely-missing harness was blocked")
	}
	if readiness.calls != 1 || readiness.purpose != domain.AgentReadinessPurposeLaunch {
		t.Fatalf("readiness call = count %d purpose %q", readiness.calls, readiness.purpose)
	}
}

func TestSpawnProjectClaudeGatewayTreatsGlobalUnauthorizedAsAdvisory(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{
		ID: "mer",
		Config: domain.ProjectConfig{Env: map[string]string{
			"ANTHROPIC_BASE_URL": "https://gateway.example",
			"ANTHROPIC_API_KEY":  "project-fixture-key",
		}},
	}
	fc := &fakeCommander{}
	readiness := &fakeAgentReadiness{snapshot: domain.AgentReadinessSnapshot{
		ID: "claude-code", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
		Authentication: domain.AgentAuthenticationObservation{
			State: domain.AgentAuthenticationUnauthorized, Freshness: domain.AgentReadinessFresh,
		},
	}}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, AgentReadiness: readiness})

	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if fc.spawnCalls != 1 {
		t.Fatalf("manager.Spawn calls = %d, want project-aware launch to remain authoritative", fc.spawnCalls)
	}
}

func TestSpawnStillRejectsFreshUnauthorizedCodexAccount(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	fc := &fakeCommander{}
	readiness := &fakeAgentReadiness{snapshot: domain.AgentReadinessSnapshot{
		ID: "codex", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
		Authentication: domain.AgentAuthenticationObservation{
			State: domain.AgentAuthenticationUnauthorized, Freshness: domain.AgentReadinessFresh,
		},
	}}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, AgentReadiness: readiness})

	_, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex})
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Code != "CODEX_ACCOUNT_AUTH_UNVERIFIED" {
		t.Fatalf("Spawn error = %v, want CODEX_ACCOUNT_AUTH_UNVERIFIED", err)
	}
	if fc.spawnCalls != 0 {
		t.Fatalf("manager.Spawn calls = %d, want fresh unauthorized Codex account blocked", fc.spawnCalls)
	}
}

// The readiness gate reads the computer's own Codex login. A task that will run
// on a saved account does not use that login, so its state is not a reason to
// refuse; the launch checks the account it runs on.
func TestSpawnOnASavedCodexAccountIgnoresTheComputersLogin(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	fc := &fakeCommander{}
	readiness := &fakeAgentReadiness{snapshot: domain.AgentReadinessSnapshot{
		ID: "codex", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
		Authentication: domain.AgentAuthenticationObservation{
			State: domain.AgentAuthenticationUnauthorized, Freshness: domain.AgentReadinessFresh,
		},
	}}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, AgentReadiness: readiness})
	var asked []string
	svc.SetComputerLoginCheck(func(_ context.Context, harness domain.AgentHarness, accountID string) bool {
		asked = append(asked, string(harness)+":"+accountID)
		return false
	})

	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if fc.spawnCalls != 1 || len(asked) != 1 || asked[0] != "codex:" {
		t.Fatalf("spawn calls = %d, asked %v; want the saved default to launch", fc.spawnCalls, asked)
	}
}

func TestSpawnInvalidatesReadinessAfterTypedLaunchFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		err              error
		wantInstallation bool
		wantAuth         bool
	}{
		{name: "binary missing", err: ports.ErrAgentBinaryNotFound, wantInstallation: true},
		{name: "auth required", err: ports.ErrChatAuthRequired, wantAuth: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
			fc := &fakeCommander{spawnErr: tt.err}
			readiness := &fakeAgentReadiness{snapshot: domain.AgentReadinessSnapshot{
				ID: "codex", Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
				Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationAuthorized},
			}}
			svc := NewWithDeps(Deps{Manager: fc, Store: st, AgentReadiness: readiness})

			_, _, _, _ = svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: "codex"})
			if got := len(readiness.installationInvalidated) == 1; got != tt.wantInstallation {
				t.Fatalf("installation invalidated = %v, want %v", got, tt.wantInstallation)
			}
			if got := len(readiness.authInvalidated) == 1; got != tt.wantAuth {
				t.Fatalf("auth invalidated = %v, want %v", got, tt.wantAuth)
			}
			if len(readiness.rechecks) != 1 || readiness.rechecks[0] != "codex" {
				t.Fatalf("rechecks = %#v, want codex", readiness.rechecks)
			}
		})
	}
}

func TestSpawnEnrichesIssueContextFromTracker(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo.git"}
	fc := &fakeCommander{}
	tracker := &fakeTracker{issue: domain.Issue{
		ID:        domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "acme/repo#42"},
		Title:     "Fix generated prompts",
		Body:      "Prompt files should include standing instructions.",
		State:     domain.IssueInProgress,
		URL:       "https://github.com/acme/repo/issues/42",
		Labels:    []string{"bug", "prompts"},
		Assignees: []string{"dev"},
	}}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Tracker: tracker})

	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, IssueID: "42"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(tracker.ids) != 1 || tracker.ids[0].Provider != domain.TrackerProviderGitHub || tracker.ids[0].Native != "acme/repo#42" {
		t.Fatalf("tracker ids = %+v, want github acme/repo#42", tracker.ids)
	}
	issueContext := fc.spawnedCfg.IssueContext
	for _, want := range []string{
		"Issue: acme/repo#42",
		"Title: Fix generated prompts",
		"State: in_progress",
		"URL: https://github.com/acme/repo/issues/42",
		"Labels: bug, prompts",
		"Assignees: dev",
		"Body:\nPrompt files should include standing instructions.",
	} {
		if !strings.Contains(issueContext, want) {
			t.Fatalf("IssueContext missing %q:\n%s", want, issueContext)
		}
	}
}

func TestSpawnIssueContextFetchFailureFallsBack(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	fc := &fakeCommander{}
	tracker := &fakeTracker{err: errors.New("tracker unavailable")}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Tracker: tracker})

	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, IssueID: "42"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(tracker.ids) != 1 {
		t.Fatalf("tracker calls = %d, want 1", len(tracker.ids))
	}
	if fc.spawnedCfg.IssueContext != "" {
		t.Fatalf("IssueContext = %q, want fallback empty context", fc.spawnedCfg.IssueContext)
	}
}

// TestSpawnPreservesIssueIDWhenTrackerIsNil covers the issue #2685 boundary: when
// the daemon wiring cannot build a GitHub tracker (no token), it hands the session
// service a true-nil ports.Tracker. Spawn must still create the session, preserve
// IssueID, and skip only the GitHub issue-context enrichment — not panic on a
// typed-nil tracker the way the pre-fix wiring did.
func TestSpawnPreservesIssueIDWhenTrackerIsNil(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	fc := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Tracker: nil})

	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, IssueID: "107"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if fc.spawnedCfg.IssueID != "107" {
		t.Fatalf("IssueID = %q, want 107 preserved", fc.spawnedCfg.IssueID)
	}
	if fc.spawnedCfg.IssueContext != "" {
		t.Fatalf("IssueContext = %q, want empty (no tracker enrichment)", fc.spawnedCfg.IssueContext)
	}
}

func TestSpawnIssueContextSkipsUnresolvableIssueRef(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	fc := &fakeCommander{}
	tracker := &fakeTracker{}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Tracker: tracker})

	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, IssueID: "not-an-issue"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(tracker.ids) != 0 {
		t.Fatalf("tracker calls = %d, want 0", len(tracker.ids))
	}
	if fc.spawnedCfg.IssueContext != "" {
		t.Fatalf("IssueContext = %q, want empty", fc.spawnedCfg.IssueContext)
	}
}

// TestSpawnIssueContextRoutesGitLabProviderToGitLabTracker covers the
// GitLab SCM-origin routing: when the project's SCM origin resolves to
// GitLab, trackerIDForIssue constructs a GitLab TrackerID (not a GitHub one)
// and the multi-tracker dispatches it to the GitLab adapter. The tracker IS
// called — the old behavior (skip, no GitHub fallback) is replaced by
// correct GitLab routing. fakeSCM.ParseRepository routes gitlab.com to
// provider "gitlab" via providerKey.
func TestSpawnIssueContextRoutesGitLabProviderToGitLabTracker(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://gitlab.com/acme/repo.git"}
	fc := &fakeCommander{}
	tracker := &fakeTracker{issue: domain.Issue{
		ID:    domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "acme/repo#42"},
		Title: "GitLab issue",
		Body:  "This should appear in a GitLab project's prompt.",
		State: domain.IssueOpen,
		URL:   "https://gitlab.com/acme/repo/-/issues/42",
	}}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Tracker: tracker, SCM: fakeSCM{}})

	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, IssueID: "42"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// The tracker must be called with a GitLab TrackerID — not skipped.
	if len(tracker.ids) != 1 {
		t.Fatalf("tracker calls = %d, want 1 (GitLab project must route to GitLab tracker)", len(tracker.ids))
	}
	if tracker.ids[0].Provider != domain.TrackerProviderGitLab {
		t.Fatalf("tracker id provider = %q, want %q", tracker.ids[0].Provider, domain.TrackerProviderGitLab)
	}
	if tracker.ids[0].Native != "acme/repo#42" {
		t.Fatalf("tracker id native = %q, want %q", tracker.ids[0].Native, "acme/repo#42")
	}
	// Issue context must be enriched with GitLab issue content.
	if fc.spawnedCfg.IssueContext == "" {
		t.Fatal("IssueContext empty, want GitLab issue enrichment")
	}
	for _, want := range []string{
		"Issue: acme/repo#42",
		"Title: GitLab issue",
		"State: open",
		"URL: https://gitlab.com/acme/repo/-/issues/42",
		"Body:",
	} {
		if !strings.Contains(fc.spawnedCfg.IssueContext, want) {
			t.Fatalf("IssueContext missing %q:\n%s", want, fc.spawnedCfg.IssueContext)
		}
	}
}

// TestSpawnIssueContextFromGitLabTracker verifies end-to-end enrichment when
// the issue ID is a full GitLab issue URL
// (https://gitlab.com/owner/repo/-/issues/N). The URL is parsed to the native
// "owner/repo#N" form and dispatched to the GitLab tracker adapter.
func TestSpawnIssueContextFromGitLabTracker(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://gitlab.com/acme/repo.git"}
	fc := &fakeCommander{}
	tracker := &fakeTracker{issue: domain.Issue{
		ID:        domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "acme/repo#42"},
		Title:     "Fix GitLab CI",
		Body:      "Pipeline is broken.",
		State:     domain.IssueInProgress,
		URL:       "https://gitlab.com/acme/repo/-/issues/42",
		Labels:    []string{"bug", "ci"},
		Assignees: []string{"dev"},
	}}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Tracker: tracker, SCM: fakeSCM{}})

	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, IssueID: "https://gitlab.com/acme/repo/-/issues/42"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(tracker.ids) != 1 {
		t.Fatalf("tracker calls = %d, want 1", len(tracker.ids))
	}
	if tracker.ids[0].Provider != domain.TrackerProviderGitLab {
		t.Fatalf("tracker id provider = %q, want %q", tracker.ids[0].Provider, domain.TrackerProviderGitLab)
	}
	if tracker.ids[0].Native != "acme/repo#42" {
		t.Fatalf("tracker id native = %q, want %q", tracker.ids[0].Native, "acme/repo#42")
	}
	if tracker.ids[0].Host != "" {
		t.Fatalf("tracker id host = %q, want empty (gitlab.com zero value)", tracker.ids[0].Host)
	}
	issueContext := fc.spawnedCfg.IssueContext
	for _, want := range []string{
		"Issue: acme/repo#42",
		"Title: Fix GitLab CI",
		"State: in_progress",
		"URL: https://gitlab.com/acme/repo/-/issues/42",
		"Labels: bug, ci",
		"Assignees: dev",
		"Body:",
		"Pipeline is broken.",
	} {
		if !strings.Contains(issueContext, want) {
			t.Fatalf("IssueContext missing %q:\n%s", want, issueContext)
		}
	}
}

// TestSpawnIssueContextFromSelfManagedGitLabTracker verifies that a
// self-managed GitLab URL (https://gitlab.internal/...) produces a
// TrackerID with Host set to "gitlab.internal" and is dispatched to the
// GitLab tracker.
func TestSpawnIssueContextFromSelfManagedGitLabTracker(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://gitlab.internal/acme/repo.git"}
	fc := &fakeCommander{}
	tracker := &fakeTracker{issue: domain.Issue{
		ID:    domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "acme/repo#42", Host: "gitlab.internal"},
		Title: "Self-managed issue",
		Body:  "Body text.",
		State: domain.IssueOpen,
		URL:   "https://gitlab.internal/acme/repo/-/issues/42",
	}}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Tracker: tracker, SCM: fakeSCM{}})

	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, IssueID: "https://gitlab.internal/acme/repo/-/issues/42"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(tracker.ids) != 1 {
		t.Fatalf("tracker calls = %d, want 1", len(tracker.ids))
	}
	if tracker.ids[0].Provider != domain.TrackerProviderGitLab {
		t.Fatalf("tracker id provider = %q, want %q", tracker.ids[0].Provider, domain.TrackerProviderGitLab)
	}
	if tracker.ids[0].Native != "acme/repo#42" {
		t.Fatalf("tracker id native = %q, want %q", tracker.ids[0].Native, "acme/repo#42")
	}
	if tracker.ids[0].Host != "gitlab.internal" {
		t.Fatalf("tracker id host = %q, want %q", tracker.ids[0].Host, "gitlab.internal")
	}
}

// A spawn whose request budget runs out before it extends it (its project's
// Setup steps) timed out; it is not a cancellation by the client.
func TestSpawnReportsTimeoutWhenTheBaseRequestBudgetRunsOut(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{spawnWaitsForCtx: true}, Store: st, Clock: func() time.Time { return time.Unix(1700000000, 0).UTC() }})
	ctx, _, release := ports.NewSpawnRequestContext(context.Background(), 20*time.Millisecond)
	defer release()

	_, _, _, err := svc.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex})
	var e *apierr.Error
	if !errors.As(err, &e) || e.Code != "SPAWN_TIMEOUT" {
		t.Fatalf("Spawn error = %v, want SPAWN_TIMEOUT", err)
	}
	for _, hint := range []string{"heavy load", "TALLPINE_SPAWN_TIMEOUT", "TALLPINE_TMUX_COMMAND_TIMEOUT"} {
		if !strings.Contains(e.Message, hint) {
			t.Fatalf("SPAWN_TIMEOUT message %q missing hint %q", e.Message, hint)
		}
	}
}

// TestSpawnOrchestratorUnknownProjectReturns404 is the orchestrator-side guard
// for Bug 1: same pre-validation, same typed envelope.
func TestSpawnOrchestratorUnknownProjectReturns404(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	_, err := svc.SpawnOrchestrator(context.Background(), "ghost", false, "", "")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "PROJECT_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr.NotFound PROJECT_NOT_FOUND", err)
	}
	if fc.spawned {
		t.Fatal("manager.Spawn must NOT be invoked for an unknown project")
	}
}

func TestSpawnGenericOrchestratorReturnsExistingActiveSession(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Harness:   domain.HarnessCodex,
	}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	got, promptBytes, systemPromptBytes, err := svc.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID:   "mer",
		Kind:        domain.KindOrchestrator,
		Harness:     domain.HarnessClaudeCode,
		Prompt:      "start another orchestrator",
		DisplayName: "duplicate",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got.ID != "mer-orch" {
		t.Fatalf("returned id = %q, want existing orchestrator mer-orch", got.ID)
	}
	if promptBytes != 0 || systemPromptBytes != 0 {
		t.Fatalf("prompt sizes = (%d, %d), want zero for reused session", promptBytes, systemPromptBytes)
	}
	if fc.spawned {
		t.Fatal("manager.Spawn must not be called when an active orchestrator already exists")
	}
}

// A scheduled orchestrator must never be falsely linked to an unrelated
// interactive orchestrator. It stays retryable until the active one exits.
func TestSpawnAutomationOrchestratorConflictsWithUnrelatedActiveSession(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-orch"] = domain.SessionRecord{ID: "mer-orch", ProjectID: "mer", Kind: domain.KindOrchestrator}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}
	runID := domain.AutomationRunID("run-1")

	_, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, AutomationRunID: &runID})
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Kind != apierr.KindConflict || apiError.Code != "ORCHESTRATOR_ALREADY_ACTIVE" {
		t.Fatalf("Spawn error = %v, want ORCHESTRATOR_ALREADY_ACTIVE conflict", err)
	}
	if fc.spawned {
		t.Fatal("manager.Spawn must not run while another orchestrator is active")
	}
}

func TestSpawnGenericOrchestratorAllowsReplacementAfterTermination(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-old"] = domain.SessionRecord{
		ID:           "mer-old",
		ProjectID:    "mer",
		Kind:         domain.KindOrchestrator,
		IsTerminated: true,
	}
	fc := &fakeCommander{spawnRecord: domain.SessionRecord{
		ID:        "mer-new",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Harness:   domain.HarnessClaudeCode,
	}}
	svc := &Service{manager: fc, store: st}
	cfg := ports.SpawnConfig{
		ProjectID:   "mer",
		Kind:        domain.KindOrchestrator,
		Harness:     domain.HarnessClaudeCode,
		Branch:      "feature/orchestrator",
		Prompt:      "coordinate this project",
		DisplayName: "coordinator",
	}

	got, _, _, err := svc.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got.ID != "mer-new" {
		t.Fatalf("returned id = %q, want replacement mer-new", got.ID)
	}
	if fc.spawnCalls != 1 {
		t.Fatalf("manager.Spawn calls = %d, want 1", fc.spawnCalls)
	}
	if fc.spawnedCfg.ProjectID != cfg.ProjectID ||
		fc.spawnedCfg.Kind != cfg.Kind ||
		fc.spawnedCfg.Harness != cfg.Harness ||
		fc.spawnedCfg.Branch != cfg.Branch ||
		fc.spawnedCfg.Prompt != cfg.Prompt ||
		fc.spawnedCfg.DisplayName != cfg.DisplayName {
		t.Fatalf("spawn config = %#v, want request config %#v", fc.spawnedCfg, cfg)
	}
}

func TestSpawnGenericWorkerUnaffectedByActiveOrchestrator(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
	}
	fc := &fakeCommander{spawnRecord: domain.SessionRecord{
		ID:        "mer-worker",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
	}}
	svc := &Service{manager: fc, store: st}

	got, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got.ID != "mer-worker" || fc.spawnCalls != 1 {
		t.Fatalf("worker spawn = %#v, manager.Spawn calls = %d", got, fc.spawnCalls)
	}
}

func TestSpawnGenericOrchestratorSerializesConcurrentRequests(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	fc := &fakeCommander{}
	fc.spawnFunc = func(cfg ports.SpawnConfig) domain.SessionRecord {
		rec := domain.SessionRecord{
			ID:        "mer-orch",
			ProjectID: cfg.ProjectID,
			Kind:      domain.KindOrchestrator,
			Harness:   cfg.Harness,
		}
		st.sessions[rec.ID] = rec
		return rec
	}
	svc := &Service{manager: fc, store: st}
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex}

	start := make(chan struct{})
	results := make(chan domain.Session, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			session, _, _, err := svc.Spawn(context.Background(), cfg)
			results <- session
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Spawn: %v", err)
		}
	}
	for session := range results {
		if session.ID != "mer-orch" {
			t.Fatalf("returned id = %q, want mer-orch", session.ID)
		}
	}
	if fc.spawnCalls != 1 {
		t.Fatalf("manager.Spawn calls = %d, want 1", fc.spawnCalls)
	}
	if len(st.sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(st.sessions))
	}
}

// TestSpawnOrchestratorNoCleanReturnsExistingWhenActiveExists is the RED test
// for the idempotency fix: when an active orchestrator already exists and
// clean=false, SpawnOrchestrator must return that orchestrator without minting
// a second one. Before the fix this test fails because a duplicate is spawned.
func TestSpawnOrchestratorNoCleanReturnsExistingWhenActiveExists(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	// Pre-load an active orchestrator.
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}

	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	got, err := svc.SpawnOrchestrator(context.Background(), "mer", false, "", "")
	if err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	// Must return the existing orchestrator, not a newly minted one.
	if got.ID != "mer-1" {
		t.Fatalf("returned id = %q, want existing orchestrator mer-1", got.ID)
	}
	// Must NOT have called manager.Spawn (no duplicate created).
	if fc.spawned {
		t.Fatal("manager.Spawn must NOT be called when an active orchestrator already exists")
	}
	// Must NOT have killed anything.
	if len(fc.killed) != 0 {
		t.Fatalf("no kills expected with clean=false, got %v", fc.killed)
	}
	// Exactly one session in the store (no duplicate).
	if len(st.sessions) != 1 {
		t.Fatalf("session count = %d, want 1 (no duplicate)", len(st.sessions))
	}
}

// TestSpawnOrchestratorNoCleanSpawnsWhenNoneExists: clean=false spawns a new
// orchestrator when no active one exists for the project.
func TestSpawnOrchestratorNoCleanSpawnsWhenNoneExists(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	// No active orchestrator present.

	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	got, err := svc.SpawnOrchestrator(context.Background(), "mer", false, "", "")
	if err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if !fc.spawned {
		t.Fatal("manager.Spawn must be called when no active orchestrator exists")
	}
	if len(fc.killed) != 0 {
		t.Fatalf("no kills expected with clean=false, got %v", fc.killed)
	}
	if got.ID == "" {
		t.Fatal("returned session must have an id")
	}
}

func TestSpawnOrchestratorVerifiesReplacementHarness(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{
		ID:     "mer",
		Config: domain.ProjectConfig{Orchestrator: domain.RoleOverride{Harness: domain.HarnessCodex}},
	}
	fc := &fakeCommander{
		spawnRecord: domain.SessionRecord{
			ID:        "mer-9",
			ProjectID: "mer",
			Kind:      domain.KindOrchestrator,
			Harness:   domain.HarnessClaudeCode,
			Metadata:  domain.SessionMetadata{Branch: "tp/mer-orchestrator"},
		},
	}
	svc := &Service{manager: fc, store: st}

	_, err := svc.SpawnOrchestrator(context.Background(), "mer", false, "", "")
	if err == nil || !strings.Contains(err.Error(), `uses harness "claude-code", want "codex"`) {
		t.Fatalf("SpawnOrchestrator err = %v, want harness verification failure", err)
	}
}

func TestDelegateTaskPassesAttachmentsToSpawnConfig(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	fc := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: fc, Store: st})
	// This test only inspects the worker spawn. Keep asynchronous title
	// refinement from issuing a second Spawn against the recording fake.
	svc.runBackground = func(func()) {}

	_, err := svc.DelegateTask(context.Background(), DelegateTaskInput{
		ProjectID:      "mer",
		Brief:          "Use the attached image.",
		RequestedAgent: domain.HarnessCodex,
		Attachments: []ports.SpawnAttachment{
			{Ext: ".png", Data: []byte{1, 2, 3}},
		},
	})
	if err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if !fc.spawned {
		t.Fatal("DelegateTask did not call Spawn")
	}
	if fc.spawnedCfg.ProjectID != "mer" || fc.spawnedCfg.Kind != domain.KindWorker {
		t.Fatalf("spawned cfg identity = %#v", fc.spawnedCfg)
	}
	if fc.spawnedCfg.Harness != domain.HarnessCodex || fc.spawnedCfg.Prompt != "Use the attached image." {
		t.Fatalf("spawned cfg fields = %#v", fc.spawnedCfg)
	}
	if len(fc.spawnedCfg.Attachments) != 1 {
		t.Fatalf("attachments = %#v, want one", fc.spawnedCfg.Attachments)
	}
	if got := fc.spawnedCfg.Attachments[0]; got.Ext != ".png" || string(got.Data) != "\x01\x02\x03" {
		t.Fatalf("attachment = %#v, want decoded png", got)
	}
}
