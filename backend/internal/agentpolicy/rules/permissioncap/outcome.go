package permissioncap

import (
	"encoding/json"
	"os"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/pkg/agentruntime"
)

// Outcome is the permission cap decided for a session and the effects its
// seams apply. The zero Outcome (level none) restricts nothing.
type Outcome struct {
	level Level
	live  func() Level
}

// Record implements agentpolicy.Outcome. The level and its source are already
// in the decision record; seams add the requested and effective mode.
func (Outcome) Record() map[string]any { return nil }

// Of returns the permission cap of a decision; the zero Decision yields the
// no-restriction Outcome.
func Of(d agentpolicy.Decision) Outcome {
	o := Outcome{level: None}
	if out, ok := d.Outcome(Name); ok {
		if typed, ok := out.(Outcome); ok {
			o = typed
		}
	}
	o.live = func() Level {
		raw, ok := d.Live(Name, FieldMax)
		if !ok {
			return None
		}
		var v string
		_ = json.Unmarshal(raw, &v)
		return parse(v)
	}
	return o
}

// Level is the frozen cap level of the decision.
func (o Outcome) Level() Level { return o.level }

// ForbidsBypass reports whether bypass / full access is ruled out.
func (o Outcome) ForbidsBypass() bool { return o.level != None && o.level != "" }

// Lower lowers mode to what the decision's cap allows. It never raises a mode.
// Use it at launch-time seams.
func (o Outcome) Lower(harness domain.AgentHarness, mode domain.PermissionMode) domain.PermissionMode {
	return lower(o.level, harness, mode)
}

// LowerLive is Lower under the stricter of the decision's cap and the cap in
// force right now. Per-turn seams use it so a managed tightening applies to a
// chat that was decided before it.
func (o Outcome) LowerLive(harness domain.AgentHarness, mode domain.PermissionMode) domain.PermissionMode {
	level := o.level
	if o.live != nil {
		level = stricter(level, o.live())
	}
	return lower(level, harness, mode)
}

// ForbidsBypassLive is ForbidsBypass under the stricter of the frozen and live caps.
func (o Outcome) ForbidsBypassLive() bool {
	level := o.level
	if o.live != nil {
		level = stricter(level, o.live())
	}
	return level != None && level != ""
}

// PinnedCodexSandbox is the `--sandbox` value a Codex terminal launch must pass,
// or "" when no cap applies. The approval flags alone leave sandbox_mode to the
// user's ~/.codex/config.toml, which may say danger-full-access.
func (o Outcome) PinnedCodexSandbox() string {
	if o.ForbidsBypass() {
		return "workspace-write"
	}
	return ""
}

// ExplicitClaudeDefault reports whether a Claude launch must name its default
// mode: with no --permission-mode Claude takes its start mode from the user's
// settings, which may say bypassPermissions.
func (o Outcome) ExplicitClaudeDefault() bool { return o.ForbidsBypass() }

// ClaudeMode is the Claude --permission-mode value for a launch under this cap
// ("" for no flag). Under a cap that forbids bypass, Claude's default is named
// explicitly: with no mode Claude resolves its start mode from the user's
// settings, which may say bypassPermissions. live selects the stricter of the
// frozen and live caps (a later MDM push) instead of the frozen one.
func (o Outcome) ClaudeMode(mode domain.PermissionMode, live bool) string {
	lowered, forbids := o.Lower(domain.HarnessClaudeCode, mode), o.ExplicitClaudeDefault()
	if live {
		lowered, forbids = o.LowerLive(domain.HarnessClaudeCode, mode), o.ForbidsBypassLive()
	}
	if name := agentruntime.ClaudePermissionMode(agentruntime.PermissionPolicy(lowered)); name != "" {
		return name
	}
	if forbids {
		return "default"
	}
	return ""
}

