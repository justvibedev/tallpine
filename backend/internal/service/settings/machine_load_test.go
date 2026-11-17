package settings

import (
	"context"
	"errors"
	"testing"
	"time"
)

type machineLoadStore struct {
	Store
	max, percent int
}

func (s *machineLoadStore) GetAppSettings(context.Context) (Snapshot, error) {
	return Snapshot{MachineMaxReviewers: s.max, MachineLoadPercent: s.percent}, nil
}

func (s *machineLoadStore) SetMachineLoad(_ context.Context, maxReviewers, loadPercent int, _ time.Time) error {
	s.max, s.percent = maxReviewers, loadPercent
	return nil
}

func TestSetMachineLoadRejectsOutOfRangeLimits(t *testing.T) {
	store := &machineLoadStore{max: 3, percent: 100}
	svc := New(store, nil, Offering{}, nil)
	bad := []int{-1, 101}
	for _, n := range bad {
		if _, err := svc.SetMachineLoad(context.Background(), MachineLoadUpdate{MaxReviewers: &n}); !errors.Is(err, ErrInvalidMachineLoad) {
			t.Fatalf("maxReviewers %d: err = %v, want ErrInvalidMachineLoad", n, err)
		}
	}
	over := 1001
	if _, err := svc.SetMachineLoad(context.Background(), MachineLoadUpdate{LoadPercent: &over}); !errors.Is(err, ErrInvalidMachineLoad) {
		t.Fatalf("loadPercent %d: err = %v", over, err)
	}
	if store.max != 3 || store.percent != 100 {
		t.Fatalf("a refused update changed the row: %d/%d", store.max, store.percent)
	}
	ok := 0
	if _, err := svc.SetMachineLoad(context.Background(), MachineLoadUpdate{MaxReviewers: &ok}); err != nil || store.max != 0 || store.percent != 100 {
		t.Fatalf("0 must disable the cap and keep the other limit: err=%v row=%d/%d", err, store.max, store.percent)
	}
}
