package backlog_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/cdc"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// lifecycleEnv wires the real service, SQLite store and CDC event feed: PR and
// session writes go through the real store, whose triggers produce the events
// the subscriber consumes.
type lifecycleEnv struct {
	t       *testing.T
	ctx     context.Context
	store   *sqlite.Store
	svc     *backlog.Service
	sub     *backlog.Subscriber
	lastSeq int64
}

func newLifecycleEnv(t *testing.T) *lifecycleEnv {
	t.Helper()
	s := sqlitetest.MustOpen(t)
	ctx := context.Background()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	svc := backlog.New(backlog.Deps{Store: s})
	return &lifecycleEnv{t: t, ctx: ctx, store: s, svc: svc, sub: backlog.NewSubscriber(svc, backlog.SubscriberConfig{})}
}

func (e *lifecycleEnv) item(title string, scope domain.BacklogScope) backlog.View {
	e.t.Helper()
	v, err := e.svc.Create(e.ctx, "p1", backlog.Actor{Kind: domain.BacklogOriginHuman}, backlog.CreateInput{Title: title, Scope: scope})
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *lifecycleEnv) session() domain.SessionRecord {
	e.t.Helper()
	now := time.Now().UTC()
	rec, err := e.store.CreateSession(e.ctx, domain.SessionRecord{
		ProjectID: "p1", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata: domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/ws"}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return rec
}

func (e *lifecycleEnv) endSession(rec domain.SessionRecord) {
	e.t.Helper()
	rec.IsTerminated = true
	rec.UpdatedAt = time.Now().UTC()
	if err := e.store.UpdateSession(e.ctx, rec); err != nil {
		e.t.Fatal(err)
	}
}

func (e *lifecycleEnv) link(it backlog.View, kind domain.BacklogLinkKind, ref string) {
	e.t.Helper()
	if _, err := e.svc.Link(e.ctx, "p1", it.ID, kind, ref, "test"); err != nil {
		e.t.Fatal(err)
	}
}

func (e *lifecycleEnv) writePR(sess domain.SessionID, number int, state string) string {
	e.t.Helper()
	url := fmt.Sprintf("https://github.com/o/r/pull/%d", number)
	pr := domain.PullRequest{URL: url, SessionID: sess, Number: number, UpdatedAt: time.Now().UTC(), Merged: state == "merged", Closed: state == "closed"}
	if err := e.store.WritePR(e.ctx, pr, nil, nil); err != nil {
		e.t.Fatal(err)
	}
	return url
}

// pump feeds every new change_log event to the subscriber and reconciles.
func (e *lifecycleEnv) pump() {
	e.t.Helper()
	events, err := e.store.EventsAfter(e.ctx, e.lastSeq, 10000)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, ev := range events {
		e.sub.Handle(ev)
		e.lastSeq = ev.Seq
	}
	e.sub.Drain(e.ctx)
}

func (e *lifecycleEnv) get(it backlog.View) backlog.View {
	e.t.Helper()
	v, err := e.svc.Get(e.ctx, "p1", it.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func TestAllLinkedPRsMergedClosesItemThroughBothMergePaths(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("ship it", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	u10 := e.writePR(sess.ID, 10, "open")
	u11 := e.writePR(sess.ID, 11, "open")
	e.pump()
	if got := e.get(it); got.State != domain.BacklogOpen {
		t.Fatalf("open PRs: state = %s", got.State)
	}

	// Tallpine merge path: MarkPRMerged.
	if err := e.store.MarkPRMerged(e.ctx, u10, "sha", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	e.pump()
	if got := e.get(it); got.State != domain.BacklogOpen {
		t.Fatalf("one of two merged: state = %s, want open", got.State)
	}
	// External merge path: the SCM observer rewrites the pr row.
	e.writePR(sess.ID, 11, "merged")
	e.pump()
	got := e.get(it)
	if got.State != domain.BacklogDone || got.ClosedReason != "merged #10, #11" {
		t.Fatalf("after both merged: %s %q", got.State, got.ClosedReason)
	}
	_ = u11
}

func TestDirectPRLinkClosesItemAndSingleMergeNamesTheNumber(t *testing.T) {
	e := newLifecycleEnv(t)
	owner := e.session()
	url := e.writePR(owner.ID, 42, "open")
	it := e.item("external pr", "")
	e.link(it, domain.BacklogLinkPR, url)
	e.writePR(owner.ID, 42, "merged")
	e.pump()
	if got := e.get(it); got.State != domain.BacklogDone || got.ClosedReason != "merged #42" {
		t.Fatalf("got %s %q", got.State, got.ClosedReason)
	}
}

func TestUntrackedPRLinkNeverCountsAsMerged(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("mixed", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.link(it, domain.BacklogLinkPR, "https://github.com/o/r/pull/999")
	e.writePR(sess.ID, 1, "open")
	e.pump()
	e.writePR(sess.ID, 1, "merged")
	e.pump()
	if got := e.get(it); got.State != domain.BacklogOpen {
		t.Fatalf("state = %s: an untracked PR link cannot be verified", got.State)
	}
}

func TestScopeLocalIsNeverAutoClosed(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("local only", domain.BacklogScopeLocal)
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writePR(sess.ID, 5, "open")
	e.pump()
	e.writePR(sess.ID, 5, "merged")
	e.pump()
	if got := e.get(it); got.State != domain.BacklogOpen {
		t.Fatalf("local item auto-closed: %s", got.State)
	}
	// Not by a done report either.
	other := e.session()
	it2 := e.item("local, no pr", domain.BacklogScopeLocal)
	e.link(it2, domain.BacklogLinkSession, string(other.ID))
	if err := e.svc.ReportFiled(e.ctx, domain.ReportRecord{SessionID: other.ID, State: domain.ReportDone}, nil); err != nil {
		t.Fatal(err)
	}
	if got := e.get(it2); got.State != domain.BacklogOpen {
		t.Fatalf("local item closed by done report: %s", got.State)
	}
	// The orchestrator closes it by hand.
	if v, err := e.svc.SetState(e.ctx, "p1", it.ID, domain.BacklogDone, "reviewed"); err != nil || v.State != domain.BacklogDone {
		t.Fatalf("manual close: %+v %v", v, err)
	}
}

func TestPRClosedUnmergedLeavesItemOpenWithOneNote(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("abandoned pr", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writePR(sess.ID, 7, "open")
	e.writePR(sess.ID, 7, "closed")
	e.pump()
	e.pump()
	if err := e.svc.Reconcile(e.ctx, it.ID); err != nil { // replay
		t.Fatal(err)
	}
	got := e.get(it)
	if got.State != domain.BacklogOpen || strings.Count(got.AgentNotes, "PR #7 closed without merge") != 1 {
		t.Fatalf("state=%s notes=%q", got.State, got.AgentNotes)
	}
}

func TestLinkedSessionEndingWithoutMergeAddsANote(t *testing.T) {
	e := newLifecycleEnv(t)
	live := e.session()
	gone := e.session()
	withOpenPR := e.session()
	it := e.item("worker dies", "")
	for _, s := range []domain.SessionRecord{live, gone, withOpenPR} {
		e.link(it, domain.BacklogLinkSession, string(s.ID))
	}
	e.writePR(withOpenPR.ID, 3, "open")
	e.pump()
	if got := e.get(it); got.AgentNotes != "" {
		t.Fatalf("notes written while sessions are live: %q", got.AgentNotes)
	}
	e.endSession(gone)
	e.endSession(withOpenPR)
	e.pump()
	e.pump()
	got := e.get(it)
	want := "worker " + string(gone.ID) + " ended without merge"
	if got.State != domain.BacklogOpen || strings.Count(got.AgentNotes, want) != 1 {
		t.Fatalf("state=%s notes=%q", got.State, got.AgentNotes)
	}
	if strings.Contains(got.AgentNotes, string(live.ID)) || strings.Contains(got.AgentNotes, string(withOpenPR.ID)) {
		t.Fatalf("notes name a live session or one waiting on an open PR: %q", got.AgentNotes)
	}
}

func TestDoneReportClosesOnlyItemsWithoutAnyPR(t *testing.T) {
	e := newLifecycleEnv(t)
	report := func(sess domain.SessionID, outputs ...domain.ReportOutput) domain.ReportRecord {
		return domain.ReportRecord{SessionID: sess, State: domain.ReportDone, Outputs: outputs}
	}
	clean := func(context.Context, domain.ReportRecord) string { return "" }

	// No PR anywhere: closed as "worker done".
	s1 := e.session()
	noPR := e.item("no pr", "")
	e.link(noPR, domain.BacklogLinkSession, string(s1.ID))
	if err := e.svc.ReportFiled(e.ctx, report(s1.ID), clean); err != nil {
		t.Fatal(err)
	}
	if got := e.get(noPR); got.State != domain.BacklogDone || got.ClosedReason != "worker done" {
		t.Fatalf("no-PR item = %s %q", got.State, got.ClosedReason)
	}

	// A session-owned PR keeps it open.
	s2 := e.session()
	owned := e.item("owned pr", "")
	e.link(owned, domain.BacklogLinkSession, string(s2.ID))
	e.writePR(s2.ID, 20, "open")
	_ = e.svc.ReportFiled(e.ctx, report(s2.ID), clean)
	// A direct PR link keeps it open.
	s3 := e.session()
	linked := e.item("linked pr", "")
	e.link(linked, domain.BacklogLinkSession, string(s3.ID))
	e.link(linked, domain.BacklogLinkPR, "https://github.com/o/r/pull/77")
	_ = e.svc.ReportFiled(e.ctx, report(s3.ID), clean)
	// A report that names a created PR keeps it open.
	s4 := e.session()
	reported := e.item("reported pr", "")
	e.link(reported, domain.BacklogLinkSession, string(s4.ID))
	_ = e.svc.ReportFiled(e.ctx, report(s4.ID, domain.ReportOutput{Kind: domain.ReportOutputPRCreated, Reference: "https://github.com/o/r/pull/78"}), clean)
	// An unverified done report (looks undelivered) keeps it open.
	s5 := e.session()
	unverified := e.item("unverified", "")
	e.link(unverified, domain.BacklogLinkSession, string(s5.ID))
	_ = e.svc.ReportFiled(e.ctx, report(s5.ID), func(context.Context, domain.ReportRecord) string { return "unpushed commits" })
	// Non-done reports and synthesized notices never close.
	s6 := e.session()
	checkpoint := e.item("checkpoint", "")
	e.link(checkpoint, domain.BacklogLinkSession, string(s6.ID))
	_ = e.svc.ReportFiled(e.ctx, domain.ReportRecord{SessionID: s6.ID, State: domain.ReportCheckpoint}, clean)
	synth := domain.ReportRecord{SessionID: s6.ID, State: domain.ReportDone, Synthesized: true}
	_ = e.svc.ReportFiled(e.ctx, synth, clean)

	for name, it := range map[string]backlog.View{"owned": owned, "linked": linked, "reported": reported, "unverified": unverified, "checkpoint": checkpoint} {
		if got := e.get(it); got.State != domain.BacklogOpen {
			t.Errorf("%s item closed by a done report: %s", name, got.State)
		}
	}
}

func TestAutoNotesKeepLastFiveAndNeverTouchOtherText(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("many closes", "")
	if _, err := e.svc.Update(e.ctx, "p1", it.ID, backlog.UpdateInput{AgentNotes: ptr("my own notes\nsecond line")}); err != nil {
		t.Fatal(err)
	}
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	for n := 1; n <= 7; n++ {
		e.writePR(sess.ID, n, "closed")
	}
	if err := e.svc.Reconcile(e.ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	got := e.get(it).AgentNotes
	if !strings.HasPrefix(got, "my own notes\nsecond line\n") {
		t.Fatalf("human text was touched: %q", got)
	}
	if n := strings.Count(got, "- auto "); n != 5 {
		t.Fatalf("auto lines = %d, want 5: %q", n, got)
	}
	if strings.Contains(got, "PR #1 closed") || strings.Contains(got, "PR #2 closed") || !strings.Contains(got, "PR #7 closed") {
		t.Fatalf("the oldest lines should be dropped first: %q", got)
	}
}

func TestSubscriberIgnoresNonTerminalPREventsAndSweepRepairsMissedOnes(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("sweep me", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writePR(sess.ID, 9, "open")
	// Consume everything so far, then merge without delivering the event.
	e.pump()
	before := e.sub.Stats().KeysReconciled
	e.writePR(sess.ID, 9, "merged")
	if err := e.svc.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.get(it); got.State != domain.BacklogDone || got.ClosedReason != "merged #9" {
		t.Fatalf("sweep missed the merge: %s %q", got.State, got.ClosedReason)
	}
	if e.sub.Stats().KeysReconciled != before {
		t.Fatal("sweep must not depend on the subscriber")
	}
}

func TestPRFirstSeenAlreadyMergedClosesItem(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("merged before Tallpine saw it", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writePR(sess.ID, 31, "merged") // inserted merged: a pr_created event, not pr_updated
	e.pump()
	if got := e.get(it); got.State != domain.BacklogDone || got.ClosedReason != "merged #31" {
		t.Fatalf("got %s %q", got.State, got.ClosedReason)
	}
}

func TestSubscriberOnlyActsOnTerminalPRStates(t *testing.T) {
	e := newLifecycleEnv(t)
	event := func(state string) cdc.Event {
		return cdc.Event{Type: cdc.EventPRUpdated, Payload: []byte(`{"url":"https://github.com/o/r/pull/1","state":"` + state + `"}`)}
	}
	for _, state := range []string{"open", "draft"} {
		e.sub.Handle(event(state))
	}
	e.sub.Handle(cdc.Event{Type: cdc.EventPRCheckRecorded, Payload: []byte(`{}`)})
	e.sub.Drain(e.ctx)
	if got := e.sub.Stats(); got.KeysReconciled != 0 || got.EventsSeen != 0 {
		t.Fatalf("non-terminal PR events were queued: %+v", got)
	}
	e.sub.Handle(event("merged"))
	e.sub.Handle(event("closed")) // same key as merged: batched into one reconcile
	e.sub.Drain(e.ctx)
	if got := e.sub.Stats(); got.KeysReconciled != 1 {
		t.Fatalf("terminal events = %+v, want one batched key", got)
	}
}

func TestBadgesAreDerivedFromLinkedSessionsAndPRs(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("badges", "")
	if b := e.get(it).Badges; b != (backlog.Badges{}) {
		t.Fatalf("unlinked badges = %+v", b)
	}
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	if b := e.get(it).Badges; !b.Active || b.InReview || b.Landed {
		t.Fatalf("live session = %+v", b)
	}
	e.writePR(sess.ID, 1, "open")
	if b := e.get(it).Badges; !b.Active || !b.InReview || b.Landed {
		t.Fatalf("open PR = %+v", b)
	}
	e.endSession(sess)
	e.writePR(sess.ID, 1, "merged")
	b := e.get(it).Badges
	if b.Active || b.InReview || !b.Landed {
		t.Fatalf("merged PR, item still open = %+v", b)
	}
	list, err := e.svc.List(e.ctx, "p1", backlog.ListFilter{})
	if err != nil || len(list) != 1 || list[0].Badges != b {
		t.Fatalf("list badges disagree with get: %+v %v", list, err)
	}
	if err := e.svc.Reconcile(e.ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if b := e.get(it).Badges; b != (backlog.Badges{}) {
		t.Fatalf("closed item badges = %+v", b)
	}
}

func ptr[T any](v T) *T { return &v }

type fakeBroadcaster struct {
	fn          func(cdc.Event)
	unsubscribe bool
}

func (f *fakeBroadcaster) Subscribe(fn func(cdc.Event)) func() {
	f.fn = fn
	return func() { f.unsubscribe = true }
}

func TestSubscriberStartDebouncesAndStopsCleanly(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("live loop", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writePR(sess.ID, 50, "open")
	e.pump()
	e.writePR(sess.ID, 50, "merged")

	sub := backlog.NewSubscriber(e.svc, backlog.SubscriberConfig{Debounce: 5 * time.Millisecond, StartDelay: time.Hour})
	bc := &fakeBroadcaster{}
	ctx, cancel := context.WithCancel(e.ctx)
	done := sub.Start(ctx, bc)
	// A burst of session updates for an unrelated session collapses to one key.
	for i := 0; i < 1000; i++ {
		bc.fn(cdc.Event{Type: cdc.EventSessionUpdated, SessionID: "someone-else"})
	}
	events, err := e.store.EventsAfter(e.ctx, e.lastSeq, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		bc.fn(ev)
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.get(it).State != domain.BacklogDone && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := e.get(it); got.State != domain.BacklogDone {
		t.Fatalf("live loop never closed the item: %s", got.State)
	}
	stats := sub.Stats()
	if stats.EventsSeen < 1000 || stats.KeysReconciled > 4 {
		t.Fatalf("stats = %+v: 1000 session events must collapse into a handful of reconciles", stats)
	}
	cancel()
	<-done
	if !bc.unsubscribe {
		t.Fatal("subscriber did not unsubscribe on shutdown")
	}
}

func TestListStateAllIncludesClosedItems(t *testing.T) {
	e := newLifecycleEnv(t)
	open := e.item("still open", "")
	closed := e.item("finished", "")
	if _, err := e.svc.SetState(e.ctx, "p1", closed.ID, domain.BacklogDone, "x"); err != nil {
		t.Fatal(err)
	}
	all, err := e.svc.List(e.ctx, "p1", backlog.ListFilter{State: "all"})
	if err != nil || len(all) != 2 {
		t.Fatalf("state=all = %d items, err %v (want 2: %s + closed)", len(all), err, open.ID)
	}
	if _, err := e.svc.List(e.ctx, "p1", backlog.ListFilter{State: "bogus"}); err == nil {
		t.Fatal("bogus state accepted")
	}
}

// B213: a manual reopen wins over merges that happened before it; the sweep and
// reconcile must not re-close, but a PR merged after the reopen still closes.
func TestReopenSticksAgainstEarlierMergesButNewMergeCloses(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("parent", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writePR(sess.ID, 10, "merged")
	e.pump()
	if got := e.get(it); got.State != domain.BacklogDone {
		t.Fatalf("setup: state = %s, want done", got.State)
	}
	if _, err := e.store.SetBacklogItemState(e.ctx, it.ID, domain.BacklogOpen, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	e.endSession(sess)
	if err := e.svc.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Reconcile(e.ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	e.pump()
	got := e.get(it)
	if got.State != domain.BacklogOpen {
		t.Fatalf("after reopen + reconcile: state = %s, want open", got.State)
	}
	if strings.Contains(got.AgentNotes, "ended without merge") {
		t.Fatalf("false abandoned-worker note: %q", got.AgentNotes)
	}
	facts, err := e.store.BacklogFactsFor(e.ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r := backlog.DecideAutoClose(domain.BacklogItem{}, facts).Reason; !strings.Contains(r, "reopened after PR #10 merged") {
		t.Fatalf("reason = %q", r)
	}
	e.writePR(sess.ID, 11, "merged")
	e.pump()
	if got := e.get(it); got.State != domain.BacklogDone || got.ClosedReason != "merged #11" {
		t.Fatalf("new merge: %s %q", got.State, got.ClosedReason)
	}
}
