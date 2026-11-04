package settings

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
)

type dynamicContextStoreFake struct {
	hibernationSettingsStore
	on      bool
	readErr error
}

func (s *dynamicContextStoreFake) GetAppSettings(context.Context) (Snapshot, error) {
	if s.readErr != nil {
		return Snapshot{}, s.readErr
	}
	return Snapshot{DynamicOrchestratorContext: s.on}, nil
}

func (s *dynamicContextStoreFake) SetDynamicOrchestratorContext(_ context.Context, enabled bool, _ time.Time) error {
	s.on = enabled
	return nil
}

// An unreadable store counts as the default, on.
func TestDynamicOrchestratorContextDefaultsOnWhenUnreadable(t *testing.T) {
	svc := New(&dynamicContextStoreFake{readErr: errors.New("db")}, nil, Offering{}, nil)
	if !svc.DynamicOrchestratorContextEnabled(context.Background()) {
		t.Fatal("an unreadable store must read as the default, on")
	}
}

// Turning the toggle off tells the hook (the daemon ends every running
// research mode); turning it on does not clear anything.
func TestTurningDynamicOrchestratorContextOffRunsTheClearHook(t *testing.T) {
	ctx := context.Background()
	store := &dynamicContextStoreFake{on: true}
	svc := New(store, nil, Offering{}, nil)
	var calls []bool
	svc.SetDynamicContextHook(func(_ context.Context, enabled bool) { calls = append(calls, enabled) })
	if _, err := svc.SetDynamicOrchestratorContext(ctx, false); err != nil {
		t.Fatal(err)
	}
	if svc.DynamicOrchestratorContextEnabled(ctx) || len(calls) != 1 || calls[0] {
		t.Fatalf("after off: enabled=%v hook calls=%v, want off and one hook call with false", svc.DynamicOrchestratorContextEnabled(ctx), calls)
	}
	if _, err := svc.SetDynamicOrchestratorContext(ctx, true); err != nil || !svc.DynamicOrchestratorContextEnabled(ctx) {
		t.Fatalf("turn on: err=%v", err)
	}
}

// A managed lock on the toggle refuses a change instead of saving a value that
// has no effect.
func TestManagedDynamicOrchestratorContextRefusesChanges(t *testing.T) {
	store := &dynamicContextStoreFake{on: true}
	svc := New(store, nil, Offering{}, nil)
	svc.SetManaged(managedcfg.New(func() map[string]json.RawMessage {
		return map[string]json.RawMessage{"app.dynamicOrchestratorContext": json.RawMessage("true")}
	}))
	_, err := svc.SetDynamicOrchestratorContext(context.Background(), false)
	var api *apierr.Error
	if !errors.As(err, &api) || api.Code != managedcfg.CodeLocked || !store.on {
		t.Fatalf("err=%v on=%v, want a locked refusal and the stored value kept", err, store.on)
	}
}
