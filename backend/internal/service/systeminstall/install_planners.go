package systeminstall

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// planFor resolves the install Plan for target on the current platform,
// probing PATH via s.executables so tests can inject deterministic results.
func (s *Service) planFor(target Target) Plan {
	switch target {
	case TargetTmux:
		return s.planTmux()
	case TargetGH:
		return s.planGH()
	case TargetClaude:
		return s.planNPM(TargetClaude, "@anthropic-ai/claude-code")
	case TargetCodex:
		return s.planNPM(TargetCodex, "@openai/codex")
	case TargetCloudflared:
		return s.planCloudflared()
	default:
		return Plan{Target: target, Unsupported: true, Reason: "unknown install target"}
	}
}

func (s *Service) planTmux() Plan {
	switch s.goos {
	case "windows":
		return Plan{
			Target: TargetTmux, Unsupported: true,
			Reason: "tmux is not required on Windows; Tallpine uses the built-in ConPTY terminal runtime instead.",
		}
	case "darwin":
		return s.planBrew(TargetTmux, "tmux")
	case "linux":
		return s.planLinuxPackage(TargetTmux, func(string) string { return "tmux" })
	default:
		return Plan{Target: TargetTmux, Unsupported: true, Reason: "tmux installation is not supported on this platform."}
	}
}

func (s *Service) planGH() Plan {
	switch s.goos {
	case "windows":
		return s.planWinget(TargetGH, "GitHub.cli")
	case "darwin":
		return s.planBrew(TargetGH, "gh")
	case "linux":
		return s.planLinuxPackage(TargetGH, func(mgr string) string {
			if mgr == "pacman" {
				return "github-cli"
			}
			return "gh"
		})
	default:
		return Plan{Target: TargetGH, Unsupported: true, Reason: "gh installation is not supported on this platform."}
	}
}

// planCloudflared mirrors planGH: Cloudflare publishes cloudflared through
// Homebrew and winget, and Linux distributions package it too.
//
// Deliberately not a downloaded binary. Fetching a release archive ourselves
// would mean pinning per-platform URLs, verifying checksums and clearing
// macOS quarantine before executing it — a fourth install shape this package
// does not have, for a tool the three it does have already cover. The one
// curl-piped target here is opencode, and only because that is the vendor's
// own documented installer.
func (s *Service) planCloudflared() Plan {
	switch s.goos {
	case "windows":
		return s.planWinget(TargetCloudflared, "Cloudflare.cloudflared")
	case "darwin":
		return s.planBrew(TargetCloudflared, "cloudflared")
	case "linux":
		return s.planLinuxPackage(TargetCloudflared, func(string) string { return "cloudflared" })
	default:
		return Plan{
			Target: TargetCloudflared, Unsupported: true,
			Reason: "cloudflared installation is not supported on this platform.",
		}
	}
}

func (s *Service) planNPM(target Target, pkg string) Plan {
	if !IsAgentTarget(target) {
		return (requestPlanner{Service: s}).planNPM(target, pkg)
	}
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		return Plan{Target: target, Unsupported: true, Method: "npm", Reason: "npm and Node.js capabilities could not be inspected."}
	}
	return planner.planNPM(target, pkg)
}

