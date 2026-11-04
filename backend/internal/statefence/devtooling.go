package statefence

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/process"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// DarwinUserTempDir is the user's per-user temp dir (confstr
// DARWIN_USER_TEMP_DIR, /var/folders/xx/yyyy/T), where Apple's developer-tool
// shims keep their xcrun_db cache. A var so tests can pin it.
var DarwinUserTempDir = func() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := process.CommandContext(ctx, "getconf", "DARWIN_USER_TEMP_DIR").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// DevToolingWritePaths are the writes local dev tooling needs under the fence
// (the protectState devTooling=allow setting): the xcrun cache files (xcrun_db-* and the xcrun_db they are renamed to) only, not
// the temp dir. Without them every git, clang and cgo build fails with
// "couldn't create cache file .../xcrun_db-*" because the shims in /usr/bin
// write the cache there regardless of $TMPDIR.
func DevToolingWritePaths() []string {
	dir := strings.TrimRight(DarwinUserTempDir(), "/")
	if dir == "" {
		return nil
	}
	// Seatbelt matches resolved paths and cannot realpath a glob, so list the
	// resolved form too: /var is a symlink to /private/var.
	dirs := []string{dir}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
		dirs = append(dirs, resolved)
	} else if strings.HasPrefix(dir, "/var/") {
		dirs = append(dirs, "/private"+dir)
	}
	// The shim writes xcrun_db-<random> and renames it onto xcrun_db (B463):
	// without the final name allowed, the rename fails and the warning
	// pollutes every git invocation's output.
	paths := make([]string, 0, 2*len(dirs))
	for _, d := range dirs {
		paths = append(paths, filepath.Join(d, "xcrun_db-*"), filepath.Join(d, "xcrun_db"))
	}
	return paths
}

// DevToolingSocketDirs are the session-private dirs a shell may bind unix
// sockets under: the launch TMPDIR, and CLAUDE_CODE_TMPDIR when set. Claude
// Code exports the shell's TMPDIR as <CLAUDE_CODE_TMPDIR>/claude-<uid>, a child
// of one of these, and the sandbox allows each entry as a subpath.
//
// A project env may override either variable, so an entry counts only when it
// resolves (symlinks followed) to a path strictly under <dataDir>/tmp, the
// per-session scratch root. Anything
// else (/tmp, the home, the data dir, the per-user /var/folders temp dir) is
// dropped: /tmp holds the production tmux socket and the IPC aliases, which a
// fenced shell must not reach.
func DevToolingSocketDirs(env map[string]string, dataDir string) []string {
	if dataDir == "" {
		return nil
	}
	// The sandbox resolves symlinks and the shell can write its own scratch dir,
	// so compare resolved paths and emit the resolved one.
	root, err := filepath.EvalSymlinks(filepath.Join(filepath.Clean(dataDir), sessionscratch.TempRoot))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, key := range []string{"TMPDIR", "CLAUDE_CODE_TMPDIR"} {
		d := strings.TrimSpace(env[key])
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		d, err = filepath.EvalSymlinks(filepath.Clean(d))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, d)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
			strings.HasPrefix(rel, sessionscratch.TrashDir) || slices.Contains(dirs, d) {
			continue
		}
		dirs = append(dirs, d)
	}
	return dirs
}
