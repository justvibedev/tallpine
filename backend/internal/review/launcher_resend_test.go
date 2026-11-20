package review

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// resendRuntime shows the swallowed-prompt screen until the prompt was typed
// `swallow` times, then a ready screen.
type resendRuntime struct {
	fakeRuntime
	swallow int
}

func (r *resendRuntime) GetOutput(_ context.Context, _ ports.RuntimeHandle, _ int) (string, error) {
	if len(r.sentMsgs) <= r.swallow {
		return fmt.Sprintf("\x1b[31m✘ Auto-update failed\x1b[0m · Run claude doctor\nResend your message to try again\n(attempt %d)", len(r.sentMsgs)), nil
	}
	return "> ", nil
}

func spawnWithResend(t *testing.T, swallow int) *resendRuntime {
	t.Helper()
	reviewer := &fakeReviewerWithLaunchSpec{spec: ports.ReviewCommandSpec{Argv: []string{"claude"}, InitialMessage: "task ref"}}
	rt := &resendRuntime{swallow: swallow}
	l := newTestLauncher(t, reviewer, rt)
	if _, err := l.Spawn(context.Background(), launchSpec()); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	return rt
}

func TestLauncherResendsPromptSwallowedByUpdateScreen(t *testing.T) {
	rt := spawnWithResend(t, 1)
	if len(rt.sentMsgs) != 2 || rt.sentMsgs[1] != "task ref" {
		t.Fatalf("sent = %q, want the prompt delivered twice", rt.sentMsgs)
	}
}

func TestLauncherResendIsBounded(t *testing.T) {
	rt := spawnWithResend(t, 100)
	if len(rt.sentMsgs) != 1+maxInitialResends {
		t.Fatalf("sent %d, want %d", len(rt.sentMsgs), 1+maxInitialResends)
	}
}

func TestLauncherDoesNotResendOnReadyScreen(t *testing.T) {
	rt := spawnWithResend(t, 0)
	if len(rt.sentMsgs) != 1 {
		t.Fatalf("sent = %q, want one delivery", rt.sentMsgs)
	}
}

func TestTailScreenStripsANSI(t *testing.T) {
	got := tailScreen("\x1b[1;31m✘ Auto-update failed\x1b[0m · Run claude doctor\n\x1b[2mChecking for updates…\x1b[0m\n", 2000)
	if strings.Contains(got, "\x1b") || !strings.Contains(got, "✘ Auto-update failed · Run claude doctor") {
		t.Fatalf("tailScreen = %q", got)
	}
}

// scriptedRuntime returns canned screens, then the last one forever.
type scriptedRuntime struct {
	fakeRuntime
	screens []string
	reads   int
}

func (r *scriptedRuntime) GetOutput(_ context.Context, _ ports.RuntimeHandle, _ int) (string, error) {
	i := min(r.reads, len(r.screens)-1)
	r.reads++
	return r.screens[i], nil
}

func spawnScripted(t *testing.T, screens ...string) *scriptedRuntime {
	t.Helper()
	reviewer := &fakeReviewerWithLaunchSpec{spec: ports.ReviewCommandSpec{Argv: []string{"claude"}, InitialMessage: "task ref"}}
	rt := &scriptedRuntime{screens: screens}
	if _, err := newTestLauncher(t, reviewer, rt).Spawn(context.Background(), launchSpec()); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	return rt
}

func TestLauncherResendsWhenFreshMarkerHasInputBoxBelow(t *testing.T) {
	fresh := "Resend your message to try again\n---\n> \n---\n? help"
	rt := spawnScripted(t, fresh, "> task ref\n* Working")
	if len(rt.sentMsgs) != 2 {
		t.Fatalf("sent = %q, want exactly one resend", rt.sentMsgs)
	}
}

func TestLauncherIgnoresStaleResendMarkerAfterSuccessfulResend(t *testing.T) {
	stale := "Resend your message to try again\n> task ref\n* Working\n---\n> \n---\n? help"
	rt := spawnScripted(t, "Resend your message to try again", stale)
	if len(rt.sentMsgs) != 2 {
		t.Fatalf("sent = %q, want exactly one resend", rt.sentMsgs)
	}
}

func TestLauncherResendWaitsOutUpdateCheck(t *testing.T) {
	rt := spawnScripted(t, "Checking for updates…", "Checking for updates…", "Resend your message to try again", "> ")
	if len(rt.sentMsgs) != 2 || rt.reads < 4 {
		t.Fatalf("sent = %q reads = %d, want one resend after the check", rt.sentMsgs, rt.reads)
	}
}
