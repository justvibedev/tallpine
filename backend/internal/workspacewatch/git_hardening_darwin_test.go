//go:build darwin

package workspacewatch

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFSEventsIgnoreBatchRunsNoAgentFsmonitor(t *testing.T) {
	w := agentFsmonitor(t)
	f := &fseventsFilter{root: w.Path, realRoot: w.Path}
	f.checkIgnored(context.Background(), []string{filepath.Join(w.Path, "f")})
	if w.Ran() {
		t.Fatal("the daemon's git ran the agent's fsmonitor")
	}
}
