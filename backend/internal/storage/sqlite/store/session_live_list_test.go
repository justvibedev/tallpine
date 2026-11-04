package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func TestListLiveSessionsExcludesTerminated(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	live, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	done.IsTerminated = true
	if err := s.UpdateSession(ctx, done); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListLiveSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != live.ID {
		t.Fatalf("live sessions = %+v, want only %s", got, live.ID)
	}
	all, err := s.ListAllSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all sessions = %d, want 2", len(all))
	}
}

func TestListLivePreviewSessionsReturnsOnlyLiveWorkersWithAPreview(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	create := func(kind domain.SessionKind, previewURL string, terminated bool) domain.SessionRecord {
		t.Helper()
		rec := sampleRecord("mer")
		rec.Kind = kind
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		if previewURL != "" {
			if _, err := s.SetSessionPreviewURL(ctx, created.ID, previewURL, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		if terminated {
			current, _, err := s.GetSession(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			current.IsTerminated = true
			if err := s.UpdateSession(ctx, current); err != nil {
				t.Fatal(err)
			}
		}
		return created
	}
	want := create(domain.KindWorker, "http://127.0.0.1:3101/preview/index.html", false)
	create(domain.KindWorker, "", false)
	create(domain.KindWorker, "http://127.0.0.1:3101/preview/old.html", true)
	create(domain.KindOrchestrator, "http://127.0.0.1:3101/preview/orch.html", false)

	got, err := s.ListLivePreviewSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != want.ID || got[0].Metadata.PreviewURL == "" || got[0].Metadata.WorkspacePath != "/ws" {
		t.Fatalf("live preview sessions = %+v, want only %s with its preview and workspace", got, want.ID)
	}
}

// benchmarkSessionHistory seeds a long-lived install: many finished sessions,
// a handful of live ones, one of them previewing a workspace file.
func benchmarkSessionHistory(b *testing.B) *sqlite.Store {
	b.Helper()
	s := sqlitetest.MustOpen(b)
	ctx := context.Background()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: time.Now().UTC()}); err != nil {
		b.Fatal(err)
	}
	for i := range 1000 {
		rec := sampleRecord("mer")
		rec.Metadata.Prompt = strings.Repeat("a realistic task prompt ", 40)
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			b.Fatal(err)
		}
		if i == 0 {
			if _, err := s.SetSessionPreviewURL(ctx, created.ID, "http://127.0.0.1:3101/preview/index.html", time.Now()); err != nil {
				b.Fatal(err)
			}
		}
		if i >= 10 {
			created.IsTerminated = true
			if err := s.UpdateSession(ctx, created); err != nil {
				b.Fatal(err)
			}
		}
	}
	return s
}

// BenchmarkPreviewPollListAllSessions is what one preview poll read before.
func BenchmarkPreviewPollListAllSessions(b *testing.B) {
	s := benchmarkSessionHistory(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.ListAllSessions(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPreviewPollListLivePreviewSessions(b *testing.B) {
	s := benchmarkSessionHistory(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.ListLivePreviewSessions(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

func TestCountArchivedSessionsGroupsTerminatedByProjectAndKind(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	create := func(project domain.ProjectID, kind domain.SessionKind, terminated, preparation bool) {
		t.Helper()
		rec := sampleRecord(string(project))
		rec.Kind = kind
		rec.IsTaskPreparation = preparation
		rec, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		if terminated {
			rec.IsTerminated = true
			if err := s.UpdateSession(ctx, rec); err != nil {
				t.Fatal(err)
			}
		}
	}
	create("mer", domain.KindWorker, false, false)
	create("mer", domain.KindWorker, true, false)
	create("mer", domain.KindWorker, true, false)
	create("mer", domain.KindOrchestrator, true, false)
	create("mer", domain.KindWorker, true, true)
	create("", domain.KindWorker, true, false)

	got, err := s.CountArchivedSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[domain.ProjectID]map[domain.SessionKind]int{}
	for _, c := range got {
		if counts[c.ProjectID] == nil {
			counts[c.ProjectID] = map[domain.SessionKind]int{}
		}
		counts[c.ProjectID][c.Kind] += c.Count
	}
	want := map[domain.ProjectID]map[domain.SessionKind]int{
		"mer": {domain.KindWorker: 2, domain.KindOrchestrator: 1},
		"":    {domain.KindWorker: 1},
	}
	if fmt.Sprint(counts) != fmt.Sprint(want) {
		t.Fatalf("archived counts = %v, want %v", counts, want)
	}
}
