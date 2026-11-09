package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/activitydispatch"
	agentregistry "tallpine.eu/tallpine/backend/internal/adapters/agent/registry"
	"tallpine.eu/tallpine/backend/internal/adapters/container/dockerreap"
	"tallpine.eu/tallpine/backend/internal/adapters/reviewer"
	"tallpine.eu/tallpine/backend/internal/adapters/runtime/runtimeselect"
	"tallpine.eu/tallpine/backend/internal/adapters/workspace/gitworktree"
	workspacerouter "tallpine.eu/tallpine/backend/internal/adapters/workspace/router"
	scratchworkspace "tallpine.eu/tallpine/backend/internal/adapters/workspace/scratch"
	"tallpine.eu/tallpine/backend/internal/artifactversions"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/lifecycle"
	activityobserver "tallpine.eu/tallpine/backend/internal/observe/activity"
	artifactsobserver "tallpine.eu/tallpine/backend/internal/observe/artifacts"
	"tallpine.eu/tallpine/backend/internal/observe/reaper"
	"tallpine.eu/tallpine/backend/internal/ports"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	reviewsvc "tallpine.eu/tallpine/backend/internal/service/review"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
	"tallpine.eu/tallpine/backend/internal/sessionreap"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

const startupReconcileWorkers = 4

type notificationSink interface {
	Notify(context.Context, ports.NotificationIntent) error
	Resolve(context.Context, ports.NotificationResolution) error
}

// lifecycleStack owns the runtime reaper goroutine started with the lifecycle
// reducer. The reducer itself is only used for wiring observations into storage.
type lifecycleStack struct {
	// LCM is the Lifecycle Manager (the canonical write path). It is exposed so
	// startSession can share the same reducer the reaper drives, rather than
	// standing up a second store+LCM pair that would diverge under writes.
	LCM *lifecycle.Manager
	// ArtifactVersions is the one artifact version capturer. The session
	// service deletes artifacts through it so a delete holds the capture lock.
	ArtifactVersions   *artifactversions.Capturer
	runtimeReaper      *reaper.Reaper
	reaperDone         <-chan struct{}
	activityDone       <-chan struct{}
	turnFailureDone    <-chan struct{}
	artifactsDone      <-chan struct{}
	autoReviewDone     <-chan struct{}
	reviewSweepDone    <-chan struct{}
	waitEscalationDone <-chan struct{}
	scmDone            <-chan struct{}
	trackerDone        <-chan struct{}
	automationDone     <-chan struct{}
}

// startLifecycle constructs the Lifecycle Manager over the store and starts the
// reaper. The goroutine stops when ctx is cancelled; Stop waits for it to drain.
// The messenger is the per-daemon agent messenger the LCM uses to nudge agents
// in response to SCM observations (CI failure, review feedback, merge conflict).
func startLifecycle(ctx context.Context, dataDir string, store *sqlite.Store, runtime ports.Runtime, messenger ports.AgentMessenger, notifier notificationSink, agents ports.AgentResolver, artifactKeep int, logger *slog.Logger) *lifecycleStack {
	lcm := lifecycle.New(store, messenger,
		lifecycle.WithNotificationSink(notifier),
		lifecycle.WithContainerReaper(dockerreap.New(), store),
		lifecycle.WithProcessReaper(sessionreap.New()),
		lifecycle.WithActiveSteering(activeTurnSteering(agents)),
		lifecycle.WithStartupSignalGate(startupSignalGatesInput(agents)),
		lifecycle.WithUrgentNudgeGate(urgentNudgeWaitingInputSafe(agents)),
		lifecycle.WithDataDir(dataDir),
	)
	rp := reaper.New(lcm, store, runtime, reaper.Config{Logger: logger})
	activityPoller := activityobserver.New(store, lcm, runtime, agents, activityobserver.Config{Logger: logger, DataDir: dataDir})
	turnFailurePoller := activityobserver.NewTurnFailureObserver(store, lcm, agents, activityobserver.TurnFailureConfig{Logger: logger})
	artifactVersions := artifactversions.New(store, dataDir, logger)
	artifactVersions.SetKeep(artifactKeep)
	go artifactTrimOnStart(ctx, artifactVersions, logger)
	go scratchTrashOnStart(ctx, dataDir, logger)
	go sweepSessionProcesses(ctx, lcm)
	artifactsPoller := artifactsobserver.New(store, lcm, artifactVersions, artifactsobserver.Config{Logger: logger})
	return &lifecycleStack{
		LCM:              lcm,
		ArtifactVersions: artifactVersions,
		runtimeReaper:    rp,
		reaperDone:       rp.Start(ctx),
		activityDone:     activityPoller.Start(ctx),
		turnFailureDone:  turnFailurePoller.Start(ctx),
		artifactsDone:    artifactsPoller.Start(ctx),
	}
}

