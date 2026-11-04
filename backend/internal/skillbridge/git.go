package skillbridge

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/process"
)

// GitCommitted returns an Options.Committed check backed by the checkout's HEAD
// commit. Outside a git repository (or without git) every path counts as
// committed, so nothing is flagged that cannot be judged.
func GitCommitted(workspace string) func(rel string) bool {
	run := func(args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return process.CommandContext(ctx, "git", append([]string{"-C", workspace}, args...)...).Run() //nolint:gosec // fixed git subcommands on the user's own checkout
	}
	if run("rev-parse", "--verify", "-q", "HEAD") != nil {
		return func(string) bool { return true }
	}
	return func(rel string) bool { return run("cat-file", "-e", "HEAD:"+rel) == nil }
}
