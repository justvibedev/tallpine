package systeminstall

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// officialManagedRecipe runs a vendor's own installer into one Tallpine release
// directory, pinned to the release the update picked, so the update never
// touches the files a running session uses. Each recipe is read from the
// vendor's script: where it installs, how it takes a version, and what keeps
// it from editing the user's shell files or their own copy.
type officialManagedRecipe struct {
	// binRel is where the installer puts the CLI inside the release directory.
	binRel string
	// env and args configure one install into dir; pin is a bare version
	// ("1.2.3"), or "" for the installer's latest.
	env  func(dir, pin string) []string
	args func(dir, pin string) []string
}

// managedInstallerPath is the PATH an official installer runs with: system
// tools only. Several installers look for an existing copy on PATH (to skip
// an install they think is current, or to migrate or replace it), which must
// never be the user's own copy or Tallpine's current release.
const managedInstallerPath = "/usr/bin:/bin:/usr/sbin:/sbin"

// officialInstallerEnv is the environment every recipe shares: a private HOME
// inside the release, so any profile or config an installer writes lands
// there instead of in the user's files, a PATH of system tools, and a plain
// sh as SHELL. Installers pick the profile they append PATH to from SHELL.
func officialInstallerEnv(dir string, extra ...string) []string {
	return append([]string{"HOME=" + filepath.Join(dir, ".install-home"), "PATH=" + managedInstallerPath, "SHELL=/bin/sh"}, extra...)
}

func noArgs(string, string) []string { return nil }

var officialManagedRecipes = map[Target]officialManagedRecipe{
	// Codex keeps each release in its own folder under $CODEX_HOME; a
	// private CODEX_HOME keeps the user's config, auth and their own
	// installs out of it.
	TargetCodex: {
		binRel: filepath.Join("bin", "codex"),
		env: func(dir, pin string) []string {
			release := strings.TrimPrefix(pin, "v")
			if release == "" {
				release = "latest"
			}
			return officialInstallerEnv(dir,
				"CODEX_HOME="+filepath.Join(dir, "codex-home"),
				"CODEX_INSTALL_DIR="+filepath.Join(dir, "bin"),
				"CODEX_RELEASE="+release,
				"CODEX_NON_INTERACTIVE=1")
		},
		args: noArgs,
	},
}

// officialManagedCask is a Homebrew cask whose upgrade deletes files a
// running session can still load. Tallpine updates it beside running sessions with
// the vendor's installer instead, into a release of its own; the cask itself
// is left as it is.
type officialManagedCask struct {
	cask, url, shell string
}

// codexInstallScriptURL is Codex's official macOS and Linux installer.
const codexInstallScriptURL = "https://chatgpt.com/codex/install.sh"

// officialManagedCasks: Codex's cask also ships codex-resources, which an
// upgrade removes while sessions may still read it.
var officialManagedCasks = map[Target]officialManagedCask{
	TargetCodex: {cask: "codex", url: codexInstallScriptURL, shell: "sh"},
}

// officialManagedCaskFor returns the cask entry when plan upgrades it.
func officialManagedCaskFor(goos string, plan Plan) (officialManagedCask, bool) {
	entry, ok := officialManagedCasks[plan.Target]
	return entry, ok && goos == "darwin" && plan.Method == "homebrew" && plan.PackageCask && plan.Package == entry.cask
}

// officialManagedRecipeFor returns the recipe that installs plan's official
// installer as a managed release, on the platforms it was read for.
func (s *Service) officialManagedRecipeFor(plan Plan) (officialManagedRecipe, bool) {
	_, cask := officialManagedCaskFor(s.goos, plan)
	if plan.Method != "official-installer" && !cask || plan.Script == nil || (s.goos != "darwin" && s.goos != "linux") {
		return officialManagedRecipe{}, false
	}
	recipe, ok := officialManagedRecipes[plan.Target]
	if !ok || s.installScripts == nil {
		return officialManagedRecipe{}, false
	}
	return recipe, true
}

