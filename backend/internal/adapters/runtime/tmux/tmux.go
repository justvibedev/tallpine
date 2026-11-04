// Package tmux implements ports.Runtime using tmux sessions on Darwin/Linux.
package tmux

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
	"tallpine.eu/tallpine/backend/internal/tmuxbin"
)

const (
	defaultTimeout    = 5 * time.Second
	defaultChunkBytes = 16 * 1024
	// defaultEnterDelay mirrors conpty's ptyInputEnterDelay: a pause after pasting
	// a non-empty message, before the trailing Enter, so a large multiline paste
	// does not absorb the Enter and leave the prompt unsubmitted (issue #2342).
	defaultEnterDelay = 300 * time.Millisecond
	// defaultReapGrace is how long Destroy waits between SIGTERM and SIGKILL when
	// reaping a pane's leftover background processes, giving them a chance to
	// exit cleanly (release ports) before being forced (issue #2523). It is a
	// ceiling, not a fixed wait: reapPollInterval decides how soon a pane that
	// is already empty lets Destroy return.
	defaultReapGrace = 5 * time.Second
	// reapPollInterval is how often the reap rechecks for survivors while the
	// grace runs. A plain shell exits within a tick or two, so Destroy returns
	// in roughly this long instead of always burning the full grace — which the
	// DELETE handler blocks on, and the user sees as a tab that will not close.
	reapPollInterval = 50 * time.Millisecond
)

var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var getenv = os.Getenv

// commandTimeoutFromEnv returns the per-command tmux timeout: TALLPINE_TMUX_COMMAND_TIMEOUT
// (a Go duration such as "20s") when set and valid, else defaultTimeout. A busy
// machine can take longer than the default to answer tmux, which fails session
// creation; a bad value falls back rather than disabling the bound.
func commandTimeoutFromEnv() time.Duration {
	raw := strings.TrimSpace(getenv("TALLPINE_TMUX_COMMAND_TIMEOUT"))
	if raw == "" {
		return defaultTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultTimeout
	}
	return d
}

// Options configures a tmux Runtime. Every field has a sensible default (see
// New), so the zero value is usable.
type Options struct {
	Binary       string        // default configured/bundled/system tmux resolution
	LegacyBinary string        // default system tmux from PATH when SocketName is set; used only for pre-private-socket sessions
	SocketName   string        // default $TALLPINE_TMUX_SOCKET_NAME; empty uses tmux's machine-wide default socket
	Shell        string        // default $SHELL else /bin/sh
	Timeout      time.Duration // default 5s
	ChunkSize    int           // default 16*1024
	EnterDelay   time.Duration // pause after pasting a non-empty message before pressing Enter; default defaultEnterDelay. Conpty already does this (ptyInputEnterDelay); tmux lacked it, so a large multiline paste could absorb the trailing Enter and leave the prompt unsubmitted (issue #2342).
	ReapGrace    time.Duration // grace between SIGTERM and SIGKILL when reaping a pane's leftover background processes on Destroy; default defaultReapGrace.
}

// Runtime runs agent sessions inside tmux sessions, driving them via the tmux
// CLI. It implements ports.Runtime.
type Runtime struct {
	binary         string
	legacyBinary   string
	socketName     string
	shell          string
	timeout        time.Duration
	chunkSize      int
	enterDelay     time.Duration
	reapGrace      time.Duration
	runner         runner
	reapSessions   func(ctx context.Context, pids []int, grace time.Duration)
	socketMu       sync.RWMutex
	sessionSockets map[string]string
}

var _ ports.Runtime = (*Runtime)(nil)

var _ ports.FencedRuntimeProber = (*Runtime)(nil)

var _ ports.Attacher = (*Runtime)(nil)

type runtimeEffectFailure struct {
	err     error
	handle  ports.RuntimeHandle
	effect  ports.RuntimeEffectOutcome
	cleanup ports.RuntimeCleanupOutcome
}

func (e runtimeEffectFailure) Error() string { return e.err.Error() }

func (e runtimeEffectFailure) Unwrap() error { return e.err }

func (e runtimeEffectFailure) PossibleHandle() ports.RuntimeHandle { return e.handle }

func (e runtimeEffectFailure) EffectOutcome() ports.RuntimeEffectOutcome { return e.effect }

func (e runtimeEffectFailure) CleanupOutcome() ports.RuntimeCleanupOutcome { return e.cleanup }

func tmuxCreateFailure(err error) error {
	return runtimeEffectFailure{err: err, effect: ports.RuntimeEffectNone, cleanup: ports.RuntimeCleanupNotAttempted}
}

