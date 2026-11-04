// Package brand is the single source of truth for the product identity of this
// fork (Tallpine). Everything that could collide with an official
// Agent Orchestrator install on the same machine — the CLI binary name, the
// state directory, the daemon port, the tmux socket, launchd labels, the skill
// name, the MCP server name and the URL schemes — is defined here and read by
// the rest of the backend. The Electron side mirrors these values in
// frontend/src/shared/brand.ts; keep the two files in sync
// (frontend/src/shared/brand.test.ts and brand_test.go pin the values).
//
// Environment variables use the TALLPINE_ prefix (TALLPINE_DATA_DIR,
// TALLPINE_RUN_FILE, TALLPINE_PORT, TALLPINE_SESSION_ID, ...).
package brand

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ProductName is the user-facing name of the application.
	ProductName = "Tallpine"
	// CLIName is the canonical command-line binary name. `tp` is an alias and
	// `LegacyCLIName` only serves migration and legacy recognition; there is deliberately no `ao`.
	CLIName = "tallpine"
	// CLIAlias is the short alias installed next to CLIName.
	CLIAlias = "tp"
	// BundleID is the macOS bundle identifier / Windows AppUserModelID / Linux
	// app id.
	BundleID = "ee.justdev.tallpine"
	// HomeDirName is the per-user state root directory name under $HOME.
	HomeDirName = ".tallpine"
	// EnvPrefix prefixes every environment variable this product reads or
	// injects into agent sessions.
	EnvPrefix = "TALLPINE_"
	// DefaultPort is the default loopback daemon port. The official install
	// uses 3001; this fork uses 3101 so both daemons can run at once.
	DefaultPort = 3101
	// TmuxSocketName is the private tmux server socket (`tmux -L tallpine`).
	TmuxSocketName = "tallpine"
	// LaunchdLabelPrefix prefixes every launchd label the product registers.
	LaunchdLabelPrefix = "ee.justdev.tallpine"
	// MobileWakeLabel is the launchd label of the closed-lid wake helper.
	MobileWakeLabel = LaunchdLabelPrefix + ".mobile-wake"
	// URLScheme is the OS-registered URL scheme handled by the desktop app.
	URLScheme = "tallpine"
	// InAppLinkScheme is the renderer-only link scheme (tallpine://sessions/...).
	InAppLinkScheme = "tallpine"
	// SkillName is the agent skill the product installs under <dataDir>/skills.
	SkillName = "using-tallpine"
	// MCPServerName is the name of the product's built-in MCP server.
	MCPServerName = "tallpine"
	// SessionLabel and SpareLabel are the Docker labels the container reaper
	// uses (`--label tallpine.session=<id>`, `--label tallpine.spare=true`). They are
	// brand-specific because session ids (<prefix>-<n>) repeat across installs
	// that register the same repo; sharing the official `ao.session` key would
	// let either install force-remove the other's containers.
	SessionLabel = "tallpine.session"
	// SpareLabel marks a container the reaper must never remove.
	SpareLabel = "tallpine.spare"
	// DaemonServiceName identifies this daemon in /healthz and /readyz probes so
	// a probe never mistakes an official AO daemon for ours.
	DaemonServiceName = "tallpine-daemon"
	// BranchPrefix prefixes every git branch this product generates for a
	// session (`tp/<session>/root`). The session id (<project>-<n>) repeats
	// across installs that register the same repo, so sharing the official
	// `ao/` namespace could make both installs claim the same branch.
	BranchPrefix = "tp"
	// LegacyBranchPrefix is the pre-rebrand branch namespace. Sessions that
	// already own an `ao/...` branch keep working; nothing new is generated
	// under it.
	LegacyBranchPrefix = "ao"
	// HookMarker is the substring identifying hook entries written by this
	// product (`tallpine hooks <agent> <event>`). Installers match and clean up only
	// entries that carry it, never the official `ao hooks` entries.
	HookMarker = CLIName + " hooks "
)