func (p requestPlanner) planNPM(target Target, pkg string) Plan {
	s := p.Service
	if _, err := s.executables.LookPath("npm"); err != nil {
		return Plan{
			Target: target, Unsupported: true,
			Method: "npm", Reason: "npm was not found on PATH. Install Node.js from https://nodejs.org first, then retry.",
		}
	}
	plan := Plan{Target: target, Command: []string{"npm", "install", "-g", pkg}, Method: "npm", Package: pkg}
	if IsAgentTarget(target) {
		if p.capabilities == nil {
			plan.Unsupported = true
			plan.Reason = "npm and Node.js capabilities could not be inspected."
			return plan
		}
		npm := p.capabilities.NPM
		if npm.Err != nil {
			plan.Unsupported = true
			plan.Reason = "npm and Node.js capabilities could not be inspected."
			return plan
		}
		nodeVersion, nodeOK := parseToolVersion(npm.NodeVersion)
		_, npmOK := parseToolVersion(npm.NPMVersion)
		runtimeReason := ""
		if !nodeOK || !npmOK {
			runtimeReason = "npm and Node.js versions could not be validated."
		} else if minimumNode := minimumNodeVersionForTarget(target); !versionAtLeast(nodeVersion, minimumNode) {
			runtimeReason = fmt.Sprintf("Node.js %s+ is required for %s's npm recipe; found %s.", formatToolVersion(minimumNode), target, npm.NodeVersion)
		}
		prefix := npm.GlobalPrefix
		prefixWritable := npm.PrefixWritable
		plan.PackagePrefix = prefix
		if prefix == "" {
			plan.Unsupported = true
			plan.Reason = "npm's global install prefix could not be resolved."
			return plan
		}
		if runtimeReason != "" {
			// The daemon's Node/npm cannot run this recipe. Reported before any
			// prefix problem, as it always was; an update may still use another.
			plan.Unsupported = true
			plan.Reason = runtimeReason
			plan.daemonRuntimeRejected = true
			plan.daemonPrefixRejected = !prefixWritable
			return plan
		}
		if !prefixWritable {
			plan.Unsupported = true
			plan.daemonPrefixRejected = true
			plan.Reason = fmt.Sprintf("npm's global prefix %s is not writable by the current user. Configure a user-owned prefix; Tallpine will not use sudo.", prefix)
			return plan
		}
		if s.goos == "windows" {
			plan.ExpectedDestination = prefix
		} else {
			plan.ExpectedDestination = filepath.Join(prefix, "bin")
		}
	}
	return plan
}

func minimumNodeVersionForTarget(target Target) [3]int {
	switch target {
	case TargetClaudeCode:
		return [3]int{22, 0, 0}
	default:
		return [3]int{16, 0, 0}
	}
}

func formatToolVersion(version [3]int) string {
	if version[2] != 0 {
		return fmt.Sprintf("%d.%d.%d", version[0], version[1], version[2])
	}
	if version[1] != 0 {
		return fmt.Sprintf("%d.%d", version[0], version[1])
	}
	return strconv.Itoa(version[0])
}

func parseToolVersion(value string) ([3]int, bool) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	fields := strings.Split(value, ".")
	if len(fields) == 0 || len(fields) > 3 {
		return [3]int{}, false
	}
	var parsed [3]int
	for index, field := range fields {
		part, err := strconv.Atoi(field)
		if err != nil || part < 0 {
			return [3]int{}, false
		}
		parsed[index] = part
	}
	return parsed, true
}

func versionAtLeast(got, minimum [3]int) bool {
	for index := range got {
		if got[index] != minimum[index] {
			return got[index] > minimum[index]
		}
	}
	return true
}

func (s *Service) planBrew(target Target, pkg string) Plan {
	if !IsAgentTarget(target) {
		return (requestPlanner{Service: s}).planBrew(target, pkg)
	}
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		return Plan{Target: target, Unsupported: true, Method: "homebrew", Reason: "Homebrew packages could not be inspected."}
	}
	return planner.planBrew(target, pkg)
}

func (p requestPlanner) planBrew(target Target, pkg string) Plan {
	return p.planHomebrew(target, pkg, false)
}

func (p requestPlanner) planHomebrew(target Target, pkg string, cask bool) Plan {
	s := p.Service
	if _, err := s.executables.LookPath("brew"); err != nil {
		return Plan{
			Target: target, Unsupported: true,
			Method: "homebrew", Reason: "Homebrew was not found on PATH. Install it from https://brew.sh first, then retry.",
		}
	}
	var installed bool
	if IsAgentTarget(target) {
		if p.capabilities == nil || p.capabilities.Homebrew.Err != nil {
			return Plan{Target: target, Unsupported: true, Method: "homebrew", Reason: "Homebrew packages could not be inspected."}
		}
		homebrew := p.capabilities.Homebrew
		prefix := homebrew.Prefix
		if prefix == "" {
			return Plan{Target: target, Unsupported: true, Method: "homebrew", Reason: "Homebrew's installation prefix could not be resolved."}
		}
		if !homebrew.PrefixWritable {
			return Plan{Target: target, Unsupported: true, Method: "homebrew", Reason: fmt.Sprintf("Homebrew's prefix %s is not writable by the current user. Repair Homebrew ownership; Tallpine will not use sudo.", prefix)}
		}
		if cask {
			installed = homebrewPackageInstalled(homebrew.Casks, pkg)
		} else {
			installed = homebrewPackageInstalled(homebrew.Formulae, pkg)
		}
	}
	verb := "install"
	if installed {
		verb = "reinstall"
	}
	command := []string{"brew", verb}
	if cask {
		command = append(command, "--cask")
	}
	command = append(command, pkg)
	return Plan{Target: target, Command: command, Method: "homebrew", Package: pkg, PackageCask: cask}
}

