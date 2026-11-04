package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/cdc"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func newBacklogRun(t *testing.T, s *sqlite.Store, project, id string) domain.BacklogRun {
	t.Helper()
	run, err := s.CreateBacklogRun(context.Background(), domain.BacklogRun{
		ID: id, ProjectID: domain.ProjectID(project), Scope: domain.BacklogScopePR,
		SelectionKind: domain.BacklogRunSelectAll, Status: domain.BacklogRunPlanning,
		RequestedBy: domain.BacklogOriginHuman, CreatedAt: backlogNow, UpdatedAt: backlogNow,
	})
	if err != nil {
		t.Fatalf("create run %s: %v", id, err)
	}
	return run
}

func TestBacklogRunRefsAndSingleOpenRun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	seedProject(t, s, "p2")
	first := newBacklogRun(t, s, "p1", "r-1")
	if first.Ref() != "R1" {
		t.Fatalf("first ref = %s, want R1", first.Ref())
	}
	// A second open run in the same project is refused; another project is fine.
	_, err := s.CreateBacklogRun(ctx, domain.BacklogRun{ID: "r-2", ProjectID: "p1", Scope: domain.BacklogScopePR,
		SelectionKind: domain.BacklogRunSelectAll, Status: domain.BacklogRunPlanning, RequestedBy: domain.BacklogOriginHuman,
		CreatedAt: backlogNow, UpdatedAt: backlogNow})
	if !errors.Is(err, domain.ErrBacklogRunOpen) {
		t.Fatalf("second open run err = %v, want ErrBacklogRunOpen", err)
	}
	if other := newBacklogRun(t, s, "p2", "r-3"); other.Ref() != "R1" {
		t.Fatalf("p2 first ref = %s, want R1 (refs are per project)", other.Ref())
	}
	// Finishing the first frees the slot and the next ref continues.
	if _, _, err := s.UpdateBacklogRunState(ctx, first.ID, backlogNow, func(r *domain.BacklogRun, _ []domain.BacklogRunItem, _ int) error {
		r.Status = domain.BacklogRunDone
		r.EndedAt = &backlogNow
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if next := newBacklogRun(t, s, "p1", "r-4"); next.Ref() != "R2" {
		t.Fatalf("next ref = %s, want R2", next.Ref())
	}
	open, err := s.ListBacklogRuns(ctx, "p1", true, 0)
	if err != nil || len(open) != 1 || open[0].ID != "r-4" {
		t.Fatalf("open runs = %+v, %v", open, err)
	}
}

func TestBacklogRunPlanKeepsProgressAndDropsRemoved(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	a := addBacklog(t, s, "p1", "a", domain.BacklogOpen, "")
	b := addBacklog(t, s, "p1", "b", domain.BacklogOpen, "")
	c := addBacklog(t, s, "p1", "c", domain.BacklogOpen, "")
	run := newBacklogRun(t, s, "p1", "r-1")
	plan := []store.BacklogRunPlanItem{{ItemID: a.ID, Lane: 0, Position: 0, Model: "opus"}, {ItemID: b.ID, Lane: 1, Position: 0, Model: "sonnet"}}
	if _, err := s.SaveBacklogRunPlan(ctx, run.ID, plan, backlogNow, func(r *domain.BacklogRun) error {
		r.Status = domain.BacklogRunRunning
		r.StartedAt = &backlogNow
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateBacklogRunState(ctx, run.ID, backlogNow, func(_ *domain.BacklogRun, items []domain.BacklogRunItem, _ int) error {
		items[0].Status = domain.BacklogRunItemActive
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Re-plan: a moves lane and model, b is dropped, c is added.
	plan = []store.BacklogRunPlanItem{{ItemID: a.ID, Lane: 1, Position: 0, Model: "sonnet"}, {ItemID: c.ID, Lane: 0, Position: 0, Model: "haiku"}}
	if _, err := s.SaveBacklogRunPlan(ctx, run.ID, plan, backlogNow, nil); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListBacklogRunItems(ctx, run.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	byRef := map[string]domain.BacklogRunItem{}
	for _, it := range items {
		byRef[it.ItemRef()] = it
	}
	if got := byRef[a.Ref()]; got.Status != domain.BacklogRunItemActive || got.Lane != 1 || got.Model != "sonnet" {
		t.Fatalf("kept item = %+v, want active in lane 1 with model sonnet", got)
	}
	if got := byRef[c.Ref()]; got.Status != domain.BacklogRunItemQueued {
		t.Fatalf("new item status = %s, want queued", got.Status)
	}
	if _, ok := byRef[b.Ref()]; ok {
		t.Fatal("removed item is still in the plan")
	}
}

func TestBacklogRunStateMutationIsAtomic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	a := addBacklog(t, s, "p1", "a", domain.BacklogOpen, "")
	run := newBacklogRun(t, s, "p1", "r-1")
	if _, err := s.SaveBacklogRunPlan(ctx, run.ID, []store.BacklogRunPlanItem{{ItemID: a.ID, Model: "opus"}}, backlogNow, nil); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	_, _, err := s.UpdateBacklogRunState(ctx, run.ID, backlogNow, func(r *domain.BacklogRun, items []domain.BacklogRunItem, _ int) error {
		r.Status = domain.BacklogRunRunning
		items[0].Status = domain.BacklogRunItemActive
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	got, _, _ := s.GetBacklogRun(ctx, "p1", run.Seq)
	items, _ := s.ListBacklogRunItems(ctx, run.ID)
	if got.Status != domain.BacklogRunPlanning || items[0].Status != domain.BacklogRunItemQueued {
		t.Fatalf("failed mutation leaked: run %s item %s", got.Status, items[0].Status)
	}
	// projectActive counts active items across the project's open runs.
	if _, _, err := s.UpdateBacklogRunState(ctx, run.ID, backlogNow, func(_ *domain.BacklogRun, items []domain.BacklogRunItem, _ int) error {
		items[0].Status = domain.BacklogRunItemActive
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var active int
	if _, _, err := s.UpdateBacklogRunState(ctx, run.ID, backlogNow, func(_ *domain.BacklogRun, _ []domain.BacklogRunItem, n int) error {
		active = n
		return nil
	}); err != nil || active != 1 {
		t.Fatalf("projectActive = %d, %v; want 1", active, err)
	}
}

func TestBacklogRunWritesEmitOneCDCEventEach(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	a := addBacklog(t, s, "p1", "a", domain.BacklogOpen, "")
	last := int64(0)
	if rows, _ := s.EventsAfter(ctx, 0, 100); len(rows) > 0 {
		last = rows[len(rows)-1].Seq
	}
	step := func(name string, wantRows int) {
		t.Helper()
		rows, err := s.EventsAfter(ctx, last, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != wantRows {
			t.Fatalf("%s: %d events, want %d: %+v", name, len(rows), wantRows, rows)
		}
		for _, e := range rows {
			if e.Type != cdc.EventBacklogChanged || e.ProjectID != "p1" {
				t.Fatalf("%s: event = %+v", name, e)
			}
			last = e.Seq
		}
	}
	run := newBacklogRun(t, s, "p1", "r-1")
	step("create run", 1)
	if _, err := s.SaveBacklogRunPlan(ctx, run.ID, []store.BacklogRunPlanItem{{ItemID: a.ID}}, backlogNow, nil); err != nil {
		t.Fatal(err)
	}
	step("add plan item", 1)
	if _, _, err := s.UpdateBacklogRunState(ctx, run.ID, backlogNow, func(_ *domain.BacklogRun, items []domain.BacklogRunItem, _ int) error {
		items[0].Status = domain.BacklogRunItemActive
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	step("item status", 1)
	if _, _, err := s.UpdateBacklogRunState(ctx, run.ID, backlogNow, func(*domain.BacklogRun, []domain.BacklogRunItem, int) error { return nil }); err != nil {
		t.Fatal(err)
	}
	step("no-op mutation", 0)
}

func TestBacklogItemUsageSumsLinkedSessionsOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sess := seedUsageSession(t, s, domain.HarnessClaudeCode)
	other, err := s.CreateSession(ctx, sampleRecord("usage"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0).UTC()
	apply := func(session domain.SessionRecord, root string, cost *int64, key string) {
		t.Helper()
		binding := mustUpsertUsageBinding(t, s, session, now, domain.UsageBindingRecord{NativeRootID: root, InitialModelID: "m", ProviderHint: "anthropic", State: domain.UsageBindingActive})
		source := mustInsertUsageSource(t, s, now, domain.UsageSourceRecord{BindingID: binding.ID, Kind: domain.UsageSourceClaudeMain, NativeSessionID: root, ArtifactPath: "/tmp/" + root, State: domain.UsageSourcePending})
		ev := domain.ModelUsageEvent{
			ProviderID: domain.UsageProviderAnthropic, BillingProviderID: "anthropic", BillingProviderSource: domain.UsageBillingProviderObserved,
			ModelID: "m", MeasurementKind: domain.UsageMeasurementNativeReported, Tokens: canonicalUsageTokens(20, 5, 15, 4),
			ProviderUsageJSON: anthropicProviderUsage(5, 10, nil, nil), SourceEventKey: key,
		}
		if cost != nil {
			ev.Costs = domain.UsageEventCosts{EstimatedCostNanos: cost, PricingVersion: "v1"}
		}
		if err := s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{ByteOffset: 1, State: domain.UsageSourceActive, UpdatedAt: now}, []domain.ModelUsageEvent{ev}); err != nil {
			t.Fatalf("apply usage %s: %v", key, err)
		}
	}
	c1, c2 := int64(1500), int64(2500)
	apply(sess, "root-a", &c1, "e1")
	apply(sess, "root-a2", &c2, "e2")
	apply(sess, "root-a3", nil, "e3") // unpriced event
	apply(other, "root-b", &c1, "e4") // a session not linked to the item

	item := addBacklog(t, s, "usage", "work", domain.BacklogOpen, "")
	if _, err := s.AddBacklogLink(ctx, item.ID, domain.BacklogLink{Kind: domain.BacklogLinkSession, Ref: string(sess.ID)}, backlogNow); err != nil {
		t.Fatal(err)
	}
	got, err := s.BacklogItemUsageFor(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PricedNanos != 4000 || got.Events != 3 || got.PricedEvents != 2 {
		t.Fatalf("usage = %+v, want 4000 nanos over 3 events (2 priced) from the linked session only", got)
	}
	if got.FirstStarted.IsZero() {
		t.Fatal("first started missing for a linked session")
	}
	empty := addBacklog(t, s, "usage", "idle", domain.BacklogOpen, "")
	if none, err := s.BacklogItemUsageFor(ctx, empty.ID); err != nil || none != (store.BacklogItemUsage{}) {
		t.Fatalf("unlinked usage = %+v, %v; want zero", none, err)
	}
}
