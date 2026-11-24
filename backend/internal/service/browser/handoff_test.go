package browser

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"tallpine.eu/tallpine/backend/internal/browserruntime"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type fakeNotifier struct {
	mu          sync.Mutex
	intents     []ports.NotificationIntent
	resolutions []ports.NotificationResolution
	// resolvedBeforeReturn records whether the notification was still open
	// while the runtime waited.
	openDuringWait bool
}

func (f *fakeNotifier) Notify(_ context.Context, intent ports.NotificationIntent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.intents = append(f.intents, intent)
	return nil
}

func (f *fakeNotifier) Resolve(_ context.Context, res ports.NotificationResolution) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolutions = append(f.resolutions, res)
	return nil
}

type handoffRuntime struct {
	notifier *fakeNotifier
	args     map[string]interface{}
	action   string
	err      error
	deadline bool
}

func (h *handoffRuntime) Status() browserruntime.Status {
	return browserruntime.Status{Connected: true}
}

func (h *handoffRuntime) ExecuteOn(ctx context.Context, _ domain.SessionID, _ string, action string, args map[string]interface{}) (browserruntime.Result, error) {
	h.action, h.args = action, args
	_, h.deadline = ctx.Deadline()
	h.notifier.openDuringWait = len(h.notifier.intents) == 1 && len(h.notifier.resolutions) == 0
	return browserruntime.Result{Value: map[string]any{"outcome": "continued"}}, h.err
}

func handoffService(t *testing.T, runtimeErr error) (*Service, *handoffRuntime, *fakeNotifier, string) {
	t.Helper()
	authority := NewAuthority()
	token, verifier, err := authority.Issue("s1")
	if err != nil {
		t.Fatal(err)
	}
	notifier := &fakeNotifier{}
	runtime := &handoffRuntime{notifier: notifier, err: runtimeErr}
	service := New(fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
		ID: "s1", ProjectID: "p1", DisplayName: "Login worker",
		Metadata: domain.SessionMetadata{BrowserCapabilityVerifier: verifier},
	}}}, runtime, authority, nil).WithNotifier(notifier, nil)
	return service, runtime, notifier, token
}

func TestHandoffRaisesNeedsYouAndResolvesItAfterTheWait(t *testing.T) {
	service, runtime, notifier, token := handoffService(t, nil)
	_, action, err := service.Execute(context.Background(), "s1", token, "handoff", map[string]interface{}{
		"reason": "  Enter the 2FA code  ", "untilUrl": " https://app.example/* ", "timeoutMs": float64(60_000), "extra": true,
	})
	if err != nil || action != "handoff" || runtime.action != "handoff" {
		t.Fatalf("action=%q runtime=%q err=%v", action, runtime.action, err)
	}
	if len(notifier.intents) != 1 {
		t.Fatalf("intents = %#v", notifier.intents)
	}
	intent := notifier.intents[0]
	if intent.Type != domain.NotificationNeedsInput || intent.SessionID != "s1" || intent.ProjectID != "p1" ||
		intent.SessionDisplayName != "Login worker" || intent.Body != "Browser: Enter the 2FA code" ||
		!strings.HasPrefix(intent.SourceKey, "browser-handoff:s1:") {
		t.Fatalf("intent = %#v", intent)
	}
	if !notifier.openDuringWait {
		t.Fatal("the notification was not open while the desktop waited")
	}
	if len(notifier.resolutions) != 1 || notifier.resolutions[0].SourceKeyPrefix != intent.SourceKey ||
		notifier.resolutions[0].Type != domain.NotificationNeedsInput || notifier.resolutions[0].SessionID != "" {
		t.Fatalf("resolutions = %#v", notifier.resolutions)
	}
	want := map[string]interface{}{"reason": "Enter the 2FA code", "untilUrl": "https://app.example/*", "timeoutMs": int64(60_000)}
	if len(runtime.args) != len(want) {
		t.Fatalf("runtime args = %#v", runtime.args)
	}
	for key, value := range want {
		if runtime.args[key] != value {
			t.Fatalf("runtime args = %#v", runtime.args)
		}
	}
	if !runtime.deadline {
		t.Fatal("the runtime wait has no backstop deadline")
	}
}

func TestHandoffResolvesTheNotificationWhenTheWaitFails(t *testing.T) {
	timeout := browserruntime.CommandError{Code: "BROWSER_HANDOFF_TIMEOUT", Message: "timed out"}
	service, _, notifier, token := handoffService(t, timeout)
	_, _, err := service.Execute(context.Background(), "s1", token, "handoff", map[string]interface{}{"reason": "Log in", "timeoutMs": float64(10_000)})
	var commandErr browserruntime.CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != "BROWSER_HANDOFF_TIMEOUT" {
		t.Fatalf("err = %v", err)
	}
	if len(notifier.intents) != 1 || len(notifier.resolutions) != 1 {
		t.Fatalf("intents=%d resolutions=%d", len(notifier.intents), len(notifier.resolutions))
	}
}

