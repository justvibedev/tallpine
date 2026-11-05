package daemon

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"tallpine.eu/tallpine/backend/internal/datadirlock"
)

const (
	// reclaimWait is how long a killed holder gets for the OS to drop its lock.
	reclaimWait = 5 * time.Second
	// reclaimMargin is how far past its own stop-by time a holder must still
	// hold the lock before it counts as hung: its exit and the kernel dropping
	// the flock take a moment.
	reclaimMargin = 2 * time.Second
	// reclaimStableGap separates the two reads of the lock file. A new holder
	// rewrites the file right after it takes the flock, so a holder read twice
	// this far apart, with the lock held throughout, is not mid-handover.
	reclaimStableGap = 250 * time.Millisecond
)

// killProcess hard-kills pid (SIGKILL; TerminateProcess on Windows). Tests swap it.
var killProcess = func(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// reclaimFromStoppingHolder runs once the whole lock wait has passed (B564).
// A holder that marked the lock file as stopping wrote the time by which its
// shutdown backstop will have forced it out; still holding the lock well past
// that time, it is hung, so it is killed and the lock taken. Until that time
// it may be in a healthy, slow quit, so this keeps waiting for the lock first.
// A holder without the mark may be a healthy daemon on this data dir with its
// own port and run file (#3716) and is left alone, as is one whose stop-by
// time lies beyond maxStopWait (a skewed clock). It returns a nil lock when
// it did not reclaim, and the caller then refuses to start.
func reclaimFromStoppingHolder(ctx context.Context, dataDir string, maxStopWait time.Duration, log *slog.Logger) (*datadirlock.Lock, error) {
	// Re-check first so a holder that exited just now is never confused with a
	// process that reused its PID.
	lock, err := datadirlock.Acquire(ctx, dataDir, 0)
	if !errors.Is(err, datadirlock.ErrLocked) {
		return lock, err
	}
	holder := datadirlock.ReadHolder(dataDir)
	if holder.PID <= 0 || holder.PID == os.Getpid() {
		return nil, nil
	}
	if holder.StopBy.IsZero() {
		log.Warn("data dir holder has not started shutting down; leaving it running", "holder_pid", holder.PID)
		return nil, nil
	}
	untilKill := time.Until(holder.StopBy.Add(reclaimMargin))
	if untilKill > maxStopWait {
		log.Warn("data dir holder's stop deadline is too far off; leaving it running", "holder_pid", holder.PID, "stop_by", holder.StopBy)
		return nil, nil
	}
	if untilKill > 0 {
		log.Warn("data dir holder is still within its shutdown deadline; waiting for it", "holder_pid", holder.PID, "wait", untilKill.Round(time.Millisecond))
	}
	lock, err = datadirlock.Acquire(ctx, dataDir, max(untilKill, reclaimStableGap))
	if !errors.Is(err, datadirlock.ErrLocked) {
		return lock, err
	}
	if again := datadirlock.ReadHolder(dataDir); !again.StopBy.Equal(holder.StopBy) || again.PID != holder.PID {
		return nil, nil // the lock changed hands meanwhile
	}
	log.Error("data dir holder is hung past its shutdown deadline; killing it", "holder_pid", holder.PID, "stop_by", holder.StopBy)
	if err := killProcess(holder.PID); err != nil {
		log.Error("could not kill the hung data dir holder", "holder_pid", holder.PID, "err", err)
		return nil, nil
	}
	lock, err = datadirlock.Acquire(ctx, dataDir, reclaimWait)
	if errors.Is(err, datadirlock.ErrLocked) {
		return nil, nil
	}
	if err == nil {
		log.Info("data dir lock reclaimed from a hung predecessor", "holder_pid", holder.PID)
	}
	return lock, err
}

// reclaimMaxStopWait caps how much longer than its lock wait a daemon waits
// for a stopping holder's deadline: a holder's whole shutdown backstop plus
// slack. A stop-by time further out comes from a skewed clock.
func reclaimMaxStopWait(wait time.Duration) time.Duration {
	return wait + shutdownBackstopFloor + shutdownBackstopGrace + reclaimMargin
}
