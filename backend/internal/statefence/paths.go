// Package statefence holds the paths a Security mode file fence protects and
// opens: Tallpine state an agent must not read, the writable set outside the
// worktree, hidden credential homes and the dev-tooling openings. Harness
// adapters (Claude Code settings, the macOS seatbelt wrapper) render them.
package statefence

import (
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// State is the Tallpine state an agent must not read: secret files and
// whole directories.
type State struct {
	Files []string
	Dirs  []string
}

// StatePaths is the state rooted at the run file's directory: the
// install token, the session key scoped tokens are MACed under, the database
// and its WAL files, the GitHub PAT, the browser runtime key, the Electron
// profile (cookies), the mobile gateway keys and device tokens, and every
// session's chat host descriptor (provider-host address and bearer token). The
// whole chat-hosts directory is denied, the session's own included: only the
// daemon and the unsandboxed host process read it for recovery and attach, so
// no shell command needs it, and another session's copy would let it drive
// that session's provider. dataDir is
// the launch's TALLPINE_DATA_DIR; empty means the default "data" directory
// under the root.
func StatePaths(runFile, dataDir string) State {
	root := filepath.Dir(runFile)
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = filepath.Join(root, "data")
	}
	tokenPath := daemontoken.Path(runFile)
	db := filepath.Join(dataDir, "ao.db")
	return State{
		Files: []string{
			tokenPath,
			daemontoken.SessionKeyPath(tokenPath),
			db, db + "-wal", db + "-shm", db + "-journal",
			filepath.Join(dataDir, "github-pat.json"),
			filepath.Join(dataDir, "browser-runtime.key"),
		},
		Dirs: []string{filepath.Join(root, "electron"), filepath.Join(dataDir, "mobile"), persistenthost.HostsRoot(dataDir)},
	}
}

// DockerSocketPaths are the Docker daemon sockets a project that opted in to
// securityMode.allowDocker may reach from the fence. Reaching one is full host
// access, which is why it is a user-only opt-in.
func DockerSocketPaths(env map[string]string) []string {
	paths := []string{"/var/run/docker.sock"}
	if home := strings.TrimSpace(env["HOME"]); home != "" {
		paths = append(paths, filepath.Join(home, ".docker", "run", "docker.sock"))
	}
	return paths
}

// RestoreEnv is the part of a session's launch env the fence depends on,
// rebuilt for a restore, which does not carry the launch env: the run file, the
// data dir (a custom TALLPINE_DATA_DIR moves the database, PAT and mobile keys)
// and the session's scratch temp dir, so the restored session is fenced and can
// write where the fresh launch could.
func RestoreEnv(runFile, dataDir string, session ports.SessionRef) map[string]string {
	env := map[string]string{brand.Env("RUN_FILE"): runFile}
	// The ssh agent socket the fresh launch's env carried; without it a
	// restored hardened session loses git over ssh.
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		env["SSH_AUTH_SOCK"] = sock
	}
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = strings.TrimSpace(session.DataDir)
	}
	if dataDir == "" {
		return env
	}
	env[brand.Env("DATA_DIR")] = dataDir
	env[brand.Env("SESSION_ID")] = session.ID
	if tmp := sessionscratch.TempDir(dataDir, domain.SessionID(session.ID)); tmp != "" {
		env["TMPDIR"] = tmp
		// Claude Code otherwise falls back to the shared /tmp/claude-<uid> (B429).
		env["CLAUDE_CODE_TMPDIR"] = tmp
		env[sessionscratch.TempPrefixEnv] = sessionscratch.TempPrefix(tmp)
	}
	return env
}

// DataDir is the launch's data dir: TALLPINE_DATA_DIR, else "data" beside
// the run file.
func DataDir(env map[string]string) string {
	dataDir := strings.TrimSpace(env[brand.Env("DATA_DIR")])
	if runFile := strings.TrimSpace(env[brand.Env("RUN_FILE")]); dataDir == "" && runFile != "" {
		dataDir = filepath.Join(filepath.Dir(runFile), "data")
	}
	return dataDir
}

// WritePaths are the writable paths outside the worktree: the session's
// temp dir, its own artifacts dir, and the user's build and package caches.
func WritePaths(env map[string]string, devTooling bool) []string {
	var paths []string
	if tmp := strings.TrimSpace(env["TMPDIR"]); tmp != "" {
		paths = append(paths, tmp)
	}
	dataDir := DataDir(env)
	if dataDir != "" {
		// verify-slots is scripts/test-slot's queue (go test and scripts/verify).
		// Only this session's own artifacts dir, never data/artifacts: that holds
		// every other session's deliverables. Without a session id none is granted.
		if id := strings.TrimSpace(env[brand.Env("SESSION_ID")]); id != "" {
			paths = append(paths, sessionartifacts.Dir(dataDir, domain.SessionID(id)))
		}
		paths = append(paths, filepath.Join(dataDir, "verify-slots"))
	}
	// testenv creates its tmux socket dirs under /tmp/tp-tmux (short paths).
	// Only that subdir: /tmp also holds live Tallpine IPC aliases and the
	// production tmux socket, which a fenced agent must not re-point.
	paths = append(paths, "/tmp/tp-tmux", "/private/tmp/tp-tmux")
	home := strings.TrimSpace(env["HOME"])
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home != "" {
		for _, rel := range permissioncap.BuildCacheDirs {
			paths = append(paths, filepath.Join(home, rel))
		}
	}
	if devTooling {
		paths = append(paths, DevToolingWritePaths()...)
	}
	return paths
}
