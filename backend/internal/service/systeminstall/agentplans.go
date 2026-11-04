package systeminstall

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

var agentDocumentationURLs = map[Target]string{
	TargetClaudeCode: "https://code.claude.com/docs/en/installation",
	TargetCodex:      "https://github.com/openai/codex",
	TargetKiro:       "https://kiro.dev/docs/getting-started/installation/",
	TargetDeepSeek:   "https://github.com/deepseek-ai/deepseek-harness",
}

func (s requestPlanner) agentMethodPlans(target Target, operation AgentOperation) []Plan {
	var plans []Plan
	switch target {
	case TargetClaudeCode:
		official := s.officialByOS(target, "https://claude.ai/install.sh", "bash", "https://claude.ai/install.ps1", agentDocumentationURLs[target])
		if s.goos == "darwin" {
			plans = []Plan{s.planBrewCask(target, "claude-code"), s.planNPM(target, "@anthropic-ai/claude-code"), official}
		} else {
			plans = []Plan{s.planNPM(target, "@anthropic-ai/claude-code"), official}
		}
	case TargetCodex:
		official := s.officialByOS(target, "https://chatgpt.com/codex/install.sh", "sh", "https://chatgpt.com/codex/install.ps1", agentDocumentationURLs[target])
		if s.goos == "darwin" {
			plans = []Plan{s.planBrewCask(target, "codex"), s.planNPM(target, "@openai/codex"), official}
		} else {
			plans = []Plan{s.planNPM(target, "@openai/codex"), official}
		}
	case TargetKiro:
		plans = []Plan{s.planKiroInstall()}
	case TargetDeepSeek:
		// One Node CLI boots every profile (headless, ACP, web), so npm is the
		// only install method.
		plans = []Plan{s.planNPM(target, "@deepseek-ai/dsh")}
	default:
		plans = []Plan{{Target: target, Unsupported: true, Method: "manual", Reason: "unknown install target"}}
	}
	if owner, ok := s.derivedOwnerFor(target); ok {
		if derived, ok := s.derivedOwnerPlan(target, owner); ok {
			plans = withDerivedOwner(plans, derived)
		}
	}
	for index := range plans {
		plans[index].DocsURL = agentDocumentationURLs[target]
		plans[index] = s.planForOperation(plans[index], operation)
	}
	return plans
}

func (s requestPlanner) planKiroInstall() Plan {
	if s.goos == "darwin" {
		return manualPlan(
			TargetKiro,
			"Kiro's official macOS installer writes an app bundle to /Applications and must be run interactively. Follow Kiro's installation guide, then refresh harness status.",
			agentDocumentationURLs[TargetKiro],
		)
	}
	return s.officialByOS(TargetKiro,
		"https://cli.kiro.dev/install", "bash",
		"https://cli.kiro.dev/install.ps1", agentDocumentationURLs[TargetKiro])
}