// artifactTrimDelay keeps the one-time history trim away from daemon startup.
const artifactTrimDelay = 2 * time.Minute

// artifactTrimOnStart applies the configured version cap to history recorded
// under an older, larger one, once per daemon run.
func artifactTrimOnStart(ctx context.Context, c *artifactversions.Capturer, logger *slog.Logger) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(artifactTrimDelay):
	}
	if err := c.TrimAll(ctx, 50*time.Millisecond); err != nil && ctx.Err() == nil {
		logger.Warn("artifact versions: startup trim failed", "err", err)
	}
}

// scratchTrashOnStart removes session temp directories and harness caches a
// previous daemon run discarded but did not finish removing (it exited
// mid-removal).
func scratchTrashOnStart(ctx context.Context, dataDir string, logger *slog.Logger) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(artifactTrimDelay):
	}
	if err := sessionscratch.EmptyTrash(ctx, dataDir); err != nil && ctx.Err() == nil {
		logger.Warn("session temp trash: startup cleanup incomplete", "err", err)
	}
	if err := sessionscratch.EmptyCacheTrash(ctx, dataDir); err != nil && ctx.Err() == nil {
		logger.Warn("harness cache trash: startup cleanup incomplete", "err", err)
	}
}

// startupSignalGatesInput resolves whether an adapter promises that its first
// hook proves native TUI startup dialogs have cleared. Unknown and hookless
// adapters remain ungated.
func startupSignalGatesInput(agents ports.AgentResolver) func(domain.AgentHarness) bool {
	return func(harness domain.AgentHarness) bool {
		if agents == nil {
			return false
		}
		agent, ok := agents.Agent(harness)
		if !ok {
			return false
		}
		signaler, ok := agent.(ports.StartupInputReadinessSignaler)
		return ok && signaler.FirstSignalProvesInputReady()
	}
}

// urgentNudgeWaitingInputSafe resolves whether an adapter reports a permission
// dialog AS blocked (ports.BlockedActivitySignaler) rather than as
// waiting_input. Only then is an urgent merge-conflict nudge safe to paste at a
// waiting_input prompt — a harness like codex, which maps permission-request to
// waiting_input and opts out of the signal, must not receive that unsolicited
// write while it could be sitting on a masked decision. Unknown and opted-out
// adapters answer false, so lifecycle withholds the urgent nudge there.
func urgentNudgeWaitingInputSafe(agents ports.AgentResolver) func(domain.AgentHarness) bool {
	return func(harness domain.AgentHarness) bool {
		if agents == nil {
			return false
		}
		agent, ok := agents.Agent(harness)
		if !ok {
			return false
		}
		signaler, ok := agent.(ports.BlockedActivitySignaler)
		return ok && signaler.EmitsBlockedActivity()
	}
}

// ReconcileRuntime runs the same conservative runtime/workload observation as
// the periodic reaper. The daemon calls it after session-manager reconciliation
// so exits missed while Tallpine was stopped are folded before the API starts serving.
func (l *lifecycleStack) ReconcileRuntime(ctx context.Context) error {
	return l.runtimeReaper.Tick(ctx)
}

