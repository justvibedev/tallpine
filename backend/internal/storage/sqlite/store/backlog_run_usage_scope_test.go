package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func applyUsage(t *testing.T, s *sqlite.Store, session domain.SessionRecord, root string, cost int64, key string) {
	t.Helper()
	now := time.Unix(1700000000, 0).UTC()
	binding := mustUpsertUsageBinding(t, s, session, now, domain.UsageBindingRecord{NativeRootID: root, InitialModelID: "m", ProviderHint: "anthropic", State: domain.UsageBindingActive})
	source := mustInsertUsageSource(t, s, now, domain.UsageSourceRecord{BindingID: binding.ID, Kind: domain.UsageSourceClaudeMain, NativeSessionID: root, ArtifactPath: "/tmp/" + root, State: domain.UsageSourcePending})
	ev := domain.ModelUsageEvent{
		ProviderID: domain.UsageProviderAnthropic, BillingProviderID: "anthropic", BillingProviderSource: domain.UsageBillingProviderObserved,
		ModelID: "m", MeasurementKind: domain.UsageMeasurementNativeReported, Tokens: canonicalUsageTokens(20, 5, 15, 4),
		ProviderUsageJSON: anthropicProviderUsage(5, 10, nil, nil), SourceEventKey: key,
		Costs: domain.UsageEventCosts{EstimatedCostNanos: &cost, PricingVersion: "v1"},
	}
	if err := s.ApplyUsageChunk(context.Background(), source.ID, 0, source.UpdatedAt, domain.SourceCursorState{ByteOffset: 1, State: domain.UsageSourceActive, UpdatedAt: now}, []domain.ModelUsageEvent{ev}); err != nil {
		t.Fatalf("apply usage %s: %v", key, err)
	}
}

func TestBacklogRunUsageCountsASharedSessionOnceAndSkipsOrchestrators(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	worker := seedUsageSession(t, s, domain.HarnessClaudeCode)
	orchRec := sampleRecord("usage")
	orchRec.Kind = domain.KindOrchestrator
	orchSession, err := s.CreateSession(ctx, orchRec)
	if err != nil {
		t.Fatal(err)
	}
	applyUsage(t, s, worker, "w", 1000, "w1")
	applyUsage(t, s, orchSession, "o", 9000, "o1")

	a := addBacklog(t, s, "usage", "a", domain.BacklogOpen, "")
	b := addBacklog(t, s, "usage", "b", domain.BacklogOpen, "")
	for _, it := range []domain.BacklogItem{a, b} { // the worker session is linked to both items
		if _, err := s.AddBacklogLink(ctx, it.ID, domain.BacklogLink{Kind: domain.BacklogLinkSession, Ref: string(worker.ID)}, backlogNow); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddBacklogLink(ctx, a.ID, domain.BacklogLink{Kind: domain.BacklogLinkSession, Ref: string(orchSession.ID)}, backlogNow); err != nil {
		t.Fatal(err)
	}
	run := newBacklogRun(t, s, "usage", "r-1")
	if _, err := s.SaveBacklogRunPlan(ctx, run.ID, []store.BacklogRunPlanItem{{ItemID: a.ID}, {ItemID: b.ID, Lane: 1}}, backlogNow, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.BacklogRunUsageFor(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PricedNanos != 1000 || got.Events != 1 {
		t.Fatalf("run usage = %+v, want 1000 nanos once (shared worker session counted once, orchestrator excluded)", got)
	}
	if item, _ := s.BacklogItemUsageFor(ctx, a.ID); item.PricedNanos != 1000 {
		t.Fatalf("item usage = %+v, want the orchestrator session excluded", item)
	}
}

func TestBacklogRunSlotCountIgnoresClosedItems(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	a := addBacklog(t, s, "p1", "a", domain.BacklogOpen, "")
	b := addBacklog(t, s, "p1", "b", domain.BacklogOpen, "")
	run := newBacklogRun(t, s, "p1", "r-1")
	if _, err := s.SaveBacklogRunPlan(ctx, run.ID, []store.BacklogRunPlanItem{{ItemID: a.ID}, {ItemID: b.ID, Lane: 1}}, backlogNow, nil); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		if _, _, err := s.UpdateBacklogRunState(ctx, run.ID, backlogNow, func(_ *domain.BacklogRun, items []domain.BacklogRunItem, active int) error {
			if n == 0 && active == 0 {
				items[0].Status, items[1].Status = domain.BacklogRunItemActive, domain.BacklogRunItemActive
			}
			n = active
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	count() // activates both
	if n := count(); n != 2 {
		t.Fatalf("active slots = %d, want 2", n)
	}
	if _, err := s.SetBacklogItemState(ctx, a.ID, domain.BacklogDone, "merged", backlogNow); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("active slots after one item closed = %d, want 1", n)
	}
}

func TestBacklogRunPlanRefusesToDropWorkOnRecord(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	a := addBacklog(t, s, "p1", "a", domain.BacklogOpen, "")
	b := addBacklog(t, s, "p1", "b", domain.BacklogOpen, "")
	run := newBacklogRun(t, s, "p1", "r-1")
	save := func(ids ...string) error {
		var plan []store.BacklogRunPlanItem
		for i, id := range ids {
			plan = append(plan, store.BacklogRunPlanItem{ItemID: id, Lane: i})
		}
		_, err := s.SaveBacklogRunPlan(ctx, run.ID, plan, backlogNow, nil)
		return err
	}
	if err := save(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	set := func(id string, st domain.BacklogRunItemStatus, attempts int) {
		if _, _, err := s.UpdateBacklogRunState(ctx, run.ID, backlogNow, func(_ *domain.BacklogRun, items []domain.BacklogRunItem, _ int) error {
			for i := range items {
				if items[i].ItemID == id {
					items[i].Status, items[i].Attempts = st, attempts
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	set(a.ID, domain.BacklogRunItemActive, 0)
	if err := save(b.ID); !errors.Is(err, domain.ErrBacklogRunInvalid) {
		t.Fatalf("dropping an active item: %v, want ErrBacklogRunInvalid", err)
	}
	set(a.ID, domain.BacklogRunItemQueued, 1)
	if err := save(b.ID); !errors.Is(err, domain.ErrBacklogRunInvalid) {
		t.Fatalf("dropping a queued item with a failure on record: %v", err)
	}
	set(a.ID, domain.BacklogRunItemQueued, 0)
	if err := save(b.ID); err != nil {
		t.Fatalf("dropping a pristine queued item: %v", err)
	}
}
