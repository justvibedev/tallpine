package cli

import (
	"fmt"
	"strings"
)

// validateBacklogFlags checks the --backlog flag family: --backlog is for
// project workers, and the flags that modify it need it.
func (opts *spawnOptions) validateBacklogFlags() error {
	backlog := strings.TrimSpace(opts.backlog) != ""
	switch {
	case backlog && (opts.kind == "orchestrator" || opts.standalone):
		return usageError{fmt.Errorf("--backlog is for project worker sessions")}
	case opts.plan != "" && !backlog:
		return usageError{fmt.Errorf("--plan requires --backlog")}
	case opts.plan != "" && opts.plan != "in-place" && opts.plan != "separate":
		return usageError{fmt.Errorf("--plan must be in-place or separate")}
	case opts.backlogForce && !backlog:
		return usageError{fmt.Errorf("--force requires --backlog")}
	case opts.allowDuplicate && !backlog:
		return usageError{fmt.Errorf("--allow-duplicate requires --backlog")}
	}
	return nil
}
