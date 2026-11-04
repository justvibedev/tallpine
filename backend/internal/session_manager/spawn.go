package sessionmanager

import (
	"context"
	"sync"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// spawnState carries the locals the Spawn stages share. cfg is mutated in
// place as the stages resolve the harness, mode and agent config.
type spawnState struct {
	// policy is the agent policy decision for this spawn, computed once in
	// spawnResolveModelAndMode and handed to every seam the spawn drives.
	policy agentpolicy.Decision
	// requestedPermissions is the mode before the policy cap, for the decision record.
	requestedPermissions ports.PermissionMode
	cfg                  ports.SpawnConfig
	project              domain.ProjectRecord
	projectKind          domain.ProjectKind

	releaseHarness func()
	agentConfig    ports.AgentConfig
	adapterConfig  ports.AgentConfig
	mode           domain.SessionMode
	asyncChat      bool

	prep              *taskPreparation
	rec               domain.SessionRecord
	id                domain.SessionID
	artifactDir       string
	prompt            string
	systemPrompt      string
	systemPromptFile  string
	promptBytes       int
	systemPromptBytes int
	branch            string

	taskName        func() generatedTaskName
	cancelNaming    context.CancelFunc
	namingHandedOff bool

	ws               ports.WorkspaceInfo
	workspaceProject *ports.WorkspaceProjectInfo

	agent            ports.Agent
	env              map[string]string
	launchCfg        ports.LaunchConfig
	delivery         ports.PromptDeliveryStrategy
	afterStartPrompt string
	argv             []string
	launchID         string
	releaseCodex     func()
	handle           ports.RuntimeHandle
}

// spawnExit is a stage's instruction to return from Spawn now. A nil *spawnExit
// means the stage finished and Spawn continues.
type spawnExit struct {
	rec               domain.SessionRecord
	promptBytes       int
	systemPromptBytes int
	err               error
}

func spawnFail(err error) *spawnExit { return &spawnExit{err: err} }

func (x *spawnExit) unpack() (domain.SessionRecord, int, int, error) {
	return x.rec, x.promptBytes, x.systemPromptBytes, x.err
}

// Spawn creates the session row (which assigns the "{project}-{n}" id), then the
// workspace and runtime, then reports completion to the LCM. If workspace
// materialization fails the still-seed row is deleted outright; a later failure
// parks the row as terminated and rolls back what was built.
//
// Each stage lives in its own method; the defers stay here so they run when
// Spawn returns, in the order they were registered.
func (m *Manager) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	st := &spawnState{cfg: cfg, cancelNaming: func() {}}
	if x := m.spawnLoadProject(ctx, st); x != nil {
		return x.unpack()
	}
	ctx, cancelBudget := ports.ExtendSpawnRequestBudget(ctx, postCreateBudget(0, st.project.Config.PostCreate))
	defer cancelBudget()
	defer func() {
		if st.releaseHarness != nil {
			st.releaseHarness()
		}
	}()
	defer func() {
		if !st.namingHandedOff {
			st.cancelNaming()
		}
	}()
	if x := m.spawnResolveHarness(ctx, st); x != nil {
		return x.unpack()
	}
	if x := m.spawnResolveModelAndMode(ctx, st); x != nil {
		return x.unpack()
	}
	if x := m.spawnCreateSeed(ctx, st); x != nil {
		return x.unpack()
	}
	if x := m.spawnBindAndBuildPrompts(ctx, st); x != nil {
		return x.unpack()
	}
	if x := m.spawnPromotePreparation(ctx, st); x != nil {
		return x.unpack()
	}
	// A speculative worktree may still be being created under this gate. Wait
	// for it above without holding the gate, then serialize the remaining
	// synchronous workspace lifecycle as before. Async Chat acquires it in its
	// background half so the API response never waits on Git.
	if !st.asyncChat {
		st.taskName, st.cancelNaming = m.startTaskNaming(st.cfg, st.rec, st.project.Config)
		releaseWorkspaceGate := sync.OnceFunc(m.acquireSpawnWorkspaceGate(st.cfg.ProjectID))
		ctx = context.WithValue(ctx, spawnWorkspaceGateKey{}, releaseWorkspaceGate)
		defer releaseWorkspaceGate()
	}
	m.markFreshSessionStatusReady(st.id)
	m.spawnResolveBranch(st)
	if x := m.spawnBeginAsync(ctx, st); x != nil {
		return x.unpack()
	}
	if x := m.spawnCreateWorkspace(ctx, st); x != nil {
		return x.unpack()
	}
	if x := m.spawnProvision(ctx, st); x != nil {
		return x.unpack()
	}
	// Everything above is shared: project, harness, prompts, seed row, worktree,
	// provisioning, attachments. From here the two modes launch different
	// controllers, and exactly one of them runs.
	if st.mode == domain.SessionModeChat {
		return m.spawnLaunchChat(ctx, st).unpack()
	}
	if x := m.spawnPrepareWorker(ctx, st); x != nil {
		return x.unpack()
	}
	defer m.lcm.CancelLaunch(st.id, st.launchID)
	if x := m.spawnAcquireCodex(ctx, st); x != nil {
		return x.unpack()
	}
	defer st.releaseCodex()
	if x := m.spawnCreateRuntime(ctx, st); x != nil {
		return x.unpack()
	}
	if x := m.spawnMarkSpawned(ctx, st); x != nil {
		return x.unpack()
	}
	return m.spawnFinish(ctx, st).unpack()
}
