package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

var backlogNow = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

var backlogSeq int

func addBacklog(t *testing.T, s *sqlite.Store, project, title string, state domain.BacklogState, parent string) domain.BacklogItem {
	t.Helper()
	backlogSeq++
	var closed *time.Time
	if state.Closed() {
		closed = &backlogNow
	}
	item, err := s.CreateBacklogItem(context.Background(), domain.BacklogItem{
		ID: fmt.Sprintf("bl-%d", backlogSeq), ProjectID: domain.ProjectID(project), Title: title, State: state,
		ParentID: parent, Origin: domain.BacklogOrigin{Kind: domain.BacklogOriginHuman},
		CreatedAt: backlogNow, UpdatedAt: backlogNow, ClosedAt: closed,
	})
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	return item
}

func TestBacklogOneLevelHierarchy(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	seedProject(t, s, "p2")
	parent := addBacklog(t, s, "p1", "parent", domain.BacklogOpen, "")
	child := addBacklog(t, s, "p1", "child", domain.BacklogOpen, parent.ID)
	loose := addBacklog(t, s, "p1", "loose", domain.BacklogOpen, "")
	other := addBacklog(t, s, "p2", "other", domain.BacklogOpen, "")

	// A child cannot have children.
	_, err := s.CreateBacklogItem(ctx, domain.BacklogItem{ID: "grand", ProjectID: "p1", Title: "g", State: domain.BacklogOpen, ParentID: child.ID,
		Origin: domain.BacklogOrigin{Kind: domain.BacklogOriginHuman}, CreatedAt: backlogNow, UpdatedAt: backlogNow})
	if !errors.Is(err, domain.ErrBacklogHierarchy) {
		t.Fatalf("grandchild create err = %v, want hierarchy", err)
	}
	// A parent cannot become a child.
	_, err = s.UpdateBacklogItem(ctx, parent.ID, backlogNow, func(i *domain.BacklogItem) error { i.ParentID = loose.ID; return nil })
	if !errors.Is(err, domain.ErrBacklogHierarchy) {
		t.Fatalf("parent->child err = %v, want hierarchy", err)
	}
	// An existing child cannot gain children by re-parenting another item under it.
	_, err = s.UpdateBacklogItem(ctx, loose.ID, backlogNow, func(i *domain.BacklogItem) error { i.ParentID = child.ID; return nil })
	if !errors.Is(err, domain.ErrBacklogHierarchy) {
		t.Fatalf("under-child err = %v, want hierarchy", err)
	}
	// Self parent and cross-project parent.
	_, err = s.UpdateBacklogItem(ctx, loose.ID, backlogNow, func(i *domain.BacklogItem) error { i.ParentID = loose.ID; return nil })
	if !errors.Is(err, domain.ErrBacklogHierarchy) {
		t.Fatalf("self parent err = %v, want hierarchy", err)
	}
	_, err = s.UpdateBacklogItem(ctx, loose.ID, backlogNow, func(i *domain.BacklogItem) error { i.ParentID = other.ID; return nil })
	if !errors.Is(err, domain.ErrBacklogCrossProject) {
		t.Fatalf("cross-project parent err = %v, want cross-project", err)
	}
	// A valid re-parent still works.
	if _, err := s.UpdateBacklogItem(ctx, loose.ID, backlogNow, func(i *domain.BacklogItem) error { i.ParentID = parent.ID; return nil }); err != nil {
		t.Fatalf("valid re-parent: %v", err)
	}
}

func TestBacklogBlockedByRejectsCyclesAndCrossProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	seedProject(t, s, "p2")
	a := addBacklog(t, s, "p1", "a", domain.BacklogOpen, "")
	b := addBacklog(t, s, "p1", "b", domain.BacklogOpen, "")
	c := addBacklog(t, s, "p1", "c", domain.BacklogOpen, "")
	x := addBacklog(t, s, "p2", "x", domain.BacklogOpen, "")
	block := func(item, blocker domain.BacklogItem) error {
		_, err := s.AddBacklogLink(ctx, item.ID, domain.BacklogLink{Kind: domain.BacklogLinkBlockedBy, Ref: blocker.ID}, backlogNow)
		return err
	}
	if err := block(a, b); err != nil {
		t.Fatal(err)
	}
	if err := block(b, c); err != nil {
		t.Fatal(err)
	}
	if err := block(b, a); !errors.Is(err, domain.ErrBacklogCycle) {
		t.Fatalf("direct cycle err = %v", err)
	}
	if err := block(c, a); !errors.Is(err, domain.ErrBacklogCycle) {
		t.Fatalf("3-hop cycle err = %v", err)
	}
	if err := block(a, a); err == nil {
		t.Fatal("self block accepted")
	}
	if err := block(a, x); !errors.Is(err, domain.ErrBacklogCrossProject) {
		t.Fatalf("cross-project err = %v", err)
	}
	// Diamond is not a cycle.
	if err := block(a, c); err != nil {
		t.Fatalf("diamond rejected: %v", err)
	}
}

