package daemon

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/lifecycle"
)

// processSweepInterval paces the sweep for processes ended sessions left
// running; the first pass runs shortly after start.
const processSweepInterval = 10 * time.Minute

// sweepSessionProcesses stops processes that sessions ended before this
// daemon started (or ended while their reap was cut short) left running.
func sweepSessionProcesses(ctx context.Context, lcm *lifecycle.Manager) {
	wait := 30 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		lcm.SweepEndedSessionProcesses(ctx)
		lcm.RearmAutoCloses(ctx) // B498: rechecks are in memory; a restart forgets them
		wait = processSweepInterval
	}
}