// lower holds the per-harness mapping.
//
// Codex "default" (and anything unrecognised) runs with full access today, so
// under a cap it is mapped to the cap's own level. Other harnesses keep
// "default" because it makes them ask, which is already stricter than the cap.
// Kiro v3 auto maps to the scoped dev-shell preset, so it needs no special case.
func lower(level Level, harness domain.AgentHarness, mode domain.PermissionMode) domain.PermissionMode {
	if level == None || level == "" {
		return mode
	}
	if !mode.Valid() {
		// Adapters treat an unknown mode as their baseline, which for Codex is
		// full access.
		mode = domain.PermissionModeDefault
	}
	switch level {
	case Auto:
		switch mode {
		case domain.PermissionModeBypassPermissions, domain.PermissionModeAuto:
			if mode == domain.PermissionModeAuto {
				return mode
			}
			return domain.PermissionModeAuto
		case domain.PermissionModeDefault, "":
			if harness == domain.HarnessCodex {
				return domain.PermissionModeAuto
			}
		}
	case AcceptEdits:
		switch mode {
		case domain.PermissionModeBypassPermissions, domain.PermissionModeAuto:
			return domain.PermissionModeAcceptEdits
		case domain.PermissionModeDefault, "":
			if harness == domain.HarnessCodex {
				return domain.PermissionModeAcceptEdits
			}
		}
	}
	return mode
}

// BuildCacheDirs are the per-user build and package caches, relative to the
// home directory, that a confined agent still has to write to for normal work
// (go build, npm/pnpm/bun install, cargo, gradle, maven, xcodebuild). Only cache
// subdirectories are listed, never a whole tool home: ~/.bun/bin, ~/.cargo/bin,
// ~/.gradle/init.d or ~/.m2/settings.xml would let a write persist into later
// unconfined runs. Both the
// Claude sandbox fence and the capped Codex sandbox grant them, so the two
// confinements cost the same productivity.
var BuildCacheDirs = []string{
	"Library/Caches", "Library/Developer/Xcode/DerivedData", ".cache", "go/pkg/mod", ".npm",
	".cargo/registry", ".gradle/caches", ".gradle/wrapper", ".gradle/daemon", ".gradle/native", ".m2/repository", ".bun/install/cache",
	".rustup/downloads", ".rustup/tmp", "Library/pnpm/store", ".local/share/pnpm/store", ".tallpine/golangci-cache",
}

// CodexWorkspaceWriteExtras is what any Codex workspace-write sandbox gets on
// top of the workspace itself, with or without a permission cap: the network
// (workspace-write has none by default, which breaks git push, gh, package
// installs and the tallpine CLI) and the writable roots a normal build needs,
// the build caches under home plus the linked worktree's git dirs, taken from
// repo, the recorded project repository (empty grants none). The caller
// decides it is in workspace-write; the cap plays no part. Roots are empty
// without a home. Terminal launch, chat thread start and per-turn settings share it.
func CodexWorkspaceWriteExtras(home, repo, workspace string) (networkAccess bool, roots []string) {
	if home == "" {
		return true, nil
	}
	roots = make([]string, 0, len(BuildCacheDirs)+2)
	for _, rel := range BuildCacheDirs {
		roots = append(roots, home+"/"+rel)
	}
	// The test-slot pool scripts/go test queues in (same dir as the Claude fence).
	dataDir := os.Getenv(brand.Env("DATA_DIR"))
	if dataDir == "" {
		dataDir = home + "/.tallpine/data"
	}
	roots = append(roots, dataDir+"/verify-slots")
	return true, dropSymlinks(append(roots, GitWritableRoots(repo, workspace)...))
}

// dropSymlinks removes roots that are symlinks. A writable root covers its own
// directory entry, so the agent can replace one with a link, and a sandbox that
// resolves its roots would then open the link's target. A missing root is kept:
// the sandbox tolerates it and the tool creates it later.
func dropSymlinks(roots []string) []string {
	kept := roots[:0]
	for _, root := range roots {
		if st, err := os.Lstat(root); err == nil && st.Mode()&os.ModeSymlink != 0 {
			continue
		}
		kept = append(kept, root)
	}
	return kept
}