func homebrewPackageInstalled(inventory map[string]bool, pkg string) bool {
	if inventory[pkg] {
		return true
	}
	if slash := strings.LastIndexByte(pkg, '/'); slash >= 0 {
		return inventory[pkg[slash+1:]]
	}
	return false
}

func (s *Service) planWinget(target Target, id string) Plan {
	if _, err := s.executables.LookPath("winget"); err != nil {
		plan := Plan{Target: target, Unsupported: true, Reason: "winget was not found on PATH."}
		if IsAgentTarget(target) {
			plan.Method = "winget"
		}
		return plan
	}
	command := []string{"winget", "install", "-e", "--id", id}
	if IsAgentTarget(target) {
		command = append(command, "--silent", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity")
		return Plan{Target: target, Command: command, Method: "winget", Package: id}
	}
	return Plan{Target: target, Command: command}
}

func withDocs(plan Plan, docsURL string) Plan {
	plan.DocsURL = docsURL
	return plan
}

// linuxPackageManagers is probed in this fixed order; the first one found on
// PATH is used.
var linuxPackageManagers = []string{"apt-get", "dnf", "pacman", "zypper", "apk"}

// planLinuxPackage resolves a Linux install command for target via the first
// available package manager. pkgFor lets a target use a different package
// name on a given manager (e.g. gh is "github-cli" on pacman).
//
// Tallpine deliberately never elevates privileges on the user's behalf (no auto
// sudo, no pkexec): every one of apt-get/dnf/pacman/zypper install requires
// root, so running the resolved command as the desktop user is guaranteed to
// fail with a permission error. Rather than expose a button that always
// fails, this always resolves as Unsupported on Linux. Command carries the
// exact argv and displayCommand adds sudo for the user-facing manual remedy.
func (s *Service) planLinuxPackage(target Target, pkgFor func(mgr string) string) Plan {
	for _, mgr := range linuxPackageManagers {
		if _, err := s.executables.LookPath(mgr); err != nil {
			continue
		}
		argv := linuxInstallArgv(mgr, pkgFor(mgr))
		return Plan{
			Target: target, Command: argv, Manager: mgr, NeedsRoot: true, Unsupported: true,
			Reason: "Tallpine cannot ask for your administrator password. Run the command below in a terminal.",
		}
	}
	return Plan{
		Target: target, Unsupported: true,
		Reason: fmt.Sprintf(
			"No supported Linux package manager (%s) was found.",
			strings.Join(linuxPackageManagers, ", "),
		),
	}
}

func linuxInstallArgv(mgr, pkg string) []string {
	switch mgr {
	case "apt-get":
		return []string{"apt-get", "install", "-y", pkg}
	case "dnf":
		return []string{"dnf", "install", "-y", pkg}
	case "pacman":
		return []string{"pacman", "-S", "--noconfirm", pkg}
	case "zypper":
		return []string{"zypper", "install", "-y", pkg}
	case "apk":
		return []string{"apk", "add", pkg}
	default:
		return nil
	}
}

func (s *Service) planBrewCask(target Target, pkg string) Plan {
	if !IsAgentTarget(target) {
		return (requestPlanner{Service: s}).planBrewCask(target, pkg)
	}
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		return Plan{Target: target, Unsupported: true, Method: "homebrew", Reason: "Homebrew packages could not be inspected."}
	}
	return planner.planBrewCask(target, pkg)
}

func (p requestPlanner) planBrewCask(target Target, pkg string) Plan {
	return p.planHomebrew(target, pkg, true)
}