// Legacy* pin the pre-rename (Vibe Orchestrator) identity. They are fixed
// names, not the current brand, so the migrator, the startup guard and the
// compat readers keep working after the flip.
const (
	LegacyCLIName        = "vo"
	LegacyProductName    = "Vibe Orchestrator"
	LegacyTmuxSocketName = "vo"
	LegacyHomeDirName    = ".vo"
	LegacyBundleID       = "ee.justdev.vibe-orchestrator"
	// LegacyDaemonServiceName is what the pre-rename daemon reports in /healthz.
	LegacyDaemonServiceName  = "vibe-orchestrator-daemon"
	LegacyLaunchdLabelPrefix = "ee.justdev.vo"
	LegacyURLScheme          = "vo-app"
	LegacyInAppLinkScheme    = "vo"
	LegacySkillName          = "using-vo"
	LegacySessionLabel       = "vo.session"
	LegacySpareLabel         = "vo.spare"
	LegacyBranchPrefixVO     = "vo"
	NextHomeDirName          = HomeDirName
	NextCLIName              = CLIName
	NextBranchPrefix         = BranchPrefix
	NextInAppLinkScheme      = InAppLinkScheme
	NextSessionLabel         = SessionLabel
	NextSpareLabel           = SpareLabel
)

// BranchPrefixes lists the session-branch namespaces this product generates or
// recognises as its own: the current prefix, then the pre-rename one.
func BranchPrefixes() []string { return []string{BranchPrefix, LegacyBranchPrefixVO} }

// LabelPair is one session/spare Docker label pair.
type LabelPair struct{ Session, Spare string }

// LabelPairs lists every Docker label pair the reaper honours: the current
// pair first, then the post-rename one.
func LabelPairs() []LabelPair {
	return []LabelPair{{SessionLabel, SpareLabel}, {LegacySessionLabel, LegacySpareLabel}}
}

// HookPrefixes returns the command prefix and its twin under the other CLI
// name ("tallpine hooks codex " <-> "vo hooks codex "), so hook entries
// written before or after the rename are both recognised. A prefix with
// neither CLI name is returned alone.
func HookPrefixes(prefix string) []string {
	for _, pair := range [][2]string{{CLIName + " hooks ", LegacyCLIName + " hooks "}, {LegacyCLIName + " hooks ", CLIName + " hooks "}} {
		if rest, ok := strings.CutPrefix(prefix, pair[0]); ok {
			return []string{prefix, pair[1] + rest}
		}
	}
	return []string{prefix}
}

// IsHookCommand reports whether command starts with prefix under either CLI name.
func IsHookCommand(command, prefix string) bool {
	for _, p := range HookPrefixes(prefix) {
		if strings.HasPrefix(command, p) {
			return true
		}
	}
	return false
}

// Env returns the full environment variable name for suffix, e.g.
// Env("DATA_DIR") == "TALLPINE_DATA_DIR".
func Env(suffix string) string { return EnvPrefix + suffix }

// LookupEnv looks up the variable named by suffix (e.g. "DATA_DIR"),
// reading TALLPINE_<suffix>.
func LookupEnv(suffix string) (string, bool) {
	return os.LookupEnv(EnvPrefix + suffix)
}

// Getenv is LookupEnv without the presence flag.
func Getenv(suffix string) string {
	v, _ := LookupEnv(suffix)
	return v
}

// HomeDir returns the absolute per-user state root (~/.tallpine).
func HomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, HomeDirName), nil
}

// OfficialHomeDirName is the state root of the official Agent Orchestrator
// install. It exists only so `vo import ao` can find that install's data to
// READ; nothing in this product may ever write under it.
const OfficialHomeDirName = ".ao"

// OfficialDataDir returns the official install's data dir (~/.ao/data), the
// default read-only source of `vo import ao`.
func OfficialDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, OfficialHomeDirName, "data"), nil
}
