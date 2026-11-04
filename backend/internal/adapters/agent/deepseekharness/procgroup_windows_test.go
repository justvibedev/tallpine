//go:build windows

package deepseekharness

import (
	"context"
	"os/exec"
)

func combinedOutputGrouped(ctx context.Context, name string, args, env []string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // test helper
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return out, 0, err
}
