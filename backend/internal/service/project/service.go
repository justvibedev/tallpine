package project

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"
)

// Manager is the controller-facing contract for the /api/v1/projects surface.
type Manager interface {
	// List returns every registered project, including degraded entries
	// (those whose config failed to load but whose registry entry survives).
	List(ctx context.Context) ([]Summary, error)

	// Get returns one project, discriminating ok vs degraded via GetResult.
	Get(ctx context.Context, id domain.ProjectID) (GetResult, error)

	// Add registers a new project from a git repository path.
	Add(ctx context.Context, in AddInput) (Project, error)

	// Clone checks out a remote git repository and registers the resulting
	// local repository as a project.
	Clone(ctx context.Context, in CloneInput) (Project, error)
	PrepareClone(ctx context.Context, in CloneInput) (ClonePreparationResult, error)
	CheckCloneDestination(ctx context.Context, in CloneInput) (CloneDestination, error)
	CleanupPreparedClone(ctx context.Context, in ClonePreparationCleanupInput) error

	// InitializeRepository prepares a selected folder for project registration.
	InitializeRepository(ctx context.Context, in InitializeRepositoryInput) (InitializeRepositoryResult, error)

	// UpdateSettings atomically replaces a project's user-facing display name
	// and per-project config, returning the updated read-model.
	UpdateSettings(ctx context.Context, id domain.ProjectID, in UpdateSettingsInput) (Project, error)
	SetPermissions(ctx context.Context, id domain.ProjectID, in SetPermissionsInput) (Project, error)
	// ForgetPermissions clears the remembered policy, so new sessions use the agent's own default.
	ForgetPermissions(ctx context.Context, id domain.ProjectID) (Project, error)

	// SetConfig replaces a project's per-project config, returning the updated
	// read-model.
	SetConfig(ctx context.Context, id domain.ProjectID, in SetConfigInput) (Project, error)

	// SetRules changes only agentRules and orchestratorRules, keeping every
	// other config field, and logs each change.
	SetRules(ctx context.Context, id domain.ProjectID, in SetRulesInput) (Project, error)

	// Remove unregisters a project, stopping its sessions and reclaiming
	// managed workspaces.
	Remove(ctx context.Context, id domain.ProjectID) (RemoveResult, error)
}

// SessionTeardowner is the narrow session-service surface project removal
// needs: stop live project sessions and reclaim managed terminal workspaces.
type SessionTeardowner interface {
	TeardownProject(ctx context.Context, project domain.ProjectID) error
}

// Service implements project registration and lookup use-cases for controllers.
type Service struct {
	store          Store
	sessions       SessionTeardowner
	clock          func() time.Time
	defaultHarness domain.AgentHarness
	logger         *slog.Logger
	audit          *settingsaudit.Recorder
	managed        *managedcfg.Locks
	ghAccounts     GitHubAccountResolver
	policy         PolicyEngine
	// protectedDirs are directories (the daemon data dir) an agent-created
	// project may not live in.
	protectedDirs []string
	// addMu serialises the whole body of Add. Workspace registration performs
	// filesystem mutations (git init, .gitignore writes, commits) that are not
	// covered by the store's own writeMu, so path/id conflict checks plus the
	// subsequent mutation must be atomic from the perspective of concurrent callers.
	addMu sync.Mutex
	// onConfigChanged, when set, runs after a project config write so running
	// sessions can follow it (B398: an orchestrator's compaction window).
	onConfigChanged []func(ctx context.Context, id domain.ProjectID)
}

// SetConfigChangedHook late-binds what runs after a project's config is
// written. Call it during wiring, before the service serves requests.
func (m *Service) SetConfigChangedHook(hook func(ctx context.Context, id domain.ProjectID)) {
	m.onConfigChanged = append(m.onConfigChanged, hook)
}

const maxDisplayNameLen = 100

var _ Manager = (*Service)(nil)

// Deps captures optional collaborators for project use-cases.
type Deps struct {
	// DefaultHarness is the daemon's configured default agent (TALLPINE_AGENT).
	// When empty, the service falls back to config.DefaultAgent.
	DefaultHarness domain.AgentHarness
	Store          Store
	Sessions       SessionTeardowner
	// GitHubAccounts resolves the account label shown on a project; optional.
	GitHubAccounts GitHubAccountResolver
	// Policy decides the agent policy; optional (nil restricts nothing).
	Policy PolicyEngine
	Clock  func() time.Time
	// ProtectedDirs are directories no agent-created project may be in or under
	// (the daemon data dir: worktrees, artifacts).
	ProtectedDirs []string
	// Logger receives structured logs. Left nil, the service falls back to
	// slog.Default, keeping service-focused tests logger-free.
	Logger *slog.Logger
}