func TestBacklogRefAllocationUnderConcurrency(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "p1")
	seedProject(t, s, "p2")
	const perProject = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	refs := map[string]map[int64]bool{"p1": {}, "p2": {}}
	for i := 0; i < perProject*2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			project := []string{"p1", "p2"}[i%2]
			item, err := s.CreateBacklogItem(context.Background(), domain.BacklogItem{
				ID: fmt.Sprintf("c-%d", i), ProjectID: domain.ProjectID(project), Title: "t", State: domain.BacklogInbox,
				Origin: domain.BacklogOrigin{Kind: domain.BacklogOriginHuman}, CreatedAt: backlogNow, UpdatedAt: backlogNow,
			})
			if err != nil {
				t.Errorf("create: %v", err)
				return
			}
			mu.Lock()
			if refs[project][item.Seq] {
				t.Errorf("%s: duplicate seq %d", project, item.Seq)
			}
			refs[project][item.Seq] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	for project, seen := range refs {
		for n := int64(1); n <= perProject; n++ {
			if !seen[n] {
				t.Fatalf("%s: seq %d missing (refs must be gapless per project)", project, n)
			}
		}
	}
	// Closing an item never frees its ref.
	item := addBacklog(t, s, "p1", "later", domain.BacklogOpen, "")
	if item.Seq != perProject+1 {
		t.Fatalf("next seq = %d, want %d", item.Seq, perProject+1)
	}
}