func (s requestPlanner) planForOperation(plan Plan, operation AgentOperation) Plan {
	if operation == AgentOperationUpdate && plan.Method == "npm" && (plan.daemonRuntimeRejected || plan.daemonPrefixRejected) {
		// The installed copy may live under another prefix (nvm, fnm, ~/.local)
		// with its own Node. The update validates that installation once the
		// owner is known, and applies this reason again if it uses the daemon's.
		plan.deferredReason = plan.Reason
		plan.Unsupported, plan.Reason = false, ""
	}
	if operation == AgentOperationInstall || plan.Unsupported {
		return plan
	}
	installScript := plan.Script
	plan.Script = nil
	if operation == AgentOperationReinstall {
		if record, ok := s.readManagedRecord(plan.Target); ok && record.Method == plan.Method && s.managedEligible(plan) {
			// Sessions run Tallpine's managed copy, so a reinstall repairs that one:
			// the same release, installed fresh beside it.
			plan.managed = true
			plan.managedPin = record.Version
			argv, _ := managedInstallCommand(plan.Method, record.Package, record.Version, filepath.Join(s.managedTargetDir(plan.Target), "versions", "<next>"))
			plan.Command, plan.Prepare = argv, nil
			return plan
		}
		switch plan.Method {
		case "homebrew":
		case "npm":
			plan.Command = append(plan.Command, "--force")
		case "winget":
			plan.Command = append(plan.Command, "--force")
		case "uv":
			plan.Command = []string{"uv", "tool", "install", plan.Package, "--force", "--reinstall"}
		case "pipx":
			plan.Command = []string{"pipx", "install", "--force", plan.Package}
		case "bun":
			plan.Command = append(plan.Command, "--force")
		case "official-installer":
			plan.Unsupported = true
			plan.Command = nil
			plan.Script = nil
			plan.Reason = "This vendor installer does not provide a verified headless reinstall operation."
		default:
			plan.Unsupported = true
			plan.Reason = "This installation method does not provide an explicit reinstall operation."
		}
		return plan
	}

	if operation == AgentOperationUpdate {
		switch plan.Method {
		case "homebrew":
			// The runner sets HOMEBREW_NO_AUTO_UPDATE, and the advisory reads the
			// live Homebrew API, so refresh local metadata before upgrading.
			plan.Prepare = [][]string{{"brew", "update"}}
			plan.Command = []string{"brew", "upgrade"}
			if plan.PackageCask {
				plan.Command = append(plan.Command, "--cask")
			}
			plan.Command = append(plan.Command, plan.Package)
		case "npm":
			pkg := packageWithoutLatest(plan.Package)
			plan.Command = []string{"npm", "install", "-g", "--prefix", plan.PackagePrefix, pkg + "@latest"}
		case "winget":
			plan.Command = []string{"winget", "upgrade", "-e", "--id", plan.Package, "--silent", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"}
		case "uv":
			plan.Command = []string{"uv", "tool", "upgrade", plan.Package}
		case "pipx":
			plan.Command = []string{"pipx", "upgrade", plan.Package}
		case "bun":
			plan.Command = []string{"bun", "install", "-g", packageWithoutLatest(plan.Package) + "@latest"}
		case "official-installer":
			// These self-updaters do not reliably replace the installed binary
			// headlessly: installerUpdateTargets on every platform, and these
			// Windows ones. Re-run the vendor's installer instead; ownership and
			// post-update version checks still guard the operation.
			if installScript != nil && installerUpdateTargets[plan.Target] {
				plan.Command = nil
				plan.Script = installScript
				break
			}
			command, ok := vendorUpdateCommands[plan.Target]
			if !ok {
				plan.Unsupported = true
				plan.Reason = "This vendor installer does not expose a supported update command."
			} else {
				plan.Command = slices.Clone(command)
			}
		default:
			plan.Unsupported = true
			plan.Reason = "This installation method does not provide a supported update command."
		}
		if !plan.Unsupported {
			plan.UpdateWhileRunning = updateWhileRunningLevel(s.goos, plan.Target, plan.Method, plan.PackageCask)
		}
		if s.managedEligible(plan) {
			// Installed side by side, so no session's files are touched, on any
			// platform: the update is safe while sessions run.
			plan.managed = true
			plan.UpdateWhileRunning = UpdateWhileRunningSafe
			argv, _ := managedInstallCommand(plan.Method, packageWithoutLatest(plan.Package), "", filepath.Join(s.managedTargetDir(plan.Target), "versions", "<next>"))
			plan.Command, plan.Prepare = argv, nil
		}
		return plan
	}

	return plan
}

// vendorUpdateCommands are the harnesses' own self-update commands, used only
// when the vendor installer, not a package manager, owns the running binary.
// Each updates the stable channel without a terminal: Kimchi's "self" leaves
// its extensions alone and --force skips its confirmation prompt, and
// OpenCode is told the install method Tallpine already proved. Amp's --porcelain
// prints a one-line result instead of interactive progress, and Pi updates
// only itself without trusting project-local files.
var vendorUpdateCommands = map[Target][]string{
	TargetClaudeCode: {"claude", "update"},
	TargetCodex:      {"codex", "update"},
}

// vendorUpdateEnv turns a vendor's own auto-update switch back on for an
// update the user asked for. `droid update` exits 0 without updating, and
// prints "Auto-update is not available for npm installations", whenever
// FACTORY_DROID_AUTO_UPDATE_ENABLED is false or its build (npm's) turns
// auto-update off, even for the copy at the official installer's location.
var vendorUpdateEnv = map[Target][]string{}

// installerUpdateTargets update by re-running the vendor installer, pinned to
// the approved release and bound to the confirmed install directory. Kimi's
// `kimi upgrade` exits 0 without replacing the binary: it reports "already up
// to date" from its own staged-rollout channel, prints a manual command for
// installs it does not manage, and on native installs only stages the new
// binary for a later launch to swap in.
var installerUpdateTargets = map[Target]bool{}

func packageWithoutLatest(pkg string) string {
	return strings.TrimSuffix(pkg, "@latest")
}

// planAgent preserves the legacy single-plan call sites while selecting from
// the same method registry used by the catalog and execution route.
func (s *Service) planAgent(target Target) Plan {
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		return Plan{Target: target, Unsupported: true, Method: "manual", Reason: "install capabilities could not be inspected", DocsURL: agentDocumentationURLs[target]}
	}
	plans := planner.agentMethodPlans(target, AgentOperationInstall)
	return plans[recommendedPlanIndex(plans)]
}

