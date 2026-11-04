package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionguard"
)

func TestSuppressedDeliveryError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		outcome sessionguard.Outcome
		want    error
	}{
		{sessionguard.SuppressedComposerBusy, ErrComposerBusy},
		{sessionguard.SuppressedNotFound, ErrNotFound},
		{sessionguard.SuppressedTerminated, ErrTerminated},
		{sessionguard.SuppressedExited, ErrAgentExited},
		{sessionguard.SuppressedAwaitingUser, ErrAwaitingDecision},
		{sessionguard.SuppressedStartupPending, ErrStartupPending},
		{sessionguard.SuppressedInputGated, ErrOperationInProgress},
	} {
		if err := suppressedDeliveryError("s1", tc.outcome); !errors.Is(err, tc.want) {
			t.Errorf("outcome %v: err = %v, want %v", tc.outcome, err, tc.want)
		}
	}
	if err := suppressedDeliveryError("s1", sessionguard.Sent); err != nil {
		t.Errorf("delivered outcome produced %v", err)
	}
}

func TestPrepareDeliveryMessageFraming(t *testing.T) {
	t.Parallel()
	m, _, _, _, _ := newTransitionManager(t, domain.SessionModeTUI)
	ctx := context.Background()

	got, handle, raw := m.prepareDeliveryMessage(ctx, "session-1", "hello", "c1", ports.MessageDeliveryOptions{AuthoredByUser: true})
	if got != "hello" || handle != "c1" || raw {
		t.Errorf("user send = (%q, %q, %v), want untouched message and client id", got, handle, raw)
	}
	got, handle, raw = m.prepareDeliveryMessage(ctx, "session-1", "do work", "c2", ports.MessageDeliveryOptions{})
	if !strings.Contains(got, "c2") || got == "do work" || handle != "c2" || raw {
		t.Errorf("non-user send = (%q, %q, %v), want wrapped with delivery id c2", got, handle, raw)
	}
	got, handle, raw = m.prepareDeliveryMessage(ctx, "session-1", "  ", "c3", ports.MessageDeliveryOptions{})
	if got != "  " || handle != "c3" || raw {
		t.Errorf("blank send = (%q, %q, %v), want untouched (deliberate Enter)", got, handle, raw)
	}
}

func TestVerifyNativeHistoryExistsSkipsAdaptersWithoutProbe(t *testing.T) {
	t.Parallel()
	if err := verifyNativeHistoryExists(context.Background(), nil, domain.SessionRecord{}, "/ws", nil); err != nil {
		t.Fatalf("agent without a probe must not block resume: %v", err)
	}
}
