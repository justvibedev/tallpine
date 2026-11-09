package sessionmanager

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type fakeGitCredentials struct {
	env      []string
	projects []domain.ProjectID
}

func (f *fakeGitCredentials) ProjectGitEnv(_ context.Context, project domain.ProjectID) []string {
	f.projects = append(f.projects, project)
	return f.env
}

func TestSpawnRefreshFetchesWithProjectAccount(t *testing.T) {
	m, _, _, ws := newManager()
	creds := &fakeGitCredentials{env: []string{"GIT_MARK=1"}}
	m.SetProjectGitCredentials(creds)
	project := domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Config: testRoleAgents()}
	m.refreshDefaultBranchesBestEffort(context.Background(), project)
	if len(ws.fetchEnvs) != 1 || len(ws.fetchEnvs[0]) != 1 || ws.fetchEnvs[0][0] != "GIT_MARK=1" {
		t.Fatalf("fetch env = %v, want the project's", ws.fetchEnvs)
	}
	if len(creds.projects) != 1 || creds.projects[0] != "mer" {
		t.Fatalf("credentials asked for %v, want mer", creds.projects)
	}
}

// A workspace's child repos can belong to another owner, so the project's
// account must not replace the login that reaches them.
func TestSpawnRefreshDoesNotUseProjectAccountForChildRepos(t *testing.T) {
	m, st, _, ws := newManager()
	m.SetProjectGitCredentials(&fakeGitCredentials{env: []string{"GIT_MARK=1"}})
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "child", RelativePath: "child", DefaultBranch: "main"}}
	project := domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	m.refreshDefaultBranchesBestEffort(context.Background(), project)
	if len(ws.fetches) != 2 {
		t.Fatalf("fetches = %d, want root and child", len(ws.fetches))
	}
	for i, call := range ws.fetches {
		wantEnv := call.repoPath == "/repo/mer"
		if got := len(ws.fetchEnvs[i]) > 0; got != wantEnv {
			t.Fatalf("%s: has env = %v, want %v", call.repoPath, got, wantEnv)
		}
	}
}

func TestSpawnRefreshWithoutCredentialsKeepsMachineLogin(t *testing.T) {
	m, _, _, ws := newManager()
	m.refreshDefaultBranchesBestEffort(context.Background(), domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Config: testRoleAgents()})
	if len(ws.fetchEnvs) != 1 || ws.fetchEnvs[0] != nil {
		t.Fatalf("fetch env = %v, want nil", ws.fetchEnvs)
	}
}

// The audit reported orchestrators failing with the wrong account. A pinned
// project's orchestrator launches through the same env as a worker, so its
// `gh` and `git` run as the project's account (the machine's git helper is
// `gh auth git-credential`, which honours GH_TOKEN).
func TestSpawnOrchestratorGetsProjectAccountToken(t *testing.T) {
	m := githubEnvManager(t, &fakeGitHubTokens{token: "orchestrator-token"})
	rt := m.runtime.(*fakeRuntime)
	if _, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if got := rt.lastCfg.Env["GH_TOKEN"]; got != "orchestrator-token" {
		t.Fatal("orchestrator launched without the project's GitHub account")
	}
}

func TestOrchestratorWorktreeRefreshGetsProjectAccount(t *testing.T) {
	ctx := context.Background()
	m, _, _, ws := newManager()
	m.SetProjectGitCredentials(&fakeGitCredentials{env: []string{"GIT_MARK=1"}})
	project := domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rec := domain.SessionRecord{ID: "mer-orchestrator", ProjectID: "mer", Kind: domain.KindOrchestrator, Metadata: domain.SessionMetadata{WorkspacePath: t.TempDir()}}
	if _, err := m.restoreSessionWorkspace(ctx, project, rec, true); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(ws.lastCfg.RefreshGitEnv) != 1 {
		t.Fatalf("orchestrator refresh env = %v", ws.lastCfg.RefreshGitEnv)
	}
	if _, err := m.restoreSessionWorkspace(ctx, project, rec, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if ws.lastCfg.RefreshGitEnv != nil {
		t.Fatalf("no refresh runs, so no token lookup: %v", ws.lastCfg.RefreshGitEnv)
	}
	rec.Kind = domain.KindWorker
	if _, err := m.restoreSessionWorkspace(ctx, project, rec, true); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if ws.lastCfg.RefreshGitEnv != nil {
		t.Fatalf("worker must not get refresh env: %v", ws.lastCfg.RefreshGitEnv)
	}
}
