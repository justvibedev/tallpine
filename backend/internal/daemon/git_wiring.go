package daemon

import (
	"tallpine.eu/tallpine/backend/internal/adapters/gitcli"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Services run git through ports.Git; the daemon fills it with the git CLI
// adapter at package load, before any service starts.
func init() { ports.SetDefaultGit(gitcli.New()) }
