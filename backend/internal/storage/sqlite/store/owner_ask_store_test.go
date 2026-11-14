package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/cdc"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func newAsk(id, project, text string) domain.OwnerAsk {
	now := time.Now().UTC().Truncate(time.Second)
	return domain.OwnerAsk{
		ID: id, ProjectID: domain.ProjectID(project), Lane: "B7", LaneRef: "B7", Text: text, State: domain.AskOpen,
		Options:     []domain.PlanOption{{ID: "a", Label: "Yes"}, {ID: "b", Label: "No"}},
		Recommended: "a", CreatedAt: now, UpdatedAt: now,
	}
}

func TestOwnerAskStore_RefsRoundTripAndCDC(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	seedProject(t, s, "p2")

	a1, err := s.CreateOwnerAsk(ctx, newAsk("a1", "p1", "One?"), nil)
	if err != nil || a1.Ref != "Q1" {
		t.Fatalf("a1 = %+v %v", a1, err)
	}
	if a2, err := s.CreateOwnerAsk(ctx, newAsk("a2", "p1", "Two?"), nil); err != nil || a2.Ref != "Q2" {
		t.Fatalf("a2 = %+v %v", a2, err)
	}
	// Refs are per project.
	if b1, err := s.CreateOwnerAsk(ctx, newAsk("b1", "p2", "One?"), nil); err != nil || b1.Ref != "Q1" {
		t.Fatalf("b1 = %+v %v", b1, err)
	}
	// check sees the project's asks and can fill in the new one or abort.
	boom := errors.New("refused")
	if _, err := s.CreateOwnerAsk(ctx, newAsk("a3", "p1", "Three?"), func(ex []domain.OwnerAsk, _ *domain.OwnerAsk) error {
		if len(ex) != 2 {
			t.Errorf("check saw %d asks, want 2", len(ex))
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if all, _ := s.ListOwnerAsks(ctx, "p1"); len(all) != 2 {
		t.Fatalf("refused ask was stored: %d", len(all))
	}
	a4, err := s.CreateOwnerAsk(ctx, newAsk("a4", "p1", "Four?"), func(_ []domain.OwnerAsk, a *domain.OwnerAsk) error {
		a.ReaskOf, a.ReaskReason = "Q1", "changed"
		return nil
	})
	if err != nil || a4.Ref != "Q3" {
		t.Fatalf("a4 = %+v %v (a refused insert must not burn a ref)", a4, err)
	}

	got, ok, err := s.GetOwnerAsk(ctx, "p1", "q3")
	if err != nil || !ok || got.ReaskOf != "Q1" || got.ReaskReason != "changed" || len(got.Options) != 2 || got.Lane != "B7" || got.LaneRef != "B7" {
		t.Fatalf("got = %+v %v %v", got, ok, err)
	}
	if _, ok, _ := s.GetOwnerAsk(ctx, "p1", "Q99"); ok {
		t.Fatal("unknown ref found")
	}
	if err := s.SetOwnerAskAnchor(ctx, "a1", "turn-1", "act-1"); err != nil {
		t.Fatal(err)
	}
	if g, _, _ := s.GetOwnerAsk(ctx, "p1", "Q1"); !g.Anchored() || g.AnchorTurnID != "turn-1" {
		t.Fatalf("anchor lost: %+v", g)
	}

	rows, err := s.EventsAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	kinds := 0
	for _, e := range rows {
		if e.Type != cdc.EventBacklogChanged {
			continue
		}
		var p struct{ Kind, State string }
		_ = json.Unmarshal(e.Payload, &p)
		if p.Kind == "ask" {
			kinds++
		}
	}
	if kinds < 4 {
		t.Fatalf("saw %d ask backlog_changed events; want one per write", kinds)
	}
}

func TestOwnerAskStore_TransitionAppliesOnceWhileOpen(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	a, _ := s.CreateOwnerAsk(ctx, newAsk("a1", "p1", "One?"), nil)
	now := time.Now().UTC()
	first, _ := a.Answered(domain.AskAnswer{OptionID: "a", Text: "Yes"}, now)
	second, _ := a.Answered(domain.AskAnswer{OptionID: "b", Text: "No"}, now)
	if err := s.SaveOwnerAskTransition(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveOwnerAskTransition(ctx, second); !errors.Is(err, domain.ErrAskNotOpen) {
		t.Fatalf("second = %v; want ErrAskNotOpen", err)
	}
	got, _, _ := s.GetOwnerAsk(ctx, "p1", "Q1")
	if got.State != domain.AskAnswered || got.Answer == nil || got.Answer.Text != "Yes" || got.AnsweredAt.IsZero() {
		t.Fatalf("stored = %+v", got)
	}
	if open, _ := s.ListOpenOwnerAsks(ctx, "p1"); len(open) != 0 {
		t.Fatalf("open = %d", len(open))
	}
}