// officialManagedScript returns the vendor installer that installs plan as a
// release of Tallpine's own: installScript, plan's official installer, or for a
// cask in officialManagedCasks the installer Tallpine runs instead of the cask.
func (s requestPlanner) officialManagedScript(plan Plan, installScript *ports.InstallScriptCommand) (*ports.InstallScriptCommand, bool) {
	if s.managedRoot == "" {
		return nil, false
	}
	script := installScript
	if entry, ok := officialManagedCaskFor(s.goos, plan); ok {
		script = s.planShellInstaller(plan.Target, entry.url, entry.shell).Script
	}
	if script == nil {
		return nil, false
	}
	plan.Script = script
	if _, ok := s.officialManagedRecipeFor(plan); !ok {
		return nil, false
	}
	return script, true
}

// officialManagedUpdate reports whether one of plans, a target's install
// plans, is an official installer Tallpine runs into a release of its own. Such an
// update maintains the copy sessions run wherever the vendor's installer put
// the user's copy, since it never writes there.
func (s *Service) officialManagedUpdate(plans []Plan) bool {
	if s.managedRoot == "" {
		return false
	}
	for _, plan := range plans {
		if plan.Method != "official-installer" || plan.Unsupported {
			continue
		}
		if _, ok := s.officialManagedRecipeFor(plan); ok {
			return true
		}
	}
	return false
}

// installManagedRelease installs one release of plan into dir, with the
// vendor's installer when plan has an official recipe and with the package
// manager otherwise, and returns where the CLI landed inside dir.
func (s *Service) installManagedRelease(ctx context.Context, plan Plan, pkg, pin, cli, dir string, env []string, out io.Writer) (string, error) {
	if recipe, ok := s.officialManagedRecipeFor(plan); ok {
		if err := s.runOfficialManagedInstall(ctx, plan, recipe, pin, dir, env, out); err != nil {
			return "", err
		}
		return recipe.binRel, nil
	}
	argv, extraEnv := managedInstallCommand(plan.Method, pkg, pin, dir)
	if argv == nil {
		return "", installFailed(fmt.Sprintf("%s cannot install a side-by-side copy.", installMethodLabel(plan.Method)), nil)
	}
	_, _ = fmt.Fprintf(out, "Installing %s into %s\n", pkg, dir)
	if err := s.installCommands.RunInstall(ctx, ports.InstallCommand{Argv: argv, Env: append(append([]string(nil), env...), extraEnv...)}, out, out); err != nil {
		return "", installFailed("The install command failed.", err)
	}
	return managedBinRel(s.goos, plan.Method, cli), nil
}

// runOfficialManagedInstall installs one release into dir: the vendor's
// installer script, or the recipe's own download.
func (s *Service) runOfficialManagedInstall(ctx context.Context, plan Plan, recipe officialManagedRecipe, pin, dir string, env []string, out io.Writer) error {
	// The private HOME must exist: installers such as Codex's append
	// their PATH line to a profile in it under set -e, and fail without it.
	if err := os.MkdirAll(filepath.Join(dir, ".install-home"), 0o700); err != nil {
		return fmt.Errorf("create installer home: %w", err)
	}
	recipeEnv := recipe.env(dir, pin)
	command := *plan.Script
	command.Env = append(append(append([]string(nil), env...), command.Env...), recipeEnv...)
	command.Args = recipe.args(dir, pin)
	// Only the recipe's environment reaches the installer: a variable the
	// user exported, such as CODEX_HOME or CODEX_INSTALL_DIR, would
	// otherwise send the install into the user's own files.
	command.Isolated = true
	_, _ = fmt.Fprintf(out, "Installing %s with its official installer into %s\n", plan.Target, dir)
	result, err := s.installScripts.RunInstallScript(ctx, command, out, out)
	if result.SHA256 != "" {
		_, _ = fmt.Fprintf(out, "\nsource: %s\nsha256: %s\n", command.URL, result.SHA256)
	}
	return err
}
