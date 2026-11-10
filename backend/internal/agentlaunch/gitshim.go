package agentlaunch

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
)

const (
	// EnvGitGuardRoot names the checkout the git shim protects: the session's
	// registered project. `tallpine git-exec` refuses remote and shared-config
	// writes only for repositories under it, so scratch repos and tools that keep
	// their own (Go's module cache runs `git remote add`) are untouched.
	EnvGitGuardRoot = brand.EnvPrefix + "GIT_GUARD_ROOT"
	// EnvAllowSharedGitConfig lets a refused write through when one command sets
	// it to 1. It is for changes the owner explicitly asked for. Launches never
	// set it, and it is dropped from project and inherited env.
	EnvAllowSharedGitConfig = brand.EnvPrefix + "ALLOW_SHARED_GIT_CONFIG"
)

// EnvGitShimTried lists the "pid|dir" PATH entries the shim already handed
// off to. exec keeps the pid, so a wrapper git that calls the shim's own path
// comes back with the same pid and the shim skips what it already tried and
// moves on to the next git (or stops with an error) instead of looping. A hook
// or other child git has another pid and still sees every directory.
const EnvGitShimTried = brand.EnvPrefix + "GIT_SHIM_TRIED"

// EnvGitShimHops counts the shim passes in one process tree, whatever the pid
// and whichever path (plain exec or `tallpine git-exec`) a pass took. It bounds
// wrappers that run the shim as a child or reach it through git-exec; ordinary
// nesting (git -> hook -> git) stays below gitShimMaxHops. The cap is small on
// purpose: a wrapper that calls the shim twice per run fans out into 2^cap
// processes before every leaf stops.
const (
	EnvGitShimHops = brand.EnvPrefix + "GIT_SHIM_HOPS"
	gitShimMaxHops = 8
)

// gitShimParent is the runtime directory the git shim directories live in.
const gitShimParent = "git-shim"

// EnsureGitShim writes the `git` shim for sessions of the daemon binary exe and
// returns its directory, or "" where the sh shim cannot run (Windows). The shim
// is an accident guard, not a security boundary: a git started by absolute path
// never meets it, and the Security mode fence stays the OS-enforced one.
func EnsureGitShim(exe, dataDir string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", nil
	}
	if dataDir == "" {
		return "", fmt.Errorf("data directory is required")
	}
	sum := sha256.Sum256([]byte(exe))
	dir := filepath.Join(dataDir, "runtime", gitShimParent, fmt.Sprintf("%x", sum[:8]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := writeShim(dir, "git", gitShimScript(exe, dir)); err != nil {
		return "", err
	}
	return dir, nil
}

// gitShimScript hands only commands with a literal `remote` or `config` argument
// to `tallpine git-exec`; everything else execs the real git with no Go process
// in between. The real git is the first one on PATH outside this directory, and
// PATH is left as it is so git's own children stay behind the shim.
func gitShimScript(exe, dir string) string {
	return "#!/bin/sh\n" +
		"hops=${" + EnvGitShimHops + ":-0}\n" +
		`if [ "$hops" -ge ` + fmt.Sprint(gitShimMaxHops) + ` ] 2>/dev/null; then` + "\n" +
		`  echo "` + brand.CLIName + ` git: git not found on PATH outside the session shim (the git on PATH leads back to the shim)" >&2` + "\n" +
		"  exit 127\n" +
		"fi\n" +
		EnvGitShimHops + "=$((hops + 1))\n" +
		"export " + EnvGitShimHops + "\n" +
		`case " $* " in` + "\n" +
		`*" remote "*|*" config "*) exec ` + singleQuote(exe) + " git-exec " + singleQuote(dir) + ` -- "$@" ;;` + "\n" +
		"esac\n" +
		"shim=" + singleQuote(dir) + "\n" +
		"me=$$\n" +
		"tried=${" + EnvGitShimTried + ":-}\n" +
		"set -f\n" +
		"IFS=:\n" +
		"for d in $PATH; do\n" +
		`  [ "$d" = "$shim" ] && continue` + "\n" +
		"  skip=\n" +
		`  for t in $tried; do [ "$t" = "$me|$d" ] && skip=1; done` + "\n" +
		`  [ -n "$skip" ] && continue` + "\n" +
		`  if [ -f "$d/git" ] && [ -x "$d/git" ]; then` + "\n" +
		"    unset IFS\n" +
		"    " + EnvGitShimTried + `="$tried:$me|$d"` + "\n" +
		"    export " + EnvGitShimTried + "\n" +
		`    exec "$d/git" "$@"` + "\n" +
		"  fi\n" +
		"done\n" +
		`echo "` + brand.CLIName + ` git: git not found on PATH outside the session shim (a wrapper git may lead back to it)" >&2` + "\n" +
		"exit 127\n"
}

func singleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// restoreGitShim moves the git shim back in front of the directories the launch
// binary step added, which may hold the real git (/opt/homebrew/bin). It sits
// right behind pinnedDir, which holds no git. A PATH with no shim is unchanged.
func restoreGitShim(parts []string, pinnedDir string) []string {
	shim := slices.IndexFunc(parts, func(dir string) bool { return filepath.Base(filepath.Dir(dir)) == gitShimParent })
	if shim < 0 {
		return parts
	}
	head := 0
	if pinnedDir != "" && parts[0] == pinnedDir {
		head = 1
	}
	if shim <= head {
		return parts
	}
	dir := parts[shim]
	return slices.Insert(slices.Delete(slices.Clone(parts), shim, shim+1), head, dir)
}