func TestBacklogNextOrderingAndExclusions(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*sqlite.Store, func() string) {
		s := newTestStore(t)
		seedProject(t, s, "p1")
		return s, func() string {
			it, ok, err := s.NextBacklogItem(ctx, "p1", "")
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				return ""
			}
			return it.Title
		}
	}

	t.Run("topmost open wins and reorder changes it", func(t *testing.T) {
		s, next := setup(t)
		a := addBacklog(t, s, "p1", "a", domain.BacklogOpen, "")
		b := addBacklog(t, s, "p1", "b", domain.BacklogOpen, "")
		if got := next(); got != "a" {
			t.Fatalf("next = %q", got)
		}
		if err := s.MoveBacklogItem(ctx, b.ID, a.ID, false, backlogNow); err != nil {
			t.Fatal(err)
		}
		if got := next(); got != "b" {
			t.Fatalf("after move next = %q", got)
		}
	})
	t.Run("ignores inbox done and dropped", func(t *testing.T) {
		s, next := setup(t)
		addBacklog(t, s, "p1", "inbox", domain.BacklogInbox, "")
		addBacklog(t, s, "p1", "done", domain.BacklogDone, "")
		addBacklog(t, s, "p1", "dropped", domain.BacklogDropped, "")
		addBacklog(t, s, "p1", "open", domain.BacklogOpen, "")
		if got := next(); got != "open" {
			t.Fatalf("next = %q", got)
		}
	})
	t.Run("skips held", func(t *testing.T) {
		s, next := setup(t)
		a := addBacklog(t, s, "p1", "held", domain.BacklogOpen, "")
		addBacklog(t, s, "p1", "free", domain.BacklogOpen, "")
		if _, err := s.UpdateBacklogItem(ctx, a.ID, backlogNow, func(i *domain.BacklogItem) error { i.Hold = "later"; return nil }); err != nil {
			t.Fatal(err)
		}
		if got := next(); got != "free" {
			t.Fatalf("next = %q", got)
		}
	})
	t.Run("skips parents but picks their children", func(t *testing.T) {
		s, next := setup(t)
		p := addBacklog(t, s, "p1", "parent", domain.BacklogOpen, "")
		addBacklog(t, s, "p1", "child", domain.BacklogOpen, p.ID)
		if got := next(); got != "child" {
			t.Fatalf("next = %q", got)
		}
		it, ok, err := s.NextBacklogItem(ctx, "p1", p.ID)
		if err != nil || !ok || it.Title != "child" {
			t.Fatalf("scoped next = %+v ok=%v err=%v", it, ok, err)
		}
	})
	t.Run("blocked until blocker closes, dropped counts", func(t *testing.T) {
		s, next := setup(t)
		blocked := addBacklog(t, s, "p1", "blocked", domain.BacklogOpen, "")
		blocker := addBacklog(t, s, "p1", "blocker", domain.BacklogInbox, "")
		addBacklog(t, s, "p1", "last", domain.BacklogOpen, "")
		if _, err := s.AddBacklogLink(ctx, blocked.ID, domain.BacklogLink{Kind: domain.BacklogLinkBlockedBy, Ref: blocker.ID}, backlogNow); err != nil {
			t.Fatal(err)
		}
		if got := next(); got != "last" {
			t.Fatalf("blocked by inbox: next = %q", got)
		}
		if _, err := s.SetBacklogItemState(ctx, blocker.ID, domain.BacklogDropped, "no", backlogNow); err != nil {
			t.Fatal(err)
		}
		if got := next(); got != "blocked" {
			t.Fatalf("dropped blocker should unblock: next = %q", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		_, next := setup(t)
		if got := next(); got != "" {
			t.Fatalf("next = %q", got)
		}
	})
}

func TestBacklogMoveAfterAndStableOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	a := addBacklog(t, s, "p1", "a", domain.BacklogOpen, "")
	b := addBacklog(t, s, "p1", "b", domain.BacklogOpen, "")
	c := addBacklog(t, s, "p1", "c", domain.BacklogOpen, "")
	if err := s.MoveBacklogItem(ctx, a.ID, c.ID, true, backlogNow); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListBacklogItems(ctx, "p1", store.BacklogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	got := ""
	for _, it := range items {
		got += it.Title
	}
	if got != "bca" {
		t.Fatalf("order = %q, want bca", got)
	}
	_ = b
}

func TestBacklogStateClosedAtAndFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	it := addBacklog(t, s, "p1", "x", domain.BacklogOpen, "")
	closed, err := s.SetBacklogItemState(ctx, it.ID, domain.BacklogDone, "shipped", backlogNow)
	if err != nil || closed.ClosedAt == nil || closed.ClosedReason != "shipped" {
		t.Fatalf("close = %+v err=%v", closed, err)
	}
	reopened, err := s.SetBacklogItemState(ctx, it.ID, domain.BacklogOpen, "ignored", backlogNow)
	if err != nil || reopened.ClosedAt != nil || reopened.ClosedReason != "" {
		t.Fatalf("reopen = %+v err=%v", reopened, err)
	}
	upd, err := s.UpdateBacklogItem(ctx, it.ID, backlogNow, func(i *domain.BacklogItem) error {
		f, err := domain.ApplyBacklogField(i.Fields, "branch", "feat/x")
		i.Fields = f
		return err
	})
	if err != nil || upd.Fields["branch"] != "feat/x" {
		t.Fatalf("field = %+v err=%v", upd, err)
	}
	got, ok, err := s.GetBacklogItem(ctx, it.ID)
	if err != nil || !ok || got.Fields["branch"] != "feat/x" {
		t.Fatalf("get = %+v ok=%v err=%v", got, ok, err)
	}
	if _, err := s.UpdateBacklogItem(ctx, it.ID, backlogNow, func(i *domain.BacklogItem) error { i.Scope = "bogus"; return nil }); err == nil {
		t.Fatal("bogus scope accepted by schema CHECK")
	}
}

func TestBacklogLinkTouchesUpdatedAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	it := addBacklog(t, s, "p1", "x", domain.BacklogOpen, "")
	later := backlogNow.Add(time.Hour)
	added, err := s.AddBacklogLink(ctx, it.ID, domain.BacklogLink{Kind: domain.BacklogLinkPR, Ref: "https://github.com/o/r/pull/1"}, later)
	if err != nil || !added {
		t.Fatalf("add = %v %v", added, err)
	}
	again, _ := s.AddBacklogLink(ctx, it.ID, domain.BacklogLink{Kind: domain.BacklogLinkPR, Ref: "https://github.com/o/r/pull/1"}, later)
	if again {
		t.Fatal("duplicate link reported as added")
	}
	got, _, _ := s.GetBacklogItem(ctx, it.ID)
	if !got.UpdatedAt.Equal(later) || len(got.Links) != 1 {
		t.Fatalf("got = %+v", got)
	}
}