// activeTurnSteering resolves the per-harness active-turn steering capability
// from the agent adapters, so lifecycle policy consumes an adapter-declared
// capability instead of knowing harness-specific terminal semantics. An
// unresolvable harness, or one whose adapter does not declare the capability,
// answers false.
func activeTurnSteering(agents ports.AgentResolver) func(domain.AgentHarness) bool {
	return func(harness domain.AgentHarness) bool {
		if agents == nil {
			return false
		}
		agent, ok := agents.Agent(harness)
		if !ok {
			return false
		}
		steerer, ok := agent.(ports.ActiveTurnSteerer)
		return ok && steerer.SteersActiveTurn()
	}
}

// Stop waits for the reaper goroutine to exit. The caller must cancel the ctx
// passed to startLifecycle before calling Stop.
func (l *lifecycleStack) Stop() {
	<-l.reaperDone
	if l.activityDone != nil {
		<-l.activityDone
	}
	if l.turnFailureDone != nil {
		<-l.turnFailureDone
	}
	if l.artifactsDone != nil {
		<-l.artifactsDone
	}
	if l.autoReviewDone != nil {
		<-l.autoReviewDone
	}
	if l.waitEscalationDone != nil {
		<-l.waitEscalationDone
	}
	if l.reviewSweepDone != nil {
		<-l.reviewSweepDone
	}
	if l.scmDone != nil {
		<-l.scmDone
	}
	if l.trackerDone != nil {
		<-l.trackerDone
	}
	if l.automationDone != nil {
		<-l.automationDone
	}
}

// sessionLifecycle is the narrow surface of sessionmanager.Manager used for
// boot/shutdown wiring. A minimal interface keeps the daemon testable without
// depending on the concrete manager type.
//
// SaveAndTeardownAll is deliberately ABSENT from this interface so the daemon
// cannot tear down live sessions on shutdown. Sessions survive the daemon exit
// and Reconcile on the next boot adopts them, preserving session IDs. Re-adding
// the method here is a visible, reviewable interface change.
type sessionLifecycle interface {
	Reconcile(ctx context.Context) error
	ReconcileStartupSafety(ctx context.Context) error
	ReconnectAwakeAgent(ctx context.Context, target domain.AwakeAgentTarget) error
	ReconcileBackground(ctx context.Context) error
	HibernateIdleChats(ctx context.Context) error
	SetBeforeHibernate(before func(context.Context, domain.SessionID) (bool, error))
	RefreshStaleChats(ctx context.Context, minIdle time.Duration) error
	RefreshStaleTerminals(ctx context.Context, minIdle time.Duration) error
	BusyHarnessSessions(ctx context.Context, harness domain.AgentHarness) ([]domain.SessionWait, error)
	StopHarnessSessions(ctx context.Context, harness domain.AgentHarness) ([]domain.SessionID, error)
	ResumeHarnessSessions(ctx context.Context, ids []domain.SessionID) error
	SetVersionRefreshPolicy(policy func() sessionmanager.VersionRefreshPolicy)
	RetryVersionMoves(ctx context.Context) error
	WorkerInstructions(ctx context.Context, id domain.SessionID) (string, error)
	MarkHarnessUpdated(ctx context.Context, harness domain.AgentHarness, version string, at time.Time) error
	CountStaleBinarySessions(ctx context.Context) (map[domain.AgentHarness]int64, error)
	RestoreAll(ctx context.Context) error
	WaitBackgroundWorkers(ctx context.Context) error
	WaitAgentSwitchWorkers(ctx context.Context) error
	Kill(ctx context.Context, id domain.SessionID) (bool, error)
	Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error
	// SetShellTerminalCloser late-binds Kill/Cleanup to close a session's
	// scoped shell terminals before its worktree is torn down. shellterm.Service
	// is built after Session Manager during boot (see startShellTerminals), so
	// this cannot be a constructor argument.
	SetShellTerminalCloser(closer sessionmanager.ShellTerminalCloser)
	// AcquireSessionInput holds direct terminal writes across the actual pane
	// write while ownership may move between provider processes.
	AcquireSessionInput(id domain.SessionID) (release func(), ok bool)
	// SessionMutationInProgress suppresses observation-driven termination while
	// Session Manager deliberately replaces or relaunches a provider process.
	SessionMutationInProgress(id domain.SessionID) bool
	// SetTerminalInputGate prevents mux input from racing a TUI-to-Chat handoff.
	SetTerminalInputGate(gate sessionmanager.TerminalInputGate)
	// SetReviewerTerminator late-binds worker lifecycle teardown to the review
	// service, which is built alongside the controller-facing service below.
	SetReviewerTerminator(terminator sessionmanager.ReviewerTerminator)
	// SetHarnessUseGate prevents lifecycle operations from racing a harness
	// executable replacement.
	SetHarnessUseGate(gate sessionmanager.HarnessUseGate)
	// PersistChatModel records the model the user picked in ChatUI onto the
	// session's durable metadata before the next prompt routes. A later TUI
	// rebuild reads it back so ChatUI model changes survive the handoff.
	PersistChatModel(ctx context.Context, id domain.SessionID, model string) error
}

