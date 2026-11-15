package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestOrchestratorIntentLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	seedProject(t, s, "oth")
	now := time.Now().UTC().Truncate(time.Second)
	for i, id := range []string{"i1", "i2"} {
		in := domain.OrchestratorIntent{ID: id, ProjectID: "mer", Text: "hold " + id, CreatedBy: "sess", CreatedAt: now.Add(time.Duration(i) * time.Second)}
		if err := s.InsertOrchestratorIntent(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertOrchestratorIntent(ctx, domain.OrchestratorIntent{ID: "i3", ProjectID: "oth", Text: "x", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkOrchestratorIntentDone(ctx, "oth", "i1", now); err != nil || ok {
		t.Fatalf("cross-project clear: ok=%v err=%v", ok, err)
	}
	if ok, err := s.MarkOrchestratorIntentDone(ctx, "mer", "i1", now); err != nil || !ok {
		t.Fatalf("clear: ok=%v err=%v", ok, err)
	}
	if ok, _ := s.MarkOrchestratorIntentDone(ctx, "mer", "i1", now); ok {
		t.Fatal("second clear must report not open")
	}
	open, err := s.SelectOrchestratorIntents(ctx, "mer", false)
	if err != nil || len(open) != 1 || open[0].ID != "i2" || open[0].DoneAt != nil {
		t.Fatalf("open = %+v err=%v", open, err)
	}
	all, _ := s.SelectOrchestratorIntents(ctx, "mer", true)
	if len(all) != 2 || all[0].ID != "i1" || all[0].DoneAt == nil {
		t.Fatalf("all = %+v", all)
	}
	err = s.InsertOrchestratorIntent(ctx, domain.OrchestratorIntent{ID: "i4", ProjectID: "nope", Text: "x", CreatedAt: now})
	if !errors.Is(err, domain.ErrProjectUnknown) {
		t.Fatalf("unknown project err = %v", err)
	}
}

// A wake time is persisted; a due wake is claimed once, a cleared intent is not
// due, and a released claim is due again (B714).
func TestIntentWakePersistsAndClaimsOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	past, future := now.Add(-time.Minute), now.Add(time.Hour)
	for id, wake := range map[string]*time.Time{"due": &past, "later": &future, "none": nil, "cleared": &past} {
		if err := s.InsertOrchestratorIntent(ctx, domain.OrchestratorIntent{ID: id, ProjectID: "mer", Text: id, CreatedAt: now, WakeAt: wake}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.MarkOrchestratorIntentDone(ctx, "mer", "cleared", now); err != nil {
		t.Fatal(err)
	}
	due, err := s.SelectDueIntentWakes(ctx, now)
	if err != nil || len(due) != 1 || due[0].ID != "due" || due[0].WakeAt == nil || !due[0].WakeAt.Equal(past) {
		t.Fatalf("due = %+v err=%v", due, err)
	}
	if ok, err := s.ClaimIntentWake(ctx, "due", now); err != nil || !ok {
		t.Fatalf("first claim ok=%v err=%v", ok, err)
	}
	if ok, _ := s.ClaimIntentWake(ctx, "due", now); ok {
		t.Fatal("second claim must lose")
	}
	if due, _ = s.SelectDueIntentWakes(ctx, now); len(due) != 0 {
		t.Fatalf("claimed wake still due: %+v", due)
	}
	if err := s.ReleaseIntentWake(ctx, "due"); err != nil {
		t.Fatal(err)
	}
	if due, _ = s.SelectDueIntentWakes(ctx, now); len(due) != 1 {
		t.Fatalf("released wake not due again: %+v", due)
	}
	if ok, _ := s.ClaimIntentWake(ctx, "cleared", now); ok {
		t.Fatal("a cleared intent must not be claimable")
	}
}
