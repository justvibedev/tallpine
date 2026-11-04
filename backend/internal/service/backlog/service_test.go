package backlog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func newService(t *testing.T) *backlog.Service {
	t.Helper()
	s := sqlitetest.MustOpen(t)
	for _, id := range []string{"p1", "p2"} {
		if err := s.UpsertProject(context.Background(), domain.ProjectRecord{ID: id, Path: "/tmp/" + id, RegisteredAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	return backlog.New(backlog.Deps{Store: s})
}

func apiCode(t *testing.T, err error) string {
	t.Helper()
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want *apierr.Error", err)
	}
	return ae.Code
}

func TestCreateStateByActor(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	cases := []struct {
		name  string
		actor backlog.Actor
		open  bool
		want  domain.BacklogState
	}{
		{"human is always open", backlog.Actor{Kind: domain.BacklogOriginHuman}, false, domain.BacklogOpen},
		{"orchestrator defaults to inbox", backlog.Actor{Kind: domain.BacklogOriginOrchestrator, SessionID: "o1"}, false, domain.BacklogInbox},
		{"orchestrator --open accepts", backlog.Actor{Kind: domain.BacklogOriginOrchestrator, SessionID: "o1"}, true, domain.BacklogOpen},
		{"worker is forced to inbox even with open", backlog.Actor{Kind: domain.BacklogOriginWorker, SessionID: "w1"}, true, domain.BacklogInbox},
	}
	for _, tc := range cases {
		v, err := svc.Create(ctx, "p1", tc.actor, backlog.CreateInput{Title: "t " + tc.name, Open: tc.open})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if v.State != tc.want || v.Origin.Kind != tc.actor.Kind || v.Origin.SessionID != tc.actor.SessionID {
			t.Fatalf("%s: state=%s origin=%+v", tc.name, v.State, v.Origin)
		}
	}
}

func TestResolveByRefAndIDStaysInProject(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	human := backlog.Actor{Kind: domain.BacklogOriginHuman}
	a, _ := svc.Create(ctx, "p1", human, backlog.CreateInput{Title: "a"})
	for _, ref := range []string{a.Ref(), "b1", "1", a.ID} {
		if v, err := svc.Get(ctx, "p1", ref); err != nil || v.ID != a.ID {
			t.Fatalf("get %q = %+v %v", ref, v, err)
		}
	}
	if _, err := svc.Get(ctx, "p2", a.ID); apiCode(t, err) != "BACKLOG_ITEM_NOT_FOUND" {
		t.Fatalf("cross-project id read: %v", err)
	}
	if _, err := svc.Get(ctx, "p2", "B1"); apiCode(t, err) != "BACKLOG_ITEM_NOT_FOUND" {
		t.Fatalf("p2 has no B1: %v", err)
	}
}

func TestUpdateParentAndHierarchyErrors(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	human := backlog.Actor{Kind: domain.BacklogOriginHuman}
	parent, _ := svc.Create(ctx, "p1", human, backlog.CreateInput{Title: "parent"})
	child, err := svc.Create(ctx, "p1", human, backlog.CreateInput{Title: "child", ParentRef: parent.Ref()})
	if err != nil || child.ParentRef != parent.Ref() || child.ParentID != parent.ID {
		t.Fatalf("child = %+v %v", child, err)
	}
	grand := backlog.CreateInput{Title: "grand", ParentRef: child.Ref()}
	if _, err := svc.Create(ctx, "p1", human, grand); apiCode(t, err) != "BACKLOG_HIERARCHY" {
		t.Fatalf("grandchild: %v", err)
	}
	if v, err := svc.Get(ctx, "p1", parent.Ref()); err != nil || v.ChildCount != 1 {
		t.Fatalf("parent = %+v %v", v, err)
	}
	cleared, err := svc.Update(ctx, "p1", child.Ref(), backlog.UpdateInput{ClearParent: true})
	if err != nil || cleared.ParentID != "" {
		t.Fatalf("clear parent = %+v %v", cleared, err)
	}
	title := "  "
	if _, err := svc.Update(ctx, "p1", child.Ref(), backlog.UpdateInput{Title: &title}); apiCode(t, err) != "INVALID_BACKLOG_TITLE" {
		t.Fatalf("blank title: %v", err)
	}
}

func TestLinkResolvesItemRefsAndRejectsCycle(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	human := backlog.Actor{Kind: domain.BacklogOriginHuman}
	a, _ := svc.Create(ctx, "p1", human, backlog.CreateInput{Title: "a"})
	b, _ := svc.Create(ctx, "p1", human, backlog.CreateInput{Title: "b"})
	v, err := svc.Link(ctx, "p1", a.Ref(), domain.BacklogLinkBlockedBy, b.Ref(), "")
	if err != nil || len(v.LinkViews) != 1 || v.LinkViews[0].Display != b.Ref() || v.LinkViews[0].Ref != b.ID {
		t.Fatalf("link = %+v %v", v, err)
	}
	if _, err := svc.Link(ctx, "p1", b.Ref(), domain.BacklogLinkBlockedBy, a.Ref(), ""); apiCode(t, err) != "BACKLOG_CYCLE" {
		t.Fatalf("cycle: %v", err)
	}
	if _, err := svc.Link(ctx, "p1", a.Ref(), "bogus", "x", ""); apiCode(t, err) != "INVALID_BACKLOG_LINK_KIND" {
		t.Fatalf("bad kind: %v", err)
	}
	v, err = svc.Unlink(ctx, "p1", a.Ref(), domain.BacklogLinkBlockedBy, b.Ref())
	if err != nil || len(v.LinkViews) != 0 {
		t.Fatalf("unlink = %+v %v", v, err)
	}
}

func TestFileDeferredReportsWhyALineWasNotFiled(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	var last []domain.DeferOutcome
	var err error
	for i := 0; i < domain.MaxBacklogInboxPerProject; i++ {
		// Many sessions so the 30-per-session cap does not hit first.
		last, err = svc.FileDeferred(ctx, "p1", domain.SessionID("s"+string(rune('a'+i%26))+string(rune('a'+i/26))), "ex", []string{"line " + string(rune('A'+i%26)) + string(rune('A'+i/26))})
		if err != nil || last[0].Status != domain.DeferFiled {
			t.Fatalf("fill %d: %+v %v", i, last, err)
		}
	}
	last, err = svc.FileDeferred(ctx, "p1", "sz", "ex", []string{"overflow"})
	if err != nil || last[0].Status != domain.DeferInboxFull || last[0].Reason != "not filed: inbox cap reached" {
		t.Fatalf("overflow = %+v %v", last, err)
	}
	if _, err := svc.FileDeferred(ctx, "p1", "sz", "ex", []string{"a", "b", "c", "d"}); err == nil {
		t.Fatal("4 lines accepted")
	}
}
