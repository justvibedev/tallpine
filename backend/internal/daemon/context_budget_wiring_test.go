package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/service/contextbudget"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// A Yes files an open backlog item and starts a worker that has no parent
// session and is linked to that item.
func TestContextHealthYesFilesItemAndLinkedWorker(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	backlog := backlogsvc.New(backlogsvc.Deps{Store: st})
	spawns := &captureSpawns{}
	w := contextHealthWork{backlog: backlog, sessions: spawns}

	item, err := w.FileItem(ctx, "p1", contextbudget.ItemInput{Title: "Trim", Note: "evidence", DoneWhen: "under budget"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.StartWorker(ctx, "p1", item, "BRIEF"); err != nil {
		t.Fatal(err)
	}
	if len(spawns.cfgs) != 1 {
		t.Fatalf("spawns = %d, want 1", len(spawns.cfgs))
	}
	cfg := spawns.cfgs[0]
	if cfg.Kind != domain.KindWorker || cfg.ParentSessionID != "" || !strings.Contains(cfg.BacklogContext, "BRIEF") || !strings.Contains(cfg.BacklogContext, "Trim") {
		t.Fatalf("spawn = %+v", cfg)
	}
	view, err := backlog.Get(ctx, "p1", item.Ref)
	if err != nil || view.State != domain.BacklogOpen {
		t.Fatalf("item = %+v err=%v, want an open item", view, err)
	}
	linked := false
	for _, l := range view.Links {
		linked = linked || (l.Kind == domain.BacklogLinkSession && l.Ref == "s1")
	}
	if !linked {
		t.Fatalf("links = %+v, want the worker session linked", view.Links)
	}
}
