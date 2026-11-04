//go:build !windows

package systemexec

import (
	"context"
	"os/exec"

	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

func commandContext(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	return tallpineprocess.CommandContext(ctx, name, args...), nil //nolint:gosec // Callers supply server-owned argv.
}

func refreshExecutablePath() {}