func TestFileDeferredBacklog(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	n := 0
	in := func(session string, lines ...string) store.BacklogDeferInput {
		return store.BacklogDeferInput{ProjectID: "p1", SessionID: session, Lines: lines, Excerpt: "ex", Now: backlogNow,
			NewID: func() string { n++; return fmt.Sprintf("d-%d", n) }}
	}
	out, err := s.FileDeferredBacklog(ctx, in("s1", "Fix the  thing", "  fix THE thing ", "Other"))
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Status != store.BacklogDeferFiled || out[1].Status != store.BacklogDeferMerged || out[1].Ref != out[0].Ref || out[2].Status != store.BacklogDeferFiled {
		t.Fatalf("outcomes = %+v", out)
	}
	// Another session merges into the same item and gets linked.
	out, _ = s.FileDeferredBacklog(ctx, in("s2", "fix the thing"))
	if out[0].Status != store.BacklogDeferMerged {
		t.Fatalf("cross-session = %+v", out)
	}
	items, _ := s.ListBacklogItems(ctx, "p1", store.BacklogFilter{})
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	first, _, _ := s.GetBacklogItem(ctx, items[0].ID)
	if len(first.Links) != 2 || first.Origin.Kind != domain.BacklogOriginWorker || first.Origin.SessionID != "s1" || first.State != domain.BacklogInbox {
		t.Fatalf("first = %+v", first)
	}
	// A closed match does not block a new filing.
	if _, err := s.SetBacklogItemState(ctx, first.ID, domain.BacklogDone, "", backlogNow); err != nil {
		t.Fatal(err)
	}
	out, _ = s.FileDeferredBacklog(ctx, in("s2", "fix the thing"))
	if out[0].Status != store.BacklogDeferFiled {
		t.Fatalf("after close = %+v", out)
	}
}

func TestFileDeferredBacklogCaps(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	n := 0
	file := func(session, title string) store.BacklogDeferStatus {
		out, err := s.FileDeferredBacklog(ctx, store.BacklogDeferInput{ProjectID: "p1", SessionID: session, Lines: []string{title}, Now: backlogNow,
			NewID: func() string { n++; return fmt.Sprintf("cap-%d", n) }})
		if err != nil {
			t.Fatal(err)
		}
		return out[0].Status
	}
	// 30 per session, within the 50-item inbox cap.
	for i := 0; i < domain.MaxDeferPerSession; i++ {
		if st := file("s1", fmt.Sprintf("s1 item %d", i)); st != store.BacklogDeferFiled {
			t.Fatalf("item %d = %s", i, st)
		}
	}
	if st := file("s1", "one too many"); st != store.BacklogDeferSessionCap {
		t.Fatalf("session cap = %s", st)
	}
	// Another session can still file until the project inbox holds 50.
	for i := 0; i < domain.MaxBacklogInboxPerProject-domain.MaxDeferPerSession; i++ {
		if st := file("s2", fmt.Sprintf("s2 item %d", i)); st != store.BacklogDeferFiled {
			t.Fatalf("s2 item %d = %s", i, st)
		}
	}
	if st := file("s2", "inbox overflow"); st != store.BacklogDeferInboxFull {
		t.Fatalf("inbox cap = %s", st)
	}
	// A duplicate still merges when the inbox is full.
	if st := file("s3", "s1 item 0"); st != store.BacklogDeferMerged {
		t.Fatalf("merge at cap = %s", st)
	}
}

func TestBacklogListActiveStateAppliesLimitAfterFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	for i := 0; i < 3; i++ {
		addBacklog(t, s, "p1", "done", domain.BacklogDone, "")
	}
	addBacklog(t, s, "p1", "inbox", domain.BacklogInbox, "")
	addBacklog(t, s, "p1", "open", domain.BacklogOpen, "")
	items, err := s.ListBacklogItems(ctx, "p1", store.BacklogFilter{State: "active", Limit: 2})
	if err != nil || len(items) != 2 {
		t.Fatalf("active = %d err=%v, want the 2 inbox/open items", len(items), err)
	}
	for _, it := range items {
		if it.State != domain.BacklogInbox && it.State != domain.BacklogOpen {
			t.Fatalf("closed item in active list: %+v", it)
		}
	}
}