func TestHandoffRejectsInvalidArgumentsBeforeNotifying(t *testing.T) {
	service, runtime, notifier, token := handoffService(t, nil)
	for _, args := range []map[string]interface{}{
		{"timeoutMs": float64(60_000)},
		{"reason": " ", "timeoutMs": float64(60_000)},
		{"reason": strings.Repeat("é", 501), "timeoutMs": float64(60_000)},
		{"reason": "x"},
		{"reason": "x", "timeoutMs": float64(9_999)},
		{"reason": "x", "timeoutMs": float64(1_800_001)},
		{"reason": "x", "timeoutMs": 60_000.5},
		{"reason": "x", "timeoutMs": "60000"},
		{"reason": "x", "timeoutMs": float64(60_000), "untilUrl": ""},
		{"reason": "x", "timeoutMs": float64(60_000), "untilUrl": 7},
		{"reason": "x", "timeoutMs": float64(60_000), "untilUrl": strings.Repeat("u", 2049)},
	} {
		if _, _, err := service.Execute(context.Background(), "s1", token, "handoff", args); apiErrorCode(err) != "BROWSER_HANDOFF_INVALID" {
			t.Fatalf("%v: err = %v", args, err)
		}
	}
	if runtime.action != "" || len(notifier.intents) != 0 {
		t.Fatalf("an invalid handoff reached the runtime (%q) or notified (%d)", runtime.action, len(notifier.intents))
	}
}

func TestHandoffWithoutNotifierStillWaits(t *testing.T) {
	authority := NewAuthority()
	token, verifier, _ := authority.Issue("s1")
	runtime := &fakeRuntime{}
	service := New(fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
		ID: "s1", Metadata: domain.SessionMetadata{BrowserCapabilityVerifier: verifier},
	}}}, runtime, authority, nil)
	if _, _, err := service.Execute(context.Background(), "s1", token, "handoff", map[string]interface{}{"reason": "x", "timeoutMs": 10_000}); err != nil || runtime.action != "handoff" {
		t.Fatalf("runtime=%q err=%v", runtime.action, err)
	}
}

// blockingRuntime holds each command until release is closed.
type blockingRuntime struct {
	started chan domain.SessionID
	release chan struct{}
}

func (b *blockingRuntime) Status() browserruntime.Status {
	return browserruntime.Status{Connected: true}
}

func (b *blockingRuntime) ExecuteOn(_ context.Context, sessionID domain.SessionID, _, _ string, _ map[string]interface{}) (browserruntime.Result, error) {
	b.started <- sessionID
	<-b.release
	return browserruntime.Result{}, nil
}

type twoSessions struct{ verifiers map[domain.SessionID]string }

func (t twoSessions) Get(_ context.Context, id domain.SessionID) (domain.Session, error) {
	return domain.Session{SessionRecord: domain.SessionRecord{ID: id, Metadata: domain.SessionMetadata{BrowserCapabilityVerifier: t.verifiers[id]}}}, nil
}

func TestHandoffRefusesASecondHandoffForTheSameSession(t *testing.T) {
	authority := NewAuthority()
	t1, v1, _ := authority.Issue("s1")
	t2, v2, _ := authority.Issue("s2")
	runtime := &blockingRuntime{started: make(chan domain.SessionID, 2), release: make(chan struct{})}
	notifier := &fakeNotifier{}
	service := New(twoSessions{verifiers: map[domain.SessionID]string{"s1": v1, "s2": v2}}, runtime, authority, nil).WithNotifier(notifier, nil)
	args := map[string]interface{}{"reason": "Log in", "timeoutMs": float64(60_000)}

	done := make(chan error, 2)
	go func() { _, _, err := service.Execute(context.Background(), "s1", t1, "handoff", args); done <- err }()
	<-runtime.started
	if _, _, err := service.Execute(context.Background(), "s1", t1, "handoff", args); apiErrorCode(err) != "BROWSER_HANDOFF_ACTIVE" {
		t.Fatalf("second handoff err = %v", err)
	}
	notifier.mu.Lock()
	intents := len(notifier.intents)
	notifier.mu.Unlock()
	if intents != 1 {
		t.Fatalf("the refused handoff notified: %d intents", intents)
	}
	// Another session is not blocked by it.
	go func() { _, _, err := service.Execute(context.Background(), "s2", t2, "handoff", args); done <- err }()
	if got := <-runtime.started; got != "s2" {
		t.Fatalf("started %q, want s2", got)
	}
	close(runtime.release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	// The slot is free once the first ended.
	go func() { <-runtime.started }()
	if _, _, err := service.Execute(context.Background(), "s1", t1, "handoff", args); err != nil {
		t.Fatalf("handoff after the first ended: %v", err)
	}
}
