package migratevo

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/process"
)

// Runner runs an external command in dir (empty = current) and returns its
// combined output. Tests and rehearsals inject fakes.
type Runner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// ExecRunner is the production Runner.
func ExecRunner(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := process.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Hook is a migration step owned by a later PR (launchd, daemon start). The
// default is nil, which the migrator reports as "not wired" and leaves unmarked.
type Hook func(ctx context.Context, plan *Plan) error

// Env bundles the roots with every side-effecting dependency.
type Env struct {
	Roots
	Run Runner
	// Processes lists running command lines; AliveTmux reports whether the
	// `tmux -L <socket>` server answers. Both feed the preconditions.
	Processes func(ctx context.Context) ([]string, error)
	AliveTmux func(ctx context.Context) bool
	SelfPID   int
	// StopOrphans SIGTERMs processes running out of <source>/data/tmp/ before
	// the process check. Kill (default SIGTERM) and OrphanSettle (wait after
	// signalling) are injectable.
	StopOrphans  bool
	Kill         func(pid int) error
	OrphanSettle time.Duration
	Out          io.Writer
	// LaunchdHook and DaemonHook are steps 6 and 8, wired by the cut-over PR.
	LaunchdHook Hook
	DaemonHook  Hook
}
