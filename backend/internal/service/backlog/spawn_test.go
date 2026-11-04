package backlog_test

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

func TestPrepareSpawnValidatesTheItem(t *testing.T) {
	e := newLifecycleEnv(t)
	ctx := e.ctx
	open := e.item("open item", "")
	inbox, _ := e.svc.Create(ctx, "p1", backlog.Actor{Kind: domain.BacklogOriginWorker, SessionID: "w"}, backlog.CreateInput{Title: "inbox item"})
	held := e.item("held item", "")
	if _, err := e.svc.Update(ctx, "p1", held.ID, backlog.UpdateInput{Hold: ptr("after rename")}); err != nil {
		t.Fatal(err)
	}
	parent := e.item("parent", "")
	if _, err := e.svc.Create(ctx, "p1", backlog.Actor{Kind: domain.BacklogOriginHuman}, backlog.CreateInput{Title: "child", ParentRef: parent.Ref()}); err != nil {
		t.Fatal(err)
	}
	done := e.item("done item", "")
	if _, err := e.svc.SetState(ctx, "p1", done.ID, domain.BacklogDone, ""); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		ref   string
		force bool
		code  string
	}{
		{"open item starts", open.Ref(), false, ""},
		{"inbox is refused", inbox.Ref(), false, "BACKLOG_ITEM_NOT_OPEN"},
		{"done is refused", done.Ref(), false, "BACKLOG_ITEM_NOT_OPEN"},
		{"held is refused", held.Ref(), false, "BACKLOG_ITEM_HELD"},
		{"held starts with force", held.Ref(), true, ""},
		{"parent with children is refused even with force", parent.Ref(), true, "BACKLOG_ITEM_IS_PARENT"},
		{"unknown ref", "B99", false, "BACKLOG_ITEM_NOT_FOUND"},
	}
	for _, tc := range cases {
		_, err := e.svc.PrepareSpawn(ctx, "p1", tc.ref, tc.force)
		if tc.code == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || apiCode(t, err) != tc.code {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.code)
		}
	}
	if _, err := e.svc.PrepareSpawn(ctx, "other-project", open.Ref(), false); err == nil {
		t.Error("another project's item resolved")
	}
}

