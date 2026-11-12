package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/peeradmit"
)

func enforcingTestChecker() *peeradmit.Checker {
	return peeradmit.New(peeradmit.Options{Mode: peeradmit.ModeEnforce, Log: slog.New(slog.DiscardHandler)})
}

func TestStoredOffSwitchAppliesAtBootAndIsLogged(t *testing.T) {
	var logs bytes.Buffer
	checker := enforcingTestChecker()
	applyStoredPeerCheck(context.Background(), checker, func(context.Context) (string, error) { return "off", nil }, slog.New(slog.NewTextHandler(&logs, nil)))
	if checker.Mode() != peeradmit.ModeOff || !strings.Contains(logs.String(), "OFF by the owner's switch") {
		t.Fatalf("mode %q, log %q", checker.Mode(), logs.String())
	}
}

func TestUnreadableSwitchKeepsEnforcing(t *testing.T) {
	checker := enforcingTestChecker()
	applyStoredPeerCheck(context.Background(), checker, func(context.Context) (string, error) { return "", errors.New("locked") }, slog.New(slog.DiscardHandler))
	if checker.Mode() != peeradmit.ModeEnforce {
		t.Fatalf("an unreadable switch changed the mode to %q", checker.Mode())
	}
}

type memPeerCheck struct{ value string }

func (m *memPeerCheck) SetPeerCheck(_ context.Context, value string, _ time.Time) error {
	m.value = value
	return nil
}

func TestSwitchStoresAndAppliesLive(t *testing.T) {
	checker := enforcingTestChecker()
	store := &memPeerCheck{}
	sw := peerCheckSwitch{checker: checker, store: store, now: time.Now}
	if err := sw.Set(context.Background(), "off"); err != nil || store.value != "off" || checker.Mode() != peeradmit.ModeOff {
		t.Fatalf("off: err %v, stored %q, mode %q", err, store.value, checker.Mode())
	}
	if err := sw.Set(context.Background(), "default"); err != nil || checker.Mode() != peeradmit.ModeEnforce {
		t.Fatalf("default: err %v, mode %q", err, checker.Mode())
	}
}
