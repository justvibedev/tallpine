// Package systeminstall executes real install commands from a fixed allowlist
// of system prerequisites and agent harnesses. This is
// the core security invariant of the package — a caller can only select
// which fixed Target value to install; the actual argv run on
// the machine is always built from hardcoded command shapes, never from
// caller-supplied strings. Runs are tracked as async Jobs so an HTTP handler
// never blocks on an installer that can take minutes.
package systeminstall

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var (
	// ErrHarnessActive prevents installers from replacing a binary used by a live session.
	ErrHarnessActive = errors.New("systeminstall: harness has an active session")
	// ErrInstallMethod reports an unknown or currently unavailable install method.
	ErrInstallMethod = errors.New("systeminstall: invalid install method")
	// ErrInstallActive prevents a second operation from being silently discarded.
	ErrInstallActive = errors.New("systeminstall: harness install operation already active")
	// ErrInstallOwner refuses to update a binary the selected method did not install.
	ErrInstallOwner = errors.New("systeminstall: installation owner not confirmed")
	// ErrUpdateVersion rejects an invalid caller-supplied verification target.
	ErrUpdateVersion = errors.New("systeminstall: invalid expected update version")
)

// maxOutputBytes bounds Job.Output so a chatty installer can't grow memory
// unbounded — only the last ~4000 bytes are kept.
const maxOutputBytes = 4000

// defaultInstallTimeout bounds how long a single install run may take. A
// stalled installer (a network hang on curl, a held brew/apt lock, winget
// waiting on a prompt it'll never get) would otherwise pin its target in
// StatusRunning forever: no retry path, and the caller polls an indefinite
// progress bar. Real installs (npm global, brew, curl-piped scripts) normally
// finish in well under a minute; 15 minutes is generous headroom, not a
// realistic expected duration.
const defaultInstallTimeout = 15 * time.Minute

// defaultPersistenceTimeout keeps best-effort worker state writes from
// consuming the daemon's entire shutdown drain budget behind a blocked DB.
const defaultPersistenceTimeout = 2 * time.Second

// Job is the tracked state of one harness operation for a Target.
type Job struct {
	Target              Target `json:"target" enum:"tmux,gh,claude,claude-code,codex,kiro,deepseek-harness,cloudflared" description:"Fixed install target this job ran (or is running) for."`
	Status              Status `json:"status" enum:"idle,running,waiting_for_idle,installing,verifying,succeeded,failed,unsupported,interrupted" description:"Current lifecycle state of the job. waiting_for_idle: the update runs once every session using the harness is idle (Windows)."`
	Method              string `json:"method,omitempty" description:"Server-owned installation method selected for this harness job."`
	Command             string `json:"command,omitempty" description:"Human-readable install command, e.g. \"brew install tmux\", for display even before/without output."`
	ExpectedDestination string `json:"expectedDestination,omitempty" description:"Expected or adapter-resolved executable destination."`
	Output              string `json:"output,omitempty" description:"Combined stdout+stderr from the install command, tail-capped to the last ~4000 bytes."`
	Error               string `json:"error,omitempty" description:"Set on failure or when the target is unsupported on this machine: the exec error, the Unsupported reason, or a timeout message."`
	Notice              string `json:"notice,omitempty" description:"Fixed user-visible consequence to acknowledge before running this install."`
	// Pointers, not time.Time: omitempty has no effect on a struct, so a bare
	// time.Time always serializes (as the zero value's "0001-01-01..."
	// timestamp) even when nothing has happened yet. A nil pointer actually
	// omits the field, matching FinishedAt's documented "zero until the job
	// finishes" contract.
	// WaitingOn lists the sessions a waiting_for_idle job still waits for.
	WaitingOn  []domain.SessionWait `json:"waitingOn,omitempty" description:"While waiting_for_idle, the sessions the update still waits for and why."`
	StartedAt  *time.Time           `json:"startedAt,omitempty"`
	FinishedAt *time.Time           `json:"finishedAt,omitempty" description:"Absent until the job finishes."`
	UpdatedAt  *time.Time           `json:"updatedAt,omitempty"`
}

