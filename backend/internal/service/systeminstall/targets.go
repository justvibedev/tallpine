package systeminstall

import (
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Target is one of the fixed install targets Tallpine knows how to install.
type Target string

// The exhaustive set of installable targets. No other value is ever accepted.
const (
	TargetTmux       Target = "tmux"
	TargetGH         Target = "gh"
	TargetClaude     Target = "claude"
	TargetClaudeCode Target = "claude-code"
	TargetCodex      Target = "codex"
	TargetKiro       Target = "kiro"
	TargetDeepSeek   Target = "deepseek-harness"
	// TargetCloudflared is the optional connector that makes a paired phone
	// reachable from outside the local network.
	TargetCloudflared Target = "cloudflared"
)

// agentTargets is the stable settings-page order.
var agentTargets = []Target{TargetClaudeCode, TargetCodex, TargetKiro, TargetDeepSeek}

var agentTargetSet = func() map[Target]bool {
	out := make(map[Target]bool, len(agentTargets))
	for _, target := range agentTargets {
		out[target] = true
	}
	return out
}()

// systemTargetSet is the stable contract of the legacy /system/install route.
// Agent-only targets use /agents/{agent}/install instead.
var systemTargetSet = map[Target]bool{
	TargetTmux: true, TargetGH: true, TargetClaude: true, TargetCloudflared: true,
	TargetCodex: true,
}

// knownTargets is the exhaustive allowlist backing Valid.
var knownTargets = func() map[Target]bool {
	out := make(map[Target]bool, len(systemTargetSet)+len(agentTargets))
	for target := range systemTargetSet {
		out[target] = true
	}
	for _, target := range agentTargets {
		out[target] = true
	}
	return out
}()

// IsAgentTarget reports whether target is a user-facing harness id.
func IsAgentTarget(target Target) bool { return agentTargetSet[target] }

// IsSystemTarget reports whether target belongs to the legacy system-install
// route documented by InstallTargetParam.
func IsSystemTarget(target Target) bool { return systemTargetSet[target] }

// Valid reports whether target is a known prerequisite or agent install target.
func Valid(target Target) bool {
	return knownTargets[target]
}

// Resolve reports how target would be installed on goos, without running
// anything and without constructing a Service.
//
// This is the package's answer to "how is this installed here?" Keeping one
// resolver lets the daemon expose a plan preview and lets the bootstrap CLI
// print the same manual tmux remedy without either caller executing arbitrary
// input. The caller supplies the PATH lookup boundary explicitly.
func Resolve(goos string, lookPath func(string) (string, error), target Target) Plan {
	if !Valid(target) {
		return Plan{Target: target, Unsupported: true, Reason: "unknown install target"}
	}
	return (&Service{goos: goos, executables: executableFinderFunc(lookPath)}).resolvePlan(target)
}

type executableFinderFunc func(string) (string, error)

func (f executableFinderFunc) LookPath(file string) (string, error) { return f(file) }

// Plan is the resolved install command for a Target on the current platform.
//
// Command is populated whenever an install command could be resolved at all,
// including when Unsupported is true. Those two are independent on purpose:
// "we know exactly how to install this" and "this daemon is allowed to run it"
// are different questions. On Linux every package manager needs root, so the
// daemon refuses (Unsupported) while still reporting the argv for the desktop
// to display as a manual command. Callers that intend to execute a Plan must
// branch on Unsupported; callers that only need to know whether a route exists
// should test len(Command).
type Plan struct {
	Target  Target
	Command []string // argv, e.g. ["brew", "install", "tmux"]
	// Prepare runs before Command with the same environment, e.g. refreshing
	// Homebrew's metadata so `brew upgrade` sees the release Tallpine announced. A
	// failed step is noted in the output; Command still runs and post-update
	// verification decides the outcome.
	Prepare             [][]string
	Script              *ports.InstallScriptCommand
	Package             string
	PackagePrefix       string
	PackageCask         bool
	Manager             string // resolving package manager ("brew", "apt-get", ...), empty when none applies
	NeedsRoot           bool   // Command must run as root; the caller supplies the privilege
	Unsupported         bool
	Reason              string // set when Unsupported, or as extra context otherwise
	Notice              string // fixed user-visible consequence that applies even when available
	Method              string
	DocsURL             string
	ExpectedDestination string
	// UpdateWhileRunning is set on update plans: whether the update may run
	// while sessions use the binary. Empty means blocked.
	UpdateWhileRunning UpdateWhileRunning
	// keepPreviousRelease asks the installer to leave the replaced release on
	// disk because live sessions still run it.
	keepPreviousRelease bool
	// managed installs this update side by side in Tallpine's versioned directory
	// (see managed.go) instead of rewriting the installed copy.
	managed bool
	// managedPin is the release a managed reinstall installs again.
	managedPin string

	// daemonRuntimeRejected and daemonPrefixRejected mark an npm plan rejected
	// only for the daemon's own Node/npm or global prefix. An install runs
	// there, so it stays rejected; an update acts on the installation that owns
	// the binary, so it re-checks against that one instead.
	daemonRuntimeRejected bool
	daemonPrefixRejected  bool
	// deferredReason is the daemon rejection an update deferred; it applies
	// again when the owning installation turns out to use the daemon's runtime
	// or prefix, or cannot be found.
	deferredReason string
	// ownerPrefixFound records that targetInstalledCopy located the npm prefix
	// that owns the installed binary.
	ownerPrefixFound bool
}

// AgentPlan is the display-safe plan returned to the settings page. Command is
// a preview of fixed server-owned argv and is never accepted from the client.
type AgentPlan struct {
	AgentID             string               `json:"agentId"`
	Available           bool                 `json:"available"`
	Automatic           bool                 `json:"automatic"`
	Method              string               `json:"method"`
	Command             string               `json:"command,omitempty"`
	Reason              string               `json:"reason,omitempty"`
	Notice              string               `json:"notice,omitempty"`
	DocumentationURL    string               `json:"documentationUrl"`
	ExpectedDestination string               `json:"expectedDestination,omitempty"`
	Methods             []AgentInstallMethod `json:"methods"`
}

// AgentInstallMethod is one server-owned installation recipe and its current
// viability. The renderer may select ID but never submits argv.
type AgentInstallMethod struct {
	ID                  string `json:"id"`
	Label               string `json:"label"`
	Available           bool   `json:"available"`
	Recommended         bool   `json:"recommended"`
	Command             string `json:"command,omitempty"`
	Reason              string `json:"reason,omitempty"`
	Notice              string `json:"notice,omitempty"`
	ExpectedDestination string `json:"expectedDestination,omitempty"`
	ReinstallAvailable  bool   `json:"reinstallAvailable"`
	ReinstallCommand    string `json:"reinstallCommand,omitempty"`
	ReinstallReason     string `json:"reinstallReason,omitempty"`
	UpdateAvailable     bool   `json:"updateAvailable"`
	UpdateCommand       string `json:"updateCommand,omitempty"`
	UpdateReason        string `json:"updateReason,omitempty"`
}

// AgentOperation selects a fixed install, reinstall, or update plan.
type AgentOperation string

const (
	// AgentOperationInstall requests a first-time installation.
	AgentOperationInstall AgentOperation = "install"
	// AgentOperationReinstall requests an explicit rebuild of an existing installation.
	AgentOperationReinstall AgentOperation = "reinstall"
	// AgentOperationUpdate refreshes the installed agent CLI using its selected method.
	AgentOperationUpdate AgentOperation = "update"
)

func (operation AgentOperation) valid() bool {
	return operation == AgentOperationInstall || operation == AgentOperationReinstall || operation == AgentOperationUpdate
}

// Status is the lifecycle state of an install Job.
type Status string

// The full set of Job lifecycle states.
const (
	StatusIdle    Status = "idle"
	StatusRunning Status = "running"
	// StatusWaitingForIdle holds an update until every session using the
	// harness is idle; it then stops them, updates and resumes them.
	StatusWaitingForIdle Status = "waiting_for_idle"
	StatusInstalling     Status = "installing"
	StatusVerifying      Status = "verifying"
	StatusSucceeded      Status = "succeeded"
	StatusFailed         Status = "failed"
	StatusUnsupported    Status = "unsupported"
	StatusInterrupted    Status = "interrupted"
)
