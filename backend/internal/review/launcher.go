package review

import (
	"context"
	"os"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const cancelInterruptDelay = 150 * time.Millisecond

const defaultReviewerInitialDelay = 500 * time.Millisecond

const reviewerTaskMessagePrefix = "Read and follow the Tallpine review task in `"

// EnvRunFile points reviewer-local Tallpine CLI calls at the live daemon run-file.
const EnvRunFile = "TALLPINE_RUN_FILE"

// EnvTallpineCommandWarning records why Tallpine could not guarantee a reviewer-local `tallpine`
// command. Review launch remains best-effort so provider reviews can still be
// posted and later reconciled, but this makes the degraded path diagnosable.
const EnvTallpineCommandWarning = "TALLPINE_REVIEW_TALLPINE_COMMAND_WARNING"

// Launcher spawns, re-notifies, and probes a reviewer over a worker's worktree.
// It is the side of the engine that talks to the reviewer registry and runtime;
// the engine owns the orchestration and persistence.
type Launcher interface {
	// InterfaceMode reports the durable surface Spawn and RestoreTerminal will use.
	InterfaceMode(harness domain.ReviewerHarness) domain.ReviewerInterfaceMode
	// Preflight checks whether the reviewer for the given harness is available
	// to run (binary on PATH, etc.) without starting a runtime pane. It runs
	// only when a reviewer launch is actually required, after ReviewRun rows
	// have been created. On failure the engine's Trigger() calls failRuns() to
	// mark those rows as failed, matching the existing Spawn failure semantics.
	// workerID names the worker under review: a Claude reviewer is checked on
	// the worker's agent account, not the device login.
	Preflight(ctx context.Context, workerID domain.SessionID, harness domain.ReviewerHarness, workspacePath string, mode ...domain.ReviewerInterfaceMode) error
	// live pane (stable per worker, reused across passes) plus any native agent
	// session id known at launch time.
	Spawn(ctx context.Context, spec LaunchSpec) (LaunchResult, error)
	// RestoreTerminal launches an idle reviewer pane for a worker that already
	// has review history, without creating or starting a new review run.
	RestoreTerminal(ctx context.Context, spec LaunchSpec) (LaunchResult, error)
	// Notify asks an already-running reviewer pane to review a new commit.
	Notify(ctx context.Context, handleID string, spec LaunchSpec) error
	// Alive reports whether the exact reviewer process is still running.
	Alive(ctx context.Context, handleID, launchID string) (bool, error)
	// Reusable reports whether the harness accepts another review task in its
	// existing TUI. Reviewers with launch-fixed context return false.
	Reusable(harness domain.ReviewerHarness) bool
	// Cancel interrupts reviewer work while keeping its Chat or terminal alive.
	Cancel(ctx context.Context, handleID string, harness domain.ReviewerHarness) error
	// Destroy tears down a reviewer pane entirely. This is used when the owning
	// worker session itself is torn down, not for user-facing review cancellation.
	Destroy(ctx context.Context, handleID string) error
}

// LaunchSpec is the engine's request to (re)launch a reviewer for one pass.
type LaunchSpec struct {
	RunID           string
	BatchID         string
	ReviewSessionID string
	LaunchID        string
	// PreviousHandleID is the runtime handle recorded for this reviewer's
	// previous pane. It can be backend-qualified, so a terminal launch destroys
	// it as well as the bare per-worker name before creating the replacement.
	PreviousHandleID string
	WorkerID         domain.SessionID
	ProjectID        domain.ProjectID
	ProjectEnv       map[string]string
	Harness          domain.ReviewerHarness
	AgentConfig      domain.AgentConfig
	WorkspacePath    string
	AgentSessionID   string
	// ProviderConversationID is the typed Chat driver's stable conversation id.
	// Terminal reviewers continue to use AgentSessionID.
	ProviderConversationID string
	RequireNativeHistory   bool
	PreviousRuns           []domain.ReviewRun
	PRURL                  string
	TargetSHA              string
	ReviewQueue            []ports.ReviewTask
	ReviewIndex            int
	// TaskContext is the worker's stored task/issue text, already sanitized and
	// truncated. It is untrusted background for the reviewer, never instructions.
	TaskContext string
	// PlanDecisions and PlanFocus come from the approved plan of the backlog
	// item the worker builds (B205); empty when there is none.
	PlanDecisions string
	PlanFocus     []string
	InterfaceMode domain.ReviewerInterfaceMode
	// DeferInitialMessage starts the reviewer without dispatching its review
	// prompt. The engine uses this while replacing a live reviewer so the old
	// process can be stopped before the replacement receives executable work.
	DeferInitialMessage bool
}

// LaunchResult is the terminal/runtime state created by a reviewer launch.
type LaunchResult struct {
	HandleID       string
	LaunchID       string
	AgentSessionID string
	// NativeResumed reports whether the launch resumed the provider-native
	// conversation instead of falling back to a fresh reviewer process.
	NativeResumed bool
}

// ReviewerChatStart is the transport-neutral typed reviewer launch request.
type ReviewerChatStart struct {
	BatchID                string
	ReviewID               string
	WorkerID               domain.SessionID
	ProjectID              domain.ProjectID
	Harness                domain.AgentHarness
	Model                  string
	Effort                 string
	DataDir                string
	WorkspacePath          string
	Env                    map[string]string
	Prompt                 string
	SystemPrompt           string
	ProviderConversationID string
}

// ReviewerChatController is the narrow bridge from the review engine to the
// typed Chat service. It deliberately excludes HTTP and renderer concerns.
type ReviewerChatController interface {
	SupportsReviewChat(domain.AgentHarness) bool
	PreflightReviewChat(context.Context, domain.AgentHarness) error
	StartReviewChat(context.Context, ReviewerChatStart) (string, error)
	RestoreReviewChat(context.Context, ReviewerChatStart) (string, error)
	SendReviewChat(context.Context, string, string, string) error
	ReviewChatAlive(string) bool
	InterruptReviewChat(context.Context, string) error
	StopReviewChat(context.Context, string) error
}

const reviewerChatHandlePrefix = "review-chat:"

// reviewerRuntime is the runtime surface the launcher needs: create a pane,
// inject a message into a running pane, and probe liveness. The tmux runtime
// satisfies it.
type reviewerRuntime interface {
	Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error)
	Destroy(ctx context.Context, handle ports.RuntimeHandle) error
	Interrupt(ctx context.Context, handle ports.RuntimeHandle) error
	SendInput(ctx context.Context, handle ports.RuntimeHandle, input string) error
	IsChildAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
	IsExactSupervisedProcessAlive(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (bool, error)
	HasSupervisedProcessRecord(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
}

// agentLauncher resolves a reviewer adapter from the registry and drives the
// runtime. The reviewer reuses the worker's worktree (a fresh session worktree
// would branch off the default branch and so would not contain the PR changes).
type agentLauncher struct {
	reviewers  ports.ReviewerResolver
	runtime    reviewerRuntime
	dataDir    string
	runFile    string
	tokens     ReviewerTokenIssuer
	auth       agentAuthResolver
	executable func() (string, error)
	chat       ReviewerChatController
	accounts   ports.AgentAccountProviderEnv
	launchAuth ReviewerLaunchAuth
	policy     PolicyDecider

	// launchedHome is the Claude config dir each live reviewer handle was
	// started under, so a reused reviewer can be told apart from the worker's
	// current account (handle -> dir).
	launchedHome sync.Map
}

type preLaunchReviewer interface {
	PreLaunch(ctx context.Context, inv ports.ReviewInvocation) error
}

type preflightReviewer interface {
	ReviewPreflight(ctx context.Context, workspacePath string) error
}

type agentAuthResolver interface {
	AuthStatus(ctx context.Context, harness domain.ReviewerHarness) (ports.AgentAuthStatus, bool, error)
}

// LauncherOption configures reviewer launcher behavior.
type LauncherOption func(*agentLauncher)

// WithReviewerChat enables typed reviewer conversations for supporting
// adapters. A nil controller intentionally keeps every reviewer on TUI.
func WithReviewerChat(chat ReviewerChatController) LauncherOption {
	return func(l *agentLauncher) { l.chat = chat }
}

// PolicyDecider decides the agent policy for a session; the policy engine
// satisfies it.
type PolicyDecider interface {
	Decide(ctx context.Context, session agentpolicy.SessionRef) agentpolicy.Decision
}

// WithPolicy makes reviewer launches run under the agent policy of the reviewed
// worker's project, so a terminal reviewer is capped like the worker it reviews.
func WithPolicy(policy PolicyDecider) LauncherOption {
	return func(l *agentLauncher) { l.policy = policy }
}

// decide is the policy decision for a reviewer launch of spec's project: both a
// fresh review launch and a restored idle terminal run under it. The zero
// Decision (no engine wired) restricts nothing.
func (l *agentLauncher) decide(ctx context.Context, spec LaunchSpec) agentpolicy.Decision {
	if l.policy == nil {
		return agentpolicy.Decision{}
	}
	return l.policy.Decide(ctx, agentpolicy.SessionRef{Project: spec.ProjectID, Kind: domain.KindWorker})
}

// WithAgentAuth lets reviewer preflight reuse the agent auth catalog for the
// same harness. Reviewer-specific auth probes must not be stricter than the
// normal agent's local auth status.
func WithAgentAuth(auth agentAuthResolver) LauncherOption {
	return func(l *agentLauncher) {
		l.auth = auth
	}
}

// WithExecutable overrides os.Executable for reviewer PATH pinning. Production
// leaves this unset; tests use it to model the daemon binary that should make a
// bare `tallpine review submit` available inside reviewer panes.
func WithExecutable(executable func() (string, error)) LauncherOption {
	return func(l *agentLauncher) {
		l.executable = executable
	}
}

// WithRunFilePath pins reviewer-local Tallpine CLI calls to this daemon's run-file.
// This is intentionally separate from TALLPINE_DATA_DIR: the CLI discovers the live
// daemon from TALLPINE_RUN_FILE, not from the durable data directory.
func WithRunFilePath(path string) LauncherOption {
	return func(l *agentLauncher) {
		l.runFile = path
	}
}

// ReviewerTokenIssuer mints a reviewer's scoped daemon credential, bound to
// its review and the worker under review (see internal/sessiontoken).
type ReviewerTokenIssuer interface {
	IssueReviewer(reviewID string, worker domain.SessionID) string
}

// WithReviewerTokens gives every reviewer launch a scoped TALLPINE_SESSION_TOKEN, so
// its `tallpine` calls (activity hooks, `tallpine review submit`) never need the install
// token.
func WithReviewerTokens(tokens ReviewerTokenIssuer) LauncherOption {
	return func(l *agentLauncher) {
		l.tokens = tokens
	}
}

// NewLauncher builds the production reviewer launcher.
func NewLauncher(reviewers ports.ReviewerResolver, rt reviewerRuntime, dataDir string, opts ...LauncherOption) Launcher {
	l := &agentLauncher{reviewers: reviewers, runtime: rt, dataDir: dataDir, executable: os.Executable}
	for _, opt := range opts {
		opt(l)
	}
	return l
}