// HarnessVerifier performs post-install binary verification without probing
// authentication.
type HarnessVerifier interface {
	Verify(ctx context.Context, target Target) (VerifyResult, error)
}

// SessionLister exposes durable lifecycle facts used to keep maintenance
// operations away from active harness processes.
type SessionLister interface {
	ListLiveSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

// ReviewerLister exposes reviewer processes, which have no session row of
// their own: live TUI reviewer panes and reviewer chats with a live controller.
type ReviewerLister interface {
	ListLiveReviewerHandles(ctx context.Context) ([]domain.ReviewerHandle, error)
	ListRecoverableChatReviews(ctx context.Context) ([]domain.Review, error)
}

// Deps are the durable and adapter-backed dependencies used for harness jobs.
type Deps struct {
	JobStore ports.AgentInstallJobStore
	Verifier HarnessVerifier
	Sessions SessionLister
	// Reviewers, when set, keeps maintenance away from live reviewers too.
	Reviewers ReviewerLister
	// DisableUpdateChecks stops update advisories from reaching the network
	// (TALLPINE_HARNESS_UPDATE_CHECKS=off).
	DisableUpdateChecks bool
	// DisableUpdateWhileRunning makes every update wait for the harness's
	// sessions to end, as before update-while-running existed
	// (TALLPINE_UPDATE_WHILE_RUNNING=off).
	DisableUpdateWhileRunning bool
	// ManagedRoot is where Tallpine keeps versioned harness installs (under the Tallpine
	// data dir). Empty keeps every update in place.
	ManagedRoot string
	// Releases records which managed release each session launch runs, so a
	// release is deleted only once no live session runs it.
	Releases ManagedReleaseStore
	// WindowsUpdateWhenIdle lets a Windows update wait for idle sessions, stop
	// them, update and resume them, instead of refusing while they run
	// (TALLPINE_WINDOWS_UPDATE_WHEN_IDLE).
	WindowsUpdateWhenIdle bool
	PrivateNPMPrefixes    map[Target]string
}

// Service runs real install commands for the fixed Target allowlist.
type Service struct {
	mu                sync.Mutex
	jobs              map[Target]*Job
	backgroundContext context.Context
	stop              context.CancelFunc
	stopping          bool
	workers           sync.WaitGroup
	harnessGatesMu    sync.Mutex
	harnessGates      map[domain.AgentHarness]*sync.RWMutex

	executables         ports.ExecutableFinder
	commands            ports.CommandRunner
	installCommands     ports.InstallCommandRunner
	installScripts      ports.InstallScriptRunner
	installCapabilities ports.InstallCapabilityProbe
	pathWritable        ports.PathWritableProbe
	privateNPMPrefixes  map[Target]string
	jobStore            ports.AgentInstallJobStore
	verifier            HarnessVerifier
	sessions            SessionLister
	reviewers           ReviewerLister
	// goos selects the platform branch in planFor. Real use is always
	// runtime.GOOS; tests override it to exercise every OS branch from one
	// machine, the same seam lookPath provides for PATH probing.
	goos string
	// installTimeout bounds each run — see defaultInstallTimeout. Tests
	// override it with a short duration to exercise the timeout path without
	// a real multi-minute wait.
	installTimeout time.Duration
	// persistenceTimeout bounds worker-owned transition and terminal writes.
	persistenceTimeout   time.Duration
	onSucceeded          func(Target)
	managedVersion       managedVersionChecker
	ownsInstallation     func(context.Context, string, string, string, bool) (bool, error)
	updateAdvisories     map[Target]UpdateAdvisory
	updateChecksDisabled bool
	// updateWhileRunningDisabled forces every update to wait for sessions.
	updateWhileRunningDisabled bool
	// managedRoot holds Tallpine's versioned harness installs; "" turns them off.
	managedRoot string
	// releases tracks the managed release each session launch runs.
	releases ManagedReleaseStore
	// invalidateBinary makes a harness adapter resolve its executable again.
	invalidateBinary func(Target)
	// windowsUpdateWhenIdle turns Windows' blocked level into update-when-idle.
	windowsUpdateWhenIdle bool
	// changing marks harnesses whose installation is being changed, so a
	// launch waits for the change instead of failing.
	changing map[Target]bool
	// idleUpdates are the waiting or running update-when-idle runs.
	idleUpdates map[Target]*idleUpdateRun
	// idleCoordinator stops and resumes a harness's sessions around such a run.
	idleCoordinator IdleUpdateCoordinator
	// updateLevels remembers the level each running update was started under.
	updateLevels map[Target]UpdateWhileRunning
	// updateObserver marks sessions an update left on the replaced release.
	updateObserver HarnessUpdateObserver
	// deferredCleanups are Homebrew formulas whose old keg is kept until the
	// sessions that run it have moved to the new release.
	deferredCleanups    map[Target]string
	derivedOwners       map[Target]derivedOwner
	updateAdvisoryCalls map[Target]*updateAdvisoryCall
	// updateCheckSlots bounds concurrent update checks to
	// maxConcurrentUpdateChecks; created on first use.
	updateCheckSlots chan struct{}
	// officialVersion reads a vendor release channel when no package manager
	// owns the harness binary.
	officialVersion func(context.Context, Target) (string, error)
}

// requestPlanner carries one immutable capability snapshot through all recipe
// resolution performed for a single HTTP/service request.
type requestPlanner struct {
	*Service
	capabilities *ports.InstallCapabilities
	ctx          context.Context
}

func (s *Service) newRequestPlanner(ctx context.Context) (requestPlanner, error) {
	planner := requestPlanner{Service: s, ctx: ctx}
	if s.installCapabilities == nil {
		return planner, nil
	}
	capabilities, err := s.installCapabilities.Probe(ctx)
	if err != nil {
		return requestPlanner{}, err
	}
	planner.capabilities = &capabilities
	return planner, nil
}

// SetOnSucceeded registers the daemon callback invoked after a successful
// harness operation. It is called outside the job mutex.
func (s *Service) SetOnSucceeded(callback func(Target)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSucceeded = callback
}

// New returns a Service backed by explicit host-operation ports. The daemon
// supplies their concrete adapter; core service code never invokes os/exec.
func New(executables ports.ExecutableFinder, commands ports.CommandRunner) *Service {
	return NewWithDeps(executables, commands, Deps{})
}

// NewWithDeps returns a Service with durable harness-job and verification
// dependencies supplied by the daemon.
func NewWithDeps(executables ports.ExecutableFinder, commands ports.CommandRunner, deps Deps) *Service {
	installCommands, _ := commands.(ports.InstallCommandRunner)
	installScripts, _ := commands.(ports.InstallScriptRunner)
	installCapabilities, _ := executables.(ports.InstallCapabilityProbe)
	pathWritable, _ := executables.(ports.PathWritableProbe)
	backgroundContext, stop := context.WithCancel(context.Background())
	return &Service{
		jobs:                       make(map[Target]*Job),
		harnessGates:               make(map[domain.AgentHarness]*sync.RWMutex),
		executables:                executables,
		commands:                   commands,
		installCommands:            installCommands,
		installScripts:             installScripts,
		installCapabilities:        installCapabilities,
		jobStore:                   deps.JobStore,
		verifier:                   deps.Verifier,
		sessions:                   deps.Sessions,
		reviewers:                  deps.Reviewers,
		updateChecksDisabled:       deps.DisableUpdateChecks,
		updateWhileRunningDisabled: deps.DisableUpdateWhileRunning,
		windowsUpdateWhenIdle:      deps.WindowsUpdateWhenIdle,
		managedRoot:                deps.ManagedRoot,
		releases:                   deps.Releases,
		goos:                       runtime.GOOS,
		installTimeout:             defaultInstallTimeout,
		persistenceTimeout:         defaultPersistenceTimeout,
		stop:                       stop,
		backgroundContext:          backgroundContext,
		managedVersion:             newManagedVersionChecker(commands, nil),
		officialVersion:            officialReleaseVersion(),
		ownsInstallation:           managerOwnsBinary(commands),
		updateAdvisories:           make(map[Target]UpdateAdvisory),
		updateAdvisoryCalls:        make(map[Target]*updateAdvisoryCall),
		pathWritable:               pathWritable,
		privateNPMPrefixes:         deps.PrivateNPMPrefixes,
	}
}
