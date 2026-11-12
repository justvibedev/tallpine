package chat

import (
	"context"
	"errors"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Close detaches this daemon's controller. Persistent provider hosts keep the
// native connection and any in-flight turn alive; non-persistent drivers retain
// their historical process-close behavior. The persistent host replays detached
// output and unresolved provider requests to the replacement controller.
func (c *Controller) Close(ctx context.Context) error {
	c.mu.Lock()
	c.explicitClose = true
	c.stopPauseWakeLocked()
	c.stopTransientRetryLocked()
	c.mu.Unlock()
	c.once.Do(func() {
		if preserver, ok := c.conv.(ports.ChatProviderPreserver); ok && preserver.PreservesProviderOnClose() {
			c.mu.Lock()
			c.preserveProviderOnStop = true
			c.mu.Unlock()
		}
		c.closeErr = c.conv.Close()
		c.asyncTasksGone()
	})
	select {
	case <-c.stopped:
		return c.closeErr
	case <-ctx.Done():
		if c.closeErr != nil {
			return errors.Join(c.closeErr, ctx.Err())
		}
		return ctx.Err()
	}
}

// Terminate closes the controller and explicitly destroys a persistent provider
// host. Daemon shutdown uses Close so the host survives; user/session teardown
// and controller replacement use Terminate.
func (c *Controller) Terminate(ctx context.Context) error {
	c.mu.Lock()
	c.explicitClose = true
	c.stopPauseWakeLocked()
	c.stopTransientRetryLocked()
	if c.forkCancel != nil {
		c.forkCancel()
	}
	c.mu.Unlock()
	c.once.Do(func() {
		if terminator, ok := c.conv.(ports.ChatProviderTerminator); ok {
			c.closeErr = terminator.Terminate()
		} else {
			c.closeErr = c.conv.Close()
		}
		c.asyncTasksGone()
	})
	select {
	case <-c.stopped:
		return c.closeErr
	case <-ctx.Done():
		if c.closeErr != nil {
			return errors.Join(c.closeErr, ctx.Err())
		}
		return ctx.Err()
	}
}