// New returns a project service backed by the given durable store.
func New(store Store) *Service {
	return NewWithDeps(Deps{Store: store})
}

// NewWithDeps returns a project service with optional teardown dependencies.
func NewWithDeps(d Deps) *Service {
	defaultHarness := d.DefaultHarness
	if defaultHarness == "" {
		defaultHarness = domain.AgentHarness(config.DefaultAgent)
	}
	if resolved, replaced := defaultHarness.OrFallback(); replaced {
		logger := d.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("TALLPINE_AGENT names an unsupported harness; falling back",
			"configured", defaultHarness, "fallback", resolved)
		defaultHarness = resolved
	}
	s := &Service{
		policy:         d.Policy,
		store:          d.Store,
		sessions:       d.Sessions,
		clock:          d.Clock,
		defaultHarness: defaultHarness,
		logger:         d.Logger,
		ghAccounts:     d.GitHubAccounts,
		protectedDirs:  d.ProtectedDirs,
	}
	if s.clock == nil {
		s.clock = time.Now
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	return s
}

// List returns every active registered project.
func (m *Service) List(ctx context.Context) ([]Summary, error) {
	projects, err := m.store.ListProjects(ctx)
	if err != nil {
		return nil, projectStoreError(err, "PROJECTS_LIST_FAILED", "Failed to load projects")
	}
	out := make([]Summary, 0, len(projects))
	for _, row := range projects {
		folderMissing := false
		exists, err := folderExists(row.Path)
		if err != nil {
			m.logger.Warn("project: stat failed", "path", row.Path, "error", err)
		} else {
			folderMissing = !exists
		}
		out = append(out, Summary{
			ID:                domain.ProjectID(row.ID),
			Name:              projectDisplayName(row),
			Path:              row.Path,
			Kind:              row.Kind.WithDefault(),
			SessionPrefix:     resolveSessionPrefix(row),
			OrchestratorAgent: row.Config.Orchestrator.Harness,
			FolderMissing:     folderMissing,
		})
	}
	return out, nil
}

// Get returns one active project by id.
func (m *Service) Get(ctx context.Context, id domain.ProjectID) (GetResult, error) {
	if err := validateProjectID(id); err != nil {
		return GetResult{}, err
	}
	row, ok, err := m.store.GetProject(ctx, string(id))
	if err != nil {
		return GetResult{}, projectStoreError(err, "PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok || !row.ArchivedAt.IsZero() {
		return GetResult{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	p := m.projectFromRow(ctx, row)
	if row.Kind.WithDefault() == domain.ProjectKindWorkspace {
		repos, err := m.store.ListWorkspaceRepos(ctx, row.ID)
		if err != nil {
			return GetResult{}, projectStoreError(err, "PROJECT_LOAD_FAILED", "Failed to load workspace repositories")
		}
		p.WorkspaceRepos = workspaceReposFromRecords(row.Path, repos)
	}
	return GetResult{Status: "ok", Project: &p}, nil
}

// PolicyEngine decides the agent policy for a project's sessions.
type PolicyEngine interface {
	Decide(ctx context.Context, session agentpolicy.SessionRef) agentpolicy.Decision
}

// SetManaged sets the managed locks; call before the service is shared.
func (m *Service) SetManaged(l *managedcfg.Locks) { m.managed = l }

// SetAgentPolicy completes daemon wiring before request handling begins. Nil
// leaves every project unrestricted.
func (m *Service) SetAgentPolicy(engine PolicyEngine) { m.policy = engine }

// SetSettingsAudit records project config changes in the settings change log.
// Nil records nothing.
func (m *Service) SetSettingsAudit(r *settingsaudit.Recorder) { m.audit = r }

// recordConfig logs the project config keys a write changed.
func (m *Service) recordConfig(ctx context.Context, id domain.ProjectID, before, after domain.ProjectConfig) {
	m.audit.Record(ctx, id, settingsaudit.ProjectConfig(before), settingsaudit.ProjectConfig(after))
	for _, hook := range m.onConfigChanged {
		hook(ctx, id)
	}
}

// decide is the policy decision for sessions of project id, with no session
// yet: the user's remembered permissions are validated against it.
func (m *Service) decide(ctx context.Context, id domain.ProjectID) agentpolicy.Decision {
	if m.policy == nil {
		return agentpolicy.Decision{}
	}
	return m.policy.Decide(ctx, agentpolicy.SessionRef{Project: id})
}
