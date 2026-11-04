package persistenthost

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"time"
)

// stateDirCheckInterval is how often a host checks its state dir still exists.
var stateDirCheckInterval = 5 * time.Second

// watchStateDir shuts the host down once its state dir is deleted. A host
// outlives its daemon on purpose (restarts, updates, a closed app with a turn
// still running), so a missing daemon is never a reason to exit; a deleted
// state dir is: nothing can ever reconnect, so exit instead of leaking (B330).
func (h *host) watchStateDir(ctx context.Context, dir string) {
	ticker := time.NewTicker(stateDirCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.shutdown:
			return
		case <-ticker.C:
			if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
				h.shutdownOnce.Do(func() { close(h.shutdown) })
				return
			}
		}
	}
}
