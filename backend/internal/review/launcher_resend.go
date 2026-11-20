package review

import (
	"context"
	"regexp"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Claude Code's start-up auto-update screen swallows the first prompt and asks
// the user to "Resend your message". The launch retypes the prompt (B840).
const (
	resendMarker      = "resend your message"
	updatingMarker    = "checking for updates"
	maxInitialResends = 3
	resendScreenLines = 40
)

// Package vars so tests can run without real waiting.
var (
	resendSettle   = 2 * time.Second
	resendPollStep = 500 * time.Millisecond
)

var ansiEscape = regexp.MustCompile("\x1b(?:\\[[0-?]*[@-~]|\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|[@-Z\\\\-_])") //nolint:gocritic // standard ECMA-48 final-byte ranges

// stripANSI removes terminal escape sequences from captured screen text.
func stripANSI(s string) string {
	return strings.ReplaceAll(ansiEscape.ReplaceAllString(s, ""), "\x1b", "")
}

// resendTailLines is how many non-blank bottom lines show a running update check.
const resendTailLines = 8

// resendWaitPolls bounds the wait for a running update check.
const resendWaitPolls = 20

func screenTail(screen string, n int) string {
	var lines []string
	for _, line := range strings.Split(stripANSI(screen), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.ToLower(strings.Join(lines, "\n"))
}

func screenUpdating(screen string) bool {
	return strings.Contains(screenTail(screen, resendTailLines), updatingMarker)
}

// screenAsksResend reports whether the pane's latest "Resend your message"
// line is current: nothing after it echoes the prompt we sent (an accepted
// prompt is echoed below a stale error). The input box and footer below a
// fresh marker do not matter.
func screenAsksResend(screen, message string) bool {
	lines := strings.Split(strings.ToLower(stripANSI(screen)), "\n")
	marker := -1
	for i, line := range lines {
		if strings.Contains(line, resendMarker) {
			marker = i
		}
	}
	if marker < 0 {
		return false
	}
	echo := strings.ToLower(strings.TrimSpace(message))
	if i := strings.IndexByte(echo, '\n'); i >= 0 {
		echo = echo[:i]
	}
	if len(echo) > 40 {
		echo = echo[:40]
	}
	return echo == "" || !strings.Contains(strings.Join(lines[marker+1:], "\n"), echo)
}

// resendIfSwallowed re-delivers message while the pane's bottom shows the
// update screen's "Resend your message" prompt, waiting out "Checking for
// updates". A pane that did not change since the last resend is left alone.
func (l *agentLauncher) resendIfSwallowed(ctx context.Context, handle ports.RuntimeHandle, message string) error {
	last := ""
	for i := 0; i < maxInitialResends; i++ {
		if err := sleepCtx(ctx, resendSettle); err != nil {
			return err
		}
		screen, ok := l.paneText(ctx, handle)
		if !ok || (!screenAsksResend(screen, message) && !screenUpdating(screen)) || (i > 0 && screen == last) {
			return nil
		}
		for polls := 0; polls < resendWaitPolls && screenUpdating(screen); polls++ {
			if err := sleepCtx(ctx, resendPollStep); err != nil {
				return err
			}
			if screen, ok = l.paneText(ctx, handle); !ok {
				return nil
			}
		}
		if !screenAsksResend(screen, message) {
			return nil
		}
		last = screen
		if err := l.runtime.SendMessage(ctx, handle, message); err != nil {
			return err
		}
	}
	return nil
}

// paneText reads the pane; ok is false when it cannot be read, which is
// unknown rather than a launch failure.
func (l *agentLauncher) paneText(ctx context.Context, handle ports.RuntimeHandle) (string, bool) {
	out, err := l.runtime.GetOutput(ctx, handle, resendScreenLines)
	return out, err == nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
