// Package releaseinstall installs a locally built release for the main-health
// orchestrator. The orchestrator's shell runs in the protectState sandbox, which
// cannot read the daemon token or database that scripts/release-local needs, so
// the daemon verifies the build and runs the installer itself, detached.
package releaseinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/process"
	"tallpine.eu/tallpine/backend/internal/releasesigner"
)

// ErrRefused marks a request refused by a gate (bad sha, path outside the build
// root, bad signature), as opposed to a failure to run the installer.
var ErrRefused = errors.New("release install refused")

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Result describes the started install.
type Result struct {
	SHA string
	App string
	Log string
	PID int
}

// Installer verifies a build under StateDir/release-builds and starts its
// scripts/release-local install. The function fields are test seams.
type Installer struct {
	StateDir string
	TeamID   string
	// Codesign runs codesign with args and returns its combined output.
	Codesign func(ctx context.Context, args ...string) (string, error)
	// Clone copies the app bundle src to dst (an APFS clone, preserving the signature).
	Clone func(ctx context.Context, src, dst string) error
	// Start runs argv detached in its own session with env and returns its pid.
	Start func(argv, env []string, logPath string) (int, error)
}

// New returns an Installer for the state root that runs the real tools.
func New(stateDir string) *Installer {
	// The build-stamped Team ID (daemon-build.sh); the env var overrides it.
	team := releasesigner.ExpectedTeamID
	if env := strings.TrimSpace(os.Getenv("TALLPINE_RELEASE_TEAM_ID")); env != "" {
		team = env
	}
	return &Installer{StateDir: stateDir, TeamID: team, Codesign: runCodesign, Clone: cloneApp, Start: startDetached}
}

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, args...))
}

// Install verifies the build for sha and starts the detached install; notify is
// the session told the outcome ("" for none).
func (in *Installer) Install(ctx context.Context, sha, notify string) (Result, error) {
	app, script, err := in.locate(sha)
	if err != nil {
		return Result{}, err
	}
	// Authenticate the bytes used: clone the app into a private dir, verify and
	// read the manifest from that clone, and install the clone, so a writer to
	// the build tree cannot swap anything after the check.
	dir, err := newStageDir(in.StateDir)
	if err != nil {
		return Result{}, err
	}
	fail := func(err error) (Result, error) { _ = os.RemoveAll(dir); return Result{}, err }
	clone := filepath.Join(dir, "app", filepath.Base(app))
	if err := os.MkdirAll(filepath.Dir(clone), 0o700); err != nil {
		return fail(err)
	}
	if err := in.Clone(ctx, app, clone); err != nil {
		return fail(fmt.Errorf("clone app for verification: %w", err))
	}
	if err := in.verifySignature(ctx, clone); err != nil {
		return fail(err)
	}
	app = clone
	// Run the staged copy of the bytes just hashed, never the writable build tree.
	if script, err = stageInstallerTree(app, filepath.Dir(filepath.Dir(script)), dir); err != nil {
		return fail(err)
	}
	logDir := filepath.Join(in.StateDir, "release-builds")
	logPath := filepath.Join(logDir, "install-"+sha[:10]+".log")
	// --no-fetch and the skip env: a pinned build tree is not a stale checkout, and
	// the stale check would run git (fetch, diff) unsandboxed in a worktree.
	argv := []string{script, "install", app, "--yes", "--no-fetch", "--log", logPath}
	if notify != "" {
		argv = append(argv, "--notify", notify)
	}
	pid, err := in.Start(argv, append(cleanEnv(), "TALLPINE_RELEASE_SKIP_STALE_CHECK=1"), logPath)
	if err != nil {
		return Result{}, fmt.Errorf("start release install: %w", err)
	}
	return Result{SHA: sha, App: app, Log: logPath, PID: pid}, nil
}

// locate returns the resolved app and installer script of the build for sha,
// refusing anything that does not resolve (symlinks followed) to
// release-builds/<sha>/frontend/out/**/<Product>.app and
// release-builds/<sha>/scripts/release-local.
func (in *Installer) locate(sha string) (app, script string, err error) {
	if !shaPattern.MatchString(sha) {
		return "", "", refuse("sha must be 40 lowercase hex characters")
	}
	root, err := filepath.EvalSymlinks(filepath.Join(in.StateDir, "release-builds"))
	if err != nil {
		return "", "", refuse("no release-builds directory: %v", err)
	}
	build := filepath.Join(root, sha)
	matches, _ := filepath.Glob(filepath.Join(build, "frontend", "out", "*", brand.ProductName+".app"))
	if len(matches) != 1 {
		return "", "", refuse("expected exactly one %s.app under release-builds/%s/frontend/out, found %d", brand.ProductName, sha, len(matches))
	}
	if app, err = within(matches[0], filepath.Join(build, "frontend", "out")); err != nil {
		return "", "", err
	}
	if script, err = within(filepath.Join(build, "scripts", "release-local"), filepath.Join(build, "scripts")); err != nil {
		return "", "", err
	}
	return app, script, nil
}

// within resolves symlinks in path and requires the result to be under dir.
func within(path, dir string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", refuse("%s: %v", path, err)
	}
	if !strings.HasPrefix(resolved, dir+string(filepath.Separator)) {
		return "", refuse("%s resolves to %s, outside %s", path, resolved, dir)
	}
	return resolved, nil
}

// verifySignature requires a valid deep, strict signature that satisfies the
// requirement anchored to Apple's roots for the Team ID. A bare TeamIdentifier
// from codesign -dv is certificate metadata a self-made cert can set.
func (in *Installer) verifySignature(ctx context.Context, app string) error {
	if !releasesigner.TeamIDPattern.MatchString(in.TeamID) {
		return refuse("no valid release Team ID is configured (build-stamped or TALLPINE_RELEASE_TEAM_ID); cannot verify the signer")
	}
	if out, err := in.Codesign(ctx, "--verify", "--deep", "--strict", "-R="+releasesigner.TeamRequirement(in.TeamID), app); err != nil {
		return refuse("codesign verification against Team %s failed: %v: %s", in.TeamID, err, strings.TrimSpace(out))
	}
	return nil
}

func runCodesign(ctx context.Context, args ...string) (string, error) {
	out, err := process.CommandContext(ctx, "/usr/bin/codesign", args...).CombinedOutput()
	return string(out), err
}

// cleanEnv is the daemon's environment minus per-session identity.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "TALLPINE_SESSION") || name == "TALLPINE_PROJECT_ID" {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func cloneApp(ctx context.Context, src, dst string) error {
	if out, err := process.CommandContext(ctx, "/bin/cp", "-c", "-R", src, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("cp -c -R: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
