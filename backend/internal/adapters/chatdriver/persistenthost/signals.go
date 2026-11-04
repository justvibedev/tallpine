package persistenthost

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// SignalContext ends a host's context on SIGTERM, SIGINT or SIGHUP, so Run
// stops its provider's process tree before the host exits. Without it a
// signal (the one launchd sends every process at logout, for example) killed
// the host outright and left the provider, which runs in a process group of
// its own, alive with no owner.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, syscall.SIGTERM, os.Interrupt, syscall.SIGHUP)
}