func tmuxPossibleCreateFailure(err error, handle ports.RuntimeHandle, cleanup ports.RuntimeCleanupOutcome) error {
	return runtimeEffectFailure{err: err, handle: handle, effect: ports.RuntimeEffectPossible, cleanup: cleanup}
}

type runner interface {
	Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := tallpineprocess.CommandContext(ctx, name, args...)
	cmd.Env = append(append([]string(nil), os.Environ()...), env...)
	// Run from a stable directory, not whatever the daemon process's cwd happens
	// to be. The first tmux CLI call auto-starts tmux's persistent server, which
	// inherits ITS launching process's cwd and keeps it for the server's entire
	// lifetime, regardless of what any later `new-session -c <dir>` asks for
	// (issue #2775). A packaged desktop build can start the daemon with its cwd
	// inside a Squirrel/ShipIt staging directory that the very next auto-update
	// deletes, permanently pinning the tmux server to a path that no longer
	// exists. os.TempDir() outlives app bundle swaps and update staging dirs, so
	// pinning here keeps the server cwd valid across the app's lifetime.
	cmd.Dir = stableRunDir()
	return cmd.CombinedOutput()
}

// stableRunDir returns the directory execRunner.Run pins the tmux CLI to.
//
// os.TempDir() is the preferred answer (see execRunner.Run), but it returns
// $TMPDIR verbatim without checking that it exists. A stale or bogus TMPDIR
// would then make exec fail with "chdir <dir>: no such file or directory" on
// EVERY tmux command, taking the whole runtime down for exactly the reason
// #2775 did: a cwd that no longer exists. So stat the candidates and degrade
// rather than hard-fail. The last resort is the empty string, which leaves
// cmd.Dir unset so the command inherits the daemon's own cwd: that is the
// pre-fix behavior and merely risks the poisoned-server race the pin avoids,
// which the retry in verifyPaneWorkingDirectory already tolerates.
func stableRunDir() string {
	candidates := []string{os.TempDir()}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, home)
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

// New builds a tmux Runtime, filling unset Options with defaults: binary from
// Tallpine's configured/bundled/system resolver; shell from $SHELL (else /bin/sh); and the
// default timeout and output chunk size.
func New(opts Options) *Runtime {
	binary := opts.Binary
	if binary == "" {
		resolution, err := tmuxbin.Resolve()
		if err == nil {
			binary = resolution.Path
		} else if configured := strings.TrimSpace(getenv("TALLPINE_TMUX_BINARY")); configured != "" {
			// Keep the configured path on failure so packaged builds fail closed
			// when they eventually execute it instead of selecting machine tmux.
			binary = configured
		} else {
			binary = "tmux"
		}
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = commandTimeoutFromEnv()
	}
	shellPath := opts.Shell
	if shellPath == "" {
		shellPath = getenv("SHELL")
	}
	if shellPath == "" {
		shellPath = "/bin/sh"
	}
	chunkSize := opts.ChunkSize
	if chunkSize <= 0 {
		chunkSize = defaultChunkBytes
	}
	enterDelay := opts.EnterDelay
	if enterDelay <= 0 {
		enterDelay = defaultEnterDelay
	}
	reapGrace := opts.ReapGrace
	if reapGrace <= 0 {
		reapGrace = defaultReapGrace
	}
	socketName := strings.TrimSpace(opts.SocketName)
	if socketName == "" {
		socketName = strings.TrimSpace(getenv("TALLPINE_TMUX_SOCKET_NAME"))
	}
	legacyBinary := opts.LegacyBinary
	if socketName == "" {
		legacyBinary = binary
	} else if legacyBinary == "" {
		// Sessions created before Tallpine introduced its private socket were started by
		// the machine tmux from PATH. Use that matching client for the legacy
		// default socket: tmux's client/server protocol is not guaranteed across
		// versions, so Tallpine's pinned bundled client may be unable to adopt them.
		if systemTmux, err := exec.LookPath("tmux"); err == nil {
			legacyBinary = systemTmux
		}
	}
	return &Runtime{
		binary:         binary,
		legacyBinary:   legacyBinary,
		socketName:     socketName,
		shell:          shellPath,
		timeout:        timeout,
		chunkSize:      chunkSize,
		enterDelay:     enterDelay,
		reapGrace:      reapGrace,
		runner:         execRunner{},
		reapSessions:   killSessionsByPID,
		sessionSockets: make(map[string]string),
	}
}

func (e commandError) Error() string {
	if e.output == "" {
		return e.err.Error()
	}
	return e.err.Error() + ": " + e.output
}

func (e commandError) Unwrap() error { return e.err }