func (s *Service) officialByOS(target Target, unixURL, unixShell, windowsURL, docsURL string) Plan {
	switch s.goos {
	case "windows":
		return withDocs(s.planPowerShellInstaller(target, windowsURL), docsURL)
	case "darwin", "linux":
		return withDocs(s.planShellInstaller(target, unixURL, unixShell), docsURL)
	default:
		return manualPlan(target, fmt.Sprintf("The official installer does not support %s.", s.goos), docsURL)
	}
}

func (s *Service) planShellInstaller(target Target, url, shell string) Plan {
	resolved, err := s.executables.LookPath(shell)
	if err != nil {
		return Plan{Target: target, Unsupported: true, Method: "official-installer", Reason: fmt.Sprintf("%s was not found on PATH.", shell)}
	}
	return Plan{
		Target: target, Method: "official-installer",
		Script: &ports.InstallScriptCommand{URL: url, Interpreter: []string{resolved}},
	}
}

func (s *Service) planPowerShellInstaller(target Target, url string) Plan {
	// WindowsApps can expose a pwsh.exe execution alias that LookPath finds but
	// the daemon cannot launch. Prefer the Windows PowerShell binary instead.
	for _, shell := range []string{"powershell.exe", "powershell", "pwsh.exe", "pwsh"} {
		resolved, err := s.executables.LookPath(shell)
		if err != nil {
			continue
		}
		return Plan{
			Target: target, Method: "official-installer",
			Script: &ports.InstallScriptCommand{
				URL:         url,
				Interpreter: []string{resolved, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File"},
			},
		}
	}
	return Plan{Target: target, Unsupported: true, Method: "official-installer", Reason: "PowerShell was not found on PATH."}
}

func (s *Service) planUV(target Target, pkg string) Plan {
	if _, err := s.executables.LookPath("uv"); err != nil {
		return Plan{Target: target, Unsupported: true, Method: "uv", Reason: "uv was not found on PATH. Install uv, then retry."}
	}
	return Plan{Target: target, Method: "uv", Command: []string{"uv", "tool", "install", pkg}, Package: pkg}
}

func (s *Service) planPipx(target Target, pkg string) Plan {
	if _, err := s.executables.LookPath("pipx"); err != nil {
		return Plan{Target: target, Unsupported: true, Method: "pipx", Reason: "pipx was not found on PATH. Install pipx, then retry."}
	}
	return Plan{Target: target, Method: "pipx", Command: []string{"pipx", "install", pkg}, Package: pkg}
}

func manualPlan(target Target, reason, docsURL string) Plan {
	return Plan{Target: target, Unsupported: true, Method: "manual", Reason: reason, DocsURL: docsURL}
}

// pinVendorUpdate asks OpenCode and Kimi's installer for the exact release
// the update advisory reported, so the version installed is the one the
// update badge announced.
func pinVendorUpdate(plan Plan, latest string) Plan {
	if plan.Method != "official-installer" || latest == "" {
		return plan
	}
	return plan
}

// targetInstalledCopy points an update at the copy sessions run. A
// vendor self-update runs from that binary rather than whichever copy PATH
// finds first, and npm acts on the global prefix that holds the package with
// that prefix's own Node, which need not be the daemon's npm.
func targetInstalledCopy(goos string, plan Plan, target Target, binaryPath string) Plan {
	if len(plan.Command) == 0 {
		return plan
	}
	switch plan.Method {
	case "official-installer":
		if command, ok := vendorUpdateCommands[target]; ok && plan.Command[0] == command[0] {
			plan.Command = append([]string{binaryPath}, plan.Command[1:]...)
		}
	case "npm":
		prefix, ok := npmPrefixOwningBinary(binaryPath, packageWithoutLatest(plan.Package))
		if !ok || plan.Command[0] != "npm" {
			return plan
		}
		args := slices.Clone(plan.Command[1:])
		for index := 0; index+1 < len(args); index++ {
			if args[index] == "--prefix" {
				args[index+1] = prefix
			}
		}
		plan.PackagePrefix = prefix
		plan.ownerPrefixFound = true
		plan.Command = append(npmCommandForPrefix(goos, prefix), args...)
	}
	return plan
}

// validateNPMUpdateTarget checks the installation an npm update acts on.
// planNPM validated the daemon's Node/npm and global prefix; an update that
// targets another prefix, or that deferred a daemon rejection, is validated
// against the owning prefix and the runtime that will run the update: that
// prefix's own Node and npm when it has them, else the daemon's.
func (s *Service) validateNPMUpdateTarget(ctx context.Context, plan Plan, plannedPrefix string) Plan {
	deferred, runtimeRejected, prefixRejected, ownerFound := plan.deferredReason, plan.daemonRuntimeRejected, plan.daemonPrefixRejected, plan.ownerPrefixFound
	plan.deferredReason, plan.daemonRuntimeRejected, plan.daemonPrefixRejected, plan.ownerPrefixFound = "", false, false, false
	if plan.Method != "npm" || plan.Unsupported {
		return plan
	}
	reject := func(reason string) Plan {
		plan.Unsupported = true
		plan.Reason = reason
		return plan
	}
	if !samePath(plan.PackagePrefix, plannedPrefix) || prefixRejected {
		if !ownerFound && deferred != "" {
			return reject(deferred)
		}
		writable := false
		if s.pathWritable != nil && plan.PackagePrefix != "" {
			writable, _ = s.pathWritable.PathWritable(ctx, plan.PackagePrefix)
		}
		if !writable {
			return reject(fmt.Sprintf("The npm prefix %s that holds the %s sessions run is not writable by the current user. Update it manually; Tallpine will not use sudo.", plan.PackagePrefix, plan.Target))
		}
	}
	if len(plan.Command) > 1 && plan.Command[0] != "npm" {
		// npmCommandForPrefix chose this prefix's own Node and npm.
		minimum := minimumNodeVersionForTarget(plan.Target)
		node, nodeOK := s.toolVersion(ctx, plan.Command[0], "--version")
		_, npmOK := s.toolVersion(ctx, plan.Command[0], plan.Command[1], "--version")
		if !nodeOK || !npmOK {
			return reject(fmt.Sprintf("Tallpine could not validate the Node.js and npm in %s that update %s.", plan.PackagePrefix, plan.Target))
		}
		if !versionAtLeast(node, minimum) {
			return reject(fmt.Sprintf("Node.js %s+ is required to update %s with the Node in %s; found %s.", formatToolVersion(minimum), plan.Target, plan.PackagePrefix, formatToolVersion(node)))
		}
	} else if runtimeRejected {
		// npm from PATH runs the update on the daemon's own Node.
		return reject(deferred)
	}
	return plan
}

// toolVersion runs a version probe and parses its first line.
func (s *Service) toolVersion(ctx context.Context, argv ...string) ([3]int, bool) {
	output, err := runProbe(ctx, s.commands, localProbeTimeout, false, argv)
	if err != nil {
		return [3]int{}, false
	}
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	return parseToolVersion(strings.TrimSpace(line))
}

func (s requestPlanner) resolveAgentMethod(target Target, method string, operation AgentOperation) (Plan, error) {
	for _, plan := range s.agentMethodPlans(target, operation) {
		if plan.Method != method {
			continue
		}
		if plan.Unsupported {
			return Plan{}, fmt.Errorf("%w: install method %q is not available: %s", ErrInstallMethod, method, plan.Reason)
		}
		return plan, nil
	}
	return Plan{}, fmt.Errorf("%w: unknown install method %q for %s", ErrInstallMethod, method, target)
}

func (s *Service) resolveAgentMethod(target Target, method string) (Plan, error) {
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		return Plan{}, err
	}
	return planner.resolveAgentMethod(target, method, AgentOperationInstall)
}