func TestPrepareSpawnBuildsContextAndWarnsAboutOpenBlockers(t *testing.T) {
	e := newLifecycleEnv(t)
	ctx := e.ctx
	blocker := e.item("blocker", "")
	closed := e.item("closed blocker", "")
	if _, err := e.svc.SetState(ctx, "p1", closed.ID, domain.BacklogDone, ""); err != nil {
		t.Fatal(err)
	}
	target, err := e.svc.Create(ctx, "p1", backlog.Actor{Kind: domain.BacklogOriginHuman}, backlog.CreateInput{
		Title: "Do the thing", Note: "details here", DoneWhen: "tests pass", AgentNotes: "look at x.go", Scope: domain.BacklogScopePR,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.link(target, domain.BacklogLinkBlockedBy, blocker.Ref())
	e.link(target, domain.BacklogLinkBlockedBy, closed.Ref())

	got, err := e.svc.PrepareSpawn(ctx, "p1", target.Ref(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Backlog item " + target.Ref() + ": Do the thing", "details here", "tests pass", "look at x.go"} {
		if !strings.Contains(got.Context, want) {
			t.Errorf("context missing %q:\n%s", want, got.Context)
		}
	}
	if got.Warning != target.Ref()+" is blocked by "+blocker.Ref() {
		t.Fatalf("warning = %q (a closed blocker must not be listed)", got.Warning)
	}
}

func TestLinkSessionRecordsTheSpawner(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("work", "")
	if err := e.svc.LinkSession(e.ctx, it.ID, sess.ID, "orch-1"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.LinkSession(e.ctx, it.ID, sess.ID, ""); err != nil { // idempotent
		t.Fatal(err)
	}
	got := e.get(it)
	if len(got.LinkViews) != 1 || got.LinkViews[0].Kind != domain.BacklogLinkSession || got.LinkViews[0].Ref != string(sess.ID) || got.LinkViews[0].CreatedBy != "orch-1" {
		t.Fatalf("links = %+v", got.LinkViews)
	}
	if !got.Badges.Active {
		t.Fatal("a linked live session should show the item as active")
	}
	other := e.session()
	it2 := e.item("human spawn", "")
	_ = e.svc.LinkSession(e.ctx, it2.ID, other.ID, "")
	if got := e.get(it2); got.LinkViews[0].CreatedBy != "human" {
		t.Fatalf("created_by = %q, want human", got.LinkViews[0].CreatedBy)
	}
}

// A closed lane item is refused for a new lane orchestrator, but ResolveLaneItem
// still resolves it so a running orchestrator stays reachable.
func TestResolveLaneItemReturnsClosedItemsResolveLaneRefuses(t *testing.T) {
	e := newLifecycleEnv(t)
	lane := e.item("lane topic", "")
	if id, closed, err := e.svc.ResolveLaneItem(e.ctx, "p1", lane.Ref()); err != nil || id != lane.ID || closed != "" {
		t.Fatalf("open lane = %q %q %v", id, closed, err)
	}
	if _, err := e.svc.SetState(e.ctx, "p1", lane.ID, domain.BacklogDropped, ""); err != nil {
		t.Fatal(err)
	}
	id, closed, err := e.svc.ResolveLaneItem(e.ctx, "p1", lane.Ref())
	if err != nil || id != lane.ID || closed != domain.BacklogDropped {
		t.Fatalf("dropped lane = %q %q %v, want its id and state dropped", id, closed, err)
	}
	if _, err := e.svc.ResolveLane(e.ctx, "p1", lane.Ref()); apiCode(t, err) != "BACKLOG_LANE_CLOSED" {
		t.Fatalf("ResolveLane on a dropped item = %v, want BACKLOG_LANE_CLOSED", err)
	}
}

func TestPrepareSpawnWarnsWhenScopeIsEmpty(t *testing.T) {
	e := newLifecycleEnv(t)
	it := e.item("no scope", "")
	got, err := e.svc.PrepareSpawn(e.ctx, "p1", it.Ref(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Warning, "no publishing permission") {
		t.Fatalf("warning = %q", got.Warning)
	}
}

func TestCreateChildInheritsParentScope(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	h := backlog.Actor{Kind: domain.BacklogOriginHuman}
	parent, err := svc.Create(ctx, "p1", h, backlog.CreateInput{Title: "parent", Scope: domain.BacklogScopePR})
	if err != nil {
		t.Fatal(err)
	}
	inh, err := svc.Create(ctx, "p1", h, backlog.CreateInput{Title: "a", ParentRef: parent.Ref()})
	if err != nil {
		t.Fatal(err)
	}
	own, err := svc.Create(ctx, "p1", h, backlog.CreateInput{Title: "b", ParentRef: parent.Ref(), Scope: domain.BacklogScopeLocal})
	if err != nil {
		t.Fatal(err)
	}
	if inh.Scope != domain.BacklogScopePR || own.Scope != domain.BacklogScopeLocal {
		t.Fatalf("scopes = %q, %q", inh.Scope, own.Scope)
	}
}

func TestPrepareSpawnBriefStatesPublishingScope(t *testing.T) {
	e := newLifecycleEnv(t)
	for scope, want := range map[domain.BacklogScope]string{
		domain.BacklogScopePR:      "Push your branch and open a PR",
		domain.BacklogScopePRMerge: "merge it once it is approved",
		domain.BacklogScopeLocal:   "do not push",
		"":                         "no run scope applies",
	} {
		it, err := e.svc.Create(e.ctx, "p1", backlog.Actor{Kind: domain.BacklogOriginHuman}, backlog.CreateInput{Title: "t", Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		got, err := e.svc.PrepareSpawn(e.ctx, "p1", it.Ref(), false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got.Context, "Publishing: ") || !strings.Contains(got.Context, want) {
			t.Errorf("scope %q: context = %q, want %q", scope, got.Context, want)
		}
	}
}

// B305: a second worker on an item a live session already holds is refused
// unless forced; an ended session does not block.
func TestPrepareSpawnRefusesWhenLiveSessionHoldsItem(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("busy", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	if _, err := e.svc.PrepareSpawn(e.ctx, "p1", it.Ref(), false); err == nil || apiCode(t, err) != "BACKLOG_ITEM_HAS_LIVE_SESSION" {
		t.Fatalf("err = %v, want BACKLOG_ITEM_HAS_LIVE_SESSION", err)
	}
	if _, err := e.svc.PrepareSpawn(e.ctx, "p1", it.Ref(), true); err == nil || apiCode(t, err) != "BACKLOG_ITEM_HAS_LIVE_SESSION" {
		t.Fatalf("force alone: err = %v, want refusal", err)
	}
	if _, err := e.svc.PrepareSpawnAllowDuplicate(e.ctx, "p1", it.Ref(), false, true); err != nil {
		t.Fatalf("allow-duplicate: %v", err)
	}
	e.endSession(sess)
	if _, err := e.svc.PrepareSpawn(e.ctx, "p1", it.Ref(), false); err != nil {
		t.Fatalf("after end: %v", err)
	}
}
