package daemon

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"
)

// shutdownBackstopFloor keeps a shrunken TALLPINE_SHUTDOWN_TIMEOUT (tests use
// 100ms) from cutting a healthy quit short: parking the agents alone may take
// 15s (parkAgents).
const shutdownBackstopFloor = 30 * time.Second

// shutdownBackstopGrace bounds the backstop's own log line and goroutine dump,
// so a blocked log writer (a full stderr pipe) cannot hold the exit off.
const shutdownBackstopGrace = 5 * time.Second

// exitProcess is os.Exit; tests swap it to observe the backstop firing.
var exitProcess = os.Exit

// shutdownBackstopDeadline is how long the daemon may spend between srv.Run
// returning and exiting before the backstop forces it out (B564). A desktop
// takeover's replacement waits 9T+dataDirLockFixedWait for the data-dir lock
// from when this daemon's /healthz goes dark, at most T (the HTTP drain) before
// srv.Run returns. Exiting by 8T+dataDirLockFixedWait/2 hands the lock over
// with half the fixed wait to spare (89s with the 10s default). Below T=2s the
// floor wins and a replacement can stop waiting first; it then waits on for the
// stop-by time the lock file records (reclaimFromStoppingHolder).
func shutdownBackstopDeadline(shutdownTimeout time.Duration) time.Duration {
	return max(8*shutdownTimeout+dataDirLockFixedWait/2, shutdownBackstopFloor)
}

// shutdownGoroutineDumpPath is where the backstop leaves the stacks of a hung
// shutdown, next to daemon.log.
func shutdownGoroutineDumpPath(dataDir string) string {
	return filepath.Join(dataDir, "logs", "shutdown-hang-goroutines.txt")
}

// armShutdownBackstop forces the process out once deadline passes. A shutdown
// stage blocked forever (a deadlocked store write, B563) would otherwise keep
// the data-dir lock and the LAN listener, and the replacement daemon would give
// up waiting for the lock and leave the app empty. Stop the returned timer to
// disarm it.
func armShutdownBackstop(deadline time.Duration, dataDir string, log *slog.Logger) *time.Timer {
	return time.AfterFunc(deadline, func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			path := shutdownGoroutineDumpPath(dataDir)
			err := writeGoroutineDump(path)
			log.Error("daemon shutdown hung past its deadline; forcing exit",
				"deadline", deadline, "goroutines", path, "dump_err", err)
		}()
		select {
		case <-done:
		case <-time.After(shutdownBackstopGrace):
		}
		exitProcess(1)
	})
}

func writeGoroutineDump(path string) error {
	var buf bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&buf, 2); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o600)
}

// beginShutdown arms the exit backstop and records in the data-dir lock file
// when the backstop will have forced this daemon out, so a replacement still
// finding the lock held well after that may kill this process (B564).
func (b *boot) beginShutdown() {
	deadline := shutdownBackstopDeadline(b.cfg.ShutdownTimeout)
	b.shutdownBackstop = armShutdownBackstop(deadline, b.cfg.DataDir, b.log)
	if err := b.dataDirLock.MarkStopping(time.Now().Add(deadline + shutdownBackstopGrace)); err != nil {
		b.log.Warn("could not mark the data dir lock as stopping", "err", err)
	}
}

// disarmShutdownBackstop stops the backstop once Run has finished its cleanups.
func (b *boot) disarmShutdownBackstop() {
	if b.shutdownBackstop != nil {
		b.shutdownBackstop.Stop()
	}
}
