package ports

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync/atomic"
)

// GitError is a failed git invocation. Detail is the trimmed diagnostic text
// git printed, kept apart from Err so services can classify the failure
// without parsing the message.
type GitError struct {
	Args   []string
	Detail string
	Err    error
}

func (e *GitError) Error() string {
	msg := "git " + strings.Join(e.Args, " ")
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *GitError) Unwrap() error { return e.Err }

// Git runs the git CLI against a working tree or repository directory. Every
// call is non-interactive, hook-free, and cannot be redirected by the
// caller's own GIT_DIR, GIT_INDEX_FILE or GIT_WORK_TREE environment.
type Git interface {
	// Output runs git in dir and returns its stdout.
	Output(ctx context.Context, dir string, args ...string) (string, error)
	// OutputEnv is Output with extra environment entries appended after the
	// scrub, such as a scratch GIT_INDEX_FILE.
	OutputEnv(ctx context.Context, dir string, env []string, args ...string) (string, error)
	// OutputCapped drains stdout but keeps at most limit bytes, reporting
	// whether it cut the output.
	OutputCapped(ctx context.Context, dir string, limit int, args ...string) (string, bool, error)
	// Command builds the hardened command without starting it, for callers
	// that pipe one git process into another or tune the process themselves.
	// An empty dir omits -C, for commands such as clone.
	Command(ctx context.Context, dir string, args ...string) *exec.Cmd
	// Plain is Command without the read-only hardening: hooks, the user's
	// diff and fsmonitor settings keep working, so it suits commands that
	// write (init, add, commit, clone, worktree) and tool-like fetch/push.
	// The environment is still scrubbed of the repository variables.
	Plain(ctx context.Context, dir string, args ...string) *exec.Cmd
	// Run is Output over Plain.
	Run(ctx context.Context, dir string, args ...string) (string, error)
	// Isolated builds a git command that runs in dir with exactly env as its
	// environment and no added arguments, for callers that pin GIT_DIR and
	// GIT_WORK_TREE themselves.
	Isolated(ctx context.Context, dir string, env []string, args ...string) *exec.Cmd
	// TopLevel returns the working-tree root containing dir.
	TopLevel(ctx context.Context, dir string) (string, error)
	// RemoteURL returns remote's configured URL in dir.
	RemoteURL(ctx context.Context, dir, remote string) (string, error)
	// CommonDir returns the absolute common git directory of dir's repository.
	CommonDir(ctx context.Context, dir string) (string, error)
}

// CappedBuffer keeps a bounded prefix of what is written to it and drops the
// rest, so large diffs never become an unbounded in-memory buffer.
type CappedBuffer struct {
	Limit     int
	buffer    bytes.Buffer
	truncated bool
}

// Write implements io.Writer; it always reports the full length written.
func (w *CappedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := w.Limit - w.buffer.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = w.buffer.Write(p[:remaining])
	}
	if remaining < len(p) {
		w.truncated = true
	}
	return written, nil
}

// String returns the retained prefix.
func (w *CappedBuffer) String() string { return w.buffer.String() }

// Truncated reports whether any output was dropped.
func (w *CappedBuffer) Truncated() bool { return w.truncated }

// gitScrubbedEnv names the variables that redirect git away from its -C
// directory. A session's own environment may carry them.
var gitScrubbedEnv = []string{"GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE"}

// GitEnv returns environ without the repository-redirecting variables and with
// the non-interactive settings added. Every git child process Tallpine starts
// for repository work should use it.
func GitEnv(environ []string) []string {
	out := make([]string, 0, len(environ)+2)
	for _, entry := range environ {
		scrubbed := false
		for _, name := range gitScrubbedEnv {
			if strings.HasPrefix(entry, name+"=") {
				scrubbed = true
				break
			}
		}
		if !scrubbed {
			out = append(out, entry)
		}
	}
	return append(out, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
}

var defaultGit atomic.Pointer[Git]

// SetDefaultGit registers the git runner services use. The daemon sets it once
// at wiring time; tests set it in their TestMain.
func SetDefaultGit(g Git) { defaultGit.Store(&g) }

// DefaultGit returns the registered runner. It panics when none was set, so a
// missing wiring step fails loudly instead of silently skipping git.
func DefaultGit() Git {
	g := defaultGit.Load()
	if g == nil {
		panic("ports: no default Git registered; call ports.SetDefaultGit during wiring")
	}
	return *g
}
