package agentlaunch

import (
	"os"
	"path/filepath"
	"strconv"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// EnvFileDir is the directory holding a tmux launch's env file between the
// daemon writing it and the pane sourcing and deleting it. The file carries the
// session's scoped token, so the Security mode fence denies this directory.
func EnvFileDir() string {
	return filepath.Join(os.TempDir(), brand.CLIName+"-launch-"+strconv.Itoa(os.Getuid()))
}