// sessionLifecycleMessenger adapts sessionLifecycle to ports.AgentMessenger so
// the fully-wired manager can replace the boot-time pane messenger once ready.
// None of the daemon-internal sends this feeds (interface-transition drains,
// lifecycle nudges) carry an attachment, so it is always nil here; the
// attachment-aware send path only exists at the HTTP /send endpoint.
type sessionLifecycleMessenger struct {
	sessionLifecycle
}

func (m sessionLifecycleMessenger) Send(ctx context.Context, id domain.SessionID, message string) error {
	return m.sessionLifecycle.Send(ctx, id, message, nil)
}

// startSession builds the controller-facing session service: a session manager
// over the selected runtime, routed git/scratch workspaces, the shared store +
// LCM, the per-session agent resolver, and the agent messenger. The tracker is
// built once by the caller (Run) and shared with the intake observer; it may
// be nil (no usable credentials) — the service's nil-guard handles that
// (issue #2685). The returned service is mounted at httpd APIDeps.Sessions.
// It also returns the manager so the caller can wire Reconcile into the boot
// sequence.
func startSession(ctx context.Context, cfg config.Config, runtime runtimeselect.Runtime, store *sqlite.Store, lcm *lifecycle.Manager, artifactVersions *artifactversions.Capturer, messenger ports.AgentMessenger, notifications notificationSink, agents ports.AgentResolver, agentReadiness ports.AgentReadinessProvider, previewLifecycle sessionmanager.PreviewLifecycle, browserLifecycle sessionmanager.BrowserLifecycle, browserCapabilities sessionmanager.BrowserCapabilityIssuer, sessionTokens *sessiontoken.Authority, chat sessionmanager.ChatLauncher, reviewerRecoveryDone <-chan struct{}, defaults sessionmanager.SessionModeDefaults, tracker ports.Tracker, codexOperationGate ports.CodexOperationGate, reviewers *reviewer.Resolver, reviewerAccounts *reviewcore.AccountEnvSlot, githubAccounts sessionmanager.GitHubAccountTokens, log *slog.Logger) (*sessionsvc.Service, reviewsvc.Manager, sessionLifecycle, error) {
	gitWS, err := gitworktree.New(gitworktree.Options{
		// Per-session worktrees live under the data dir, so a single TALLPINE_DATA_DIR
		// override moves all durable per-user state together.
		ManagedRoot: filepath.Join(cfg.DataDir, "worktrees"),
		// Resolve each project's source repo from the projects table, so a
		// session spawned for a registered project materialises its worktree off
		// that repo. Unregistered projects fail loudly.
		RepoResolver: projectRepoResolver{store: store},
		Logger:       log,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("session workspace: %w", err)
	}
	scratchWS, err := scratchworkspace.New(scratchworkspace.Options{
		ManagedRoot: filepath.Join(cfg.DataDir, "worktrees"),
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("scratch session workspace: %w", err)
	}
	ws := workspacerouter.New(workspacerouter.Deps{
		Git:      gitWS,
		Scratch:  scratchWS,
		Projects: store,
	})
	mgr := sessionmanager.New(sessionmanager.Deps{
		Runtime:             runtime,
		Agents:              agents,
		Workspace:           ws,
		Store:               store,
		Messenger:           messenger,
		Chat:                chat,
		Defaults:            defaults,
		Lifecycle:           lcm,
		Preview:             previewLifecycle,
		Browser:             browserLifecycle,
		BrowserCapabilities: browserCapabilities,
		SessionTokens:       sessionTokens,
		DataDir:             cfg.DataDir,
		RunFilePath:         cfg.RunFilePath,
		SessionGradleHome:   cfg.SessionGradleHome,
		BackgroundContext:   ctx,
		Logger:              log,
		ReconcileWorkers:    startupReconcileWorkers,
		CodexOperationGate:  codexOperationGate,
	})
	mgr.SetAgentReadiness(agentReadiness)
	sessionmanager.SetDefaultHarness(domain.AgentHarness(normalizeDefaultAgent(cfg.Agent, log)))
	scmProvider := newMultiSCMProvider(cfg.GitLab, log)
	sessionSvc := sessionsvc.NewWithDeps(sessionsvc.Deps{
		Manager:           mgr,
		Store:             store,
		PRClaimer:         store,
		SCM:               scmProvider,
		DataDir:           cfg.DataDir,
		Tracker:           tracker,
		Logger:            log,
		BackgroundContext: ctx,
		AgentReadiness:    agentReadiness,
		// no_signal only makes sense for harnesses with complete lifecycle signal
		// coverage; partial callbacks cannot prove that silence is abnormal.
		SignalCapable:        activitydispatch.FullySupportsHarness,
		OutputTypeReconciler: lcm,
		ArtifactDeleter:      artifactVersions,
	})
	// Triggering a review spawns a reviewer over the worker's worktree, resolved
	// from the reviewer registry (distinct from the worker agent set). The
	// reviewer posts its review to the PR itself, so the service needs no SCM
	// writer.
	if reviewers == nil {
		if reviewers, err = reviewer.NewResolver(); err != nil {
			return nil, nil, nil, fmt.Errorf("reviewer resolver: %w", err)
		}
	}
	if reviewerAccounts == nil {
		reviewerAccounts = &reviewcore.AccountEnvSlot{}
	}
	// The engine reads a zero idle grace as its default; configured off is negative.
	reviewIdleGrace := cfg.ReviewIdleGrace
	if reviewIdleGrace == 0 {
		reviewIdleGrace = -1
	}
	var reviewPlans reviewcore.PlanSource
	if cfg.Planner {
		reviewPlans = workerPlanSource{store: store}
	}
	reviewEngine := reviewcore.New(reviewcore.Deps{
		Plans: reviewPlans,
		Items: workerPlanSource{store: store},
		// The manager's installer gate is bound after the installer is built.
		HarnessUse: mgr,
		Store:      store,
		Sessions:   store,
		PRs:        store,
		Projects:   store,
		Launcher:   newReviewerLauncher(cfg, reviewers, runtime, store, sessionTokens, reviewerAccounts, githubAccounts, agents, agentReadiness, chat),

		ChatRecoveryDone: reviewerRecoveryDone,
		PassDeadline:     cfg.ReviewPassDeadline,
		IdleGrace:        reviewIdleGrace,
	})
	reviewOpts := []reviewsvc.Option{
		reviewsvc.WithNotificationSink(notifications),
		reviewsvc.WithCodexAccountOperationGate(codexOperationGate),
	}
	if scmProvider != nil {
		reviewOpts = append(reviewOpts,
			reviewsvc.WithReviewRequester(scmProvider),
			reviewsvc.WithReviewResolver(scmProvider),
		)
	}
	refresher := reviewPRRefresher{sessions: sessionSvc}
	if scmProvider != nil {
		// Assign only a real provider: a nil *Provider in the interface is non-nil.
		refresher.store, refresher.scm = store, scmProvider
	}
	reviewOpts = append(reviewOpts, reviewsvc.WithPRRefresher(refresher))
	reviewSvc := reviewsvc.New(reviewEngine, store, reviewOpts...)
	mgr.SetReviewerTerminator(reviewSvc)
	return sessionSvc, reviewSvc, mgr, nil
}

// reviewFailureSweeper is the review service's background half: it reconciles
// running passes without a list call and tells the worker and orchestrator
// about passes that ended without a verdict.
type reviewFailureSweeper interface {
	SetFailureNotice(reviewsvc.FailureNoticeStore, reviewsvc.FailureReports, reviewsvc.WorkerNudger)
	StartSweep(context.Context, time.Duration) <-chan struct{}
}

// runtimeMessageSender is the narrow part of the concrete runtime needed by
// tallpine send. Both tmux.Runtime and conpty.Runtime implement this via SendMessage.
type runtimeMessageSender interface {
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
}

// newSessionMessenger assembles the per-daemon agent messenger: `tallpine send`
// submits the message to the live runtime pane through terminal admission.
func newSessionMessenger(store *sqlite.Store, runtime runtimeMessageSender, _ *slog.Logger, resolvers ...ports.AgentResolver) ports.AgentMessenger {
	var agents ports.AgentResolver
	if len(resolvers) > 0 {
		agents = resolvers[0]
	}
	return sessionmanager.NewTerminalAdmission(store, runtime, agents)
}

// modeAwareMessenger lets lifecycle start before the session manager while
// ensuring every reaction crosses the same persisted-mode dispatcher as an
// explicit `tallpine send`. A send in the short boot window waits for Bind instead of
// falling through to the terminal runtime, which would be wrong for Chat sessions.
type modeAwareMessenger struct {
	mu     sync.RWMutex
	target ports.AgentMessenger
	ready  chan struct{}
	once   sync.Once
}

func newModeAwareMessenger() *modeAwareMessenger {
	return &modeAwareMessenger{ready: make(chan struct{})}
}

func (m *modeAwareMessenger) Bind(target ports.AgentMessenger) {
	m.mu.Lock()
	m.target = target
	m.mu.Unlock()
	m.once.Do(func() { close(m.ready) })
}

func (m *modeAwareMessenger) Send(ctx context.Context, id domain.SessionID, message string) error {
	m.mu.RLock()
	target := m.target
	m.mu.RUnlock()
	if target == nil {
		select {
		case <-m.ready:
		case <-ctx.Done():
			return ctx.Err()
		}
		m.mu.RLock()
		target = m.target
		m.mu.RUnlock()
	}
	if target == nil {
		return fmt.Errorf("mode-aware lifecycle messenger bound without a target")
	}
	return target.Send(ctx, id, message)
}

// buildAgentRegistry returns a registry populated with the agent adapters the
// daemon ships, keyed by manifest id. Registration only fails on an
// empty/duplicate id — a programmer error, not a runtime condition.
// The shipped adapter list lives in the adapters/agent/registry package
// (registry.Constructors). Adding a new harness is a one-line edit there.
func buildAgentRegistry() (*adapters.Registry, error) {
	return agentregistry.Build()
}

type reviewerAgentAuth struct {
	readiness ports.AgentReadinessProvider
}

func (r reviewerAgentAuth) AuthStatus(ctx context.Context, harness domain.ReviewerHarness) (ports.AgentAuthStatus, bool, error) {
	if r.readiness == nil {
		return "", false, nil
	}
	snapshot, err := r.readiness.EnsureAgentReadiness(ctx, string(harness), domain.AgentReadinessPurposeLaunch)
	if err != nil {
		return "", false, err
	}
	switch snapshot.Authentication.State {
	case domain.AgentAuthenticationAuthorized, domain.AgentAuthenticationNotApplicable:
		return ports.AgentAuthStatusAuthorized, true, nil
	case domain.AgentAuthenticationConfigured:
		return ports.AgentAuthStatusConfigured, true, nil
	case domain.AgentAuthenticationUnauthorized:
		return ports.AgentAuthStatusUnauthorized, true, nil
	default:
		return ports.AgentAuthStatusUnknown, true, nil
	}
}

// normalizeDefaultAgent resolves the configured default agent (TALLPINE_AGENT). An
// empty value means the built-in default; a harness earlier builds shipped and
// this one removed resolves to the fallback with a warning rather than aborting
// daemon start. Any other unknown id is returned unchanged so a typo still
// fails in buildAgentResolver.
func normalizeDefaultAgent(configured string, log *slog.Logger) string {
	if configured == "" {
		return config.DefaultAgent
	}
	if domain.AgentHarness(configured).IsRemoved() {
		log.Warn("TALLPINE_AGENT names an unsupported harness; falling back",
			"configured", configured, "fallback", domain.FallbackHarness)
		return string(domain.FallbackHarness)
	}
	return configured
}

// buildAgentResolver constructs the per-session agent resolver the Session
// Manager consumes (sessionmanager.Deps.Agents): a registry of the shipped
// adapters. It still validates TALLPINE_AGENT at startup for compatibility with the
// config surface, but worker/orchestrator spawns must provide a resolved
// harness before calling Agent.
func buildAgentResolver(defaultAgent string, log *slog.Logger) (ports.AgentResolver, error) {
	if defaultAgent == "" {
		defaultAgent = config.DefaultAgent
	}
	reg, err := buildAgentRegistry()
	if err != nil {
		return nil, err
	}
	resolver := agentRegistry{reg: reg}
	if _, ok := resolver.Agent(domain.AgentHarness(defaultAgent)); !ok {
		return nil, fmt.Errorf("configured agent %q is not a registered adapter", defaultAgent)
	}
	ids := make([]string, 0)
	for _, mf := range reg.Manifests() {
		ids = append(ids, mf.ID)
	}
	log.Info("built per-session agent resolver", "default", defaultAgent, "registered", ids)
	return resolver, nil
}

// projectRepoResolver resolves a project's on-disk repo path from the projects
// table so gitworktree can materialise per-session worktrees off it. It replaces
// the empty StaticRepoResolver the daemon used before (which failed every
// lookup), turning a registered project into a spawnable one.
type projectRepoResolver struct{ store *sqlite.Store }

var _ gitworktree.RepoResolver = projectRepoResolver{}

func (r projectRepoResolver) RepoPath(projectID domain.ProjectID) (string, error) {
	rec, ok, err := r.store.GetProject(context.Background(), string(projectID))
	if err != nil {
		return "", fmt.Errorf("look up project %q: %w", projectID, err)
	}
	if !ok {
		return "", fmt.Errorf("no project registered with id %q — add one with `tallpine project add`: %w", projectID, sessionmanager.ErrProjectNotResolvable)
	}
	if !rec.ArchivedAt.IsZero() {
		return "", fmt.Errorf("project %q is archived: %w", projectID, sessionmanager.ErrProjectNotResolvable)
	}
	if rec.Path == "" {
		return "", fmt.Errorf("project %q has no repo path on record: %w", projectID, sessionmanager.ErrProjectNotResolvable)
	}
	return rec.Path, nil
}

func (r reviewerLaunchAuth) ValidateLaunchAuth(ctx context.Context, workingDir string, env map[string]string) (ports.AgentAuthStatus, error) {
	if r.agents == nil {
		return ports.AgentAuthStatusUnknown, nil
	}
	agent, ok := r.agents.Agent(domain.HarnessClaudeCode)
	if !ok {
		return ports.AgentAuthStatusUnknown, nil
	}
	validator, ok := agent.(ports.AgentLaunchAuthValidator)
	if !ok {
		return ports.AgentAuthStatusUnknown, nil
	}
	return validator.ValidateLaunchAuth(ctx, workingDir, env)
}

// policyOf returns the agent policy engine behind the chat launcher, nil when
// there is none (tests, minimal wiring), so reviewer launches decide under it.
func policyOf(chat sessionmanager.ChatLauncher) reviewcore.PolicyDecider {
	if launcher, ok := chat.(chatLauncher); ok && launcher.policy != nil {
		return launcher.policy
	}
	return nil
}
