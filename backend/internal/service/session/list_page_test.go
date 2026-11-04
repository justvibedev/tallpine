package session

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// recordingStore notes which sessions a list read loaded PR facts for.
type recordingStore struct {
	*fakeStore
	prFactIDs []domain.SessionID
}

func (r *recordingStore) ListPRFactsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.PRFacts, error) {
	r.prFactIDs = append(r.prFactIDs, ids...)
	return r.fakeStore.ListPRFactsForSessions(ctx, ids)
}

func archivedRecord(id string, project domain.ProjectID, updated time.Time) domain.SessionRecord {
	return domain.SessionRecord{ID: domain.SessionID(id), ProjectID: project, Kind: domain.KindWorker, IsTerminated: true, CreatedAt: updated, UpdatedAt: updated}
}

func pageIDs(page ListPage) []string {
	out := make([]string, 0, len(page.Sessions))
	for _, sess := range page.Sessions {
		out = append(out, string(sess.ID))
	}
	return out
}

func boolPtr(v bool) *bool { return &v }

// The archive pages pinned first, then most recently updated (desktop's
// archive order), with the id breaking ties, whatever order the store returns.
func TestListPageOrdersTheArchiveAndResumesAfterTheCursor(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	for i := 1; i <= 7; i++ {
		rec := archivedRecord(fmt.Sprintf("a-%d", i), "tp", base.Add(time.Duration(i)*time.Hour))
		st.sessions[rec.ID] = rec
	}
	tie := archivedRecord("a-9", "tp", base.Add(4*time.Hour)) // same updatedAt as a-4
	st.sessions[tie.ID] = tie
	pinned := archivedRecord("a-0", "tp", base)
	pinned.IsPinned = true
	st.sessions[pinned.ID] = pinned
	st.sessions["live"] = domain.SessionRecord{ID: "live", ProjectID: "tp", Kind: domain.KindWorker}

	svc := &Service{store: st}
	filter := ListFilter{Active: boolPtr(false), Limit: 4}
	var got []string
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("cursor never ran out")
		}
		page, err := svc.ListPage(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Sessions) > filter.Limit {
			t.Fatalf("page of %d, limit %d", len(page.Sessions), filter.Limit)
		}
		got = append(got, pageIDs(page)...)
		if page.NextCursor == "" {
			break
		}
		filter.Cursor = page.NextCursor
	}
	want := []string{"a-0", "a-7", "a-6", "a-5", "a-9", "a-4", "a-3", "a-2", "a-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("archive order = %v, want %v", got, want)
	}
}

// A cursor is a position, not an offset: a session archived between two page
// reads lands at the top and neither repeats nor pushes a row out of page two.
func TestListPageCursorSurvivesNewArchiveRowsBetweenPages(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	for i := 1; i <= 4; i++ {
		rec := archivedRecord(fmt.Sprintf("a-%d", i), "tp", base.Add(time.Duration(i)*time.Hour))
		st.sessions[rec.ID] = rec
	}
	svc := &Service{store: st}
	first, err := svc.ListPage(context.Background(), ListFilter{Active: boolPtr(false), Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	newest := archivedRecord("a-new", "tp", base.Add(10*time.Hour))
	st.sessions[newest.ID] = newest
	second, err := svc.ListPage(context.Background(), ListFilter{Active: boolPtr(false), Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pageIDs(first), []string{"a-4", "a-3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first page = %v, want %v", got, want)
	}
	if got, want := pageIDs(second), []string{"a-2", "a-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second page = %v, want %v", got, want)
	}
	if second.NextCursor != "" {
		t.Fatalf("last page cursor = %q, want none", second.NextCursor)
	}
}

// Paging happens before the read model is built, so a page of a big archive
// loads PR facts for that page only.
func TestListPageLoadsFactsForThePageOnly(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := &recordingStore{fakeStore: newFakeStore()}
	for i := 1; i <= 60; i++ {
		rec := archivedRecord(fmt.Sprintf("a-%02d", i), "tp", base.Add(time.Duration(i)*time.Minute))
		st.sessions[rec.ID] = rec
	}
	page, err := (&Service{store: st}).ListPage(context.Background(), ListFilter{Active: boolPtr(false), Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 5 || len(st.prFactIDs) != 5 {
		t.Fatalf("page %d sessions, PR facts loaded for %d; want 5 and 5", len(page.Sessions), len(st.prFactIDs))
	}
}

// The live list says how big the archive it left out is, per project and
// under the same kind filter, so a phone can show Archive's count without
// downloading it.
func TestListPageSummarisesTheArchiveBehindAnActiveList(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	st.sessions["live"] = domain.SessionRecord{ID: "live", ProjectID: "tp", Kind: domain.KindWorker}
	st.sessions["legacy-live"] = domain.SessionRecord{ID: "legacy-live", ProjectID: "tp"} // written before kinds
	st.sessions["tp-1"] = archivedRecord("tp-1", "tp", now)
	st.sessions["tp-2"] = archivedRecord("tp-2", "tp", now)
	st.sessions["web-1"] = archivedRecord("web-1", "web", now)
	st.sessions["solo"] = archivedRecord("solo", "", now)
	orch := archivedRecord("tp-orch", "tp", now)
	orch.Kind = domain.KindOrchestrator
	st.sessions[orch.ID] = orch
	st.sessions["prep"] = domain.SessionRecord{ID: "prep", ProjectID: "tp", Kind: domain.KindWorker, IsTerminated: true, IsTaskPreparation: true}
	svc := &Service{store: st}

	page, err := svc.ListPage(context.Background(), ListFilter{Active: boolPtr(true), Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	ids := pageIDs(page)
	if len(ids) != 2 {
		t.Fatalf("live workers = %v, want live and legacy-live", ids)
	}
	want := &ArchiveSummary{Total: 4, ByProject: map[domain.ProjectID]int{"tp": 2, "web": 1, "": 1}}
	if !reflect.DeepEqual(page.Archived, want) {
		t.Fatalf("archive summary = %+v, want %+v", page.Archived, want)
	}

	scoped, err := svc.ListPage(context.Background(), ListFilter{ProjectID: "web", Active: boolPtr(true), Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Archived == nil || scoped.Archived.Total != 1 {
		t.Fatalf("project-scoped summary = %+v, want total 1", scoped.Archived)
	}

	for _, filter := range []ListFilter{{}, {Active: boolPtr(false)}} {
		page, err := svc.ListPage(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		if page.Archived != nil {
			t.Fatalf("filter %+v reported an archive summary %+v", filter, page.Archived)
		}
	}
}

func TestListPageRejectsBadPagingInput(t *testing.T) {
	t.Parallel()

	svc := &Service{store: newFakeStore()}
	cursor := encodeSessionCursor(listPageKey{id: "a-1", updatedAt: time.Now().UTC()})
	for name, filter := range map[string]ListFilter{
		"garbage cursor":       {Limit: 10, Cursor: "%%%"},
		"cursor without id":    {Limit: 10, Cursor: "MAoyMDI2LTEwLTAxVDAwOjAwOjAwWgo"},
		"cursor without limit": {Cursor: cursor},
		"limit too large":      {Limit: MaxListLimit + 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.ListPage(context.Background(), filter)
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid {
				t.Fatalf("err = %v, want an invalid-input error", err)
			}
		})
	}
}

// An unpaged list is unchanged: every match, no cursor.
func TestListWithoutLimitReturnsEveryMatch(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	for i := 0; i < MaxListLimit+5; i++ {
		rec := archivedRecord(fmt.Sprintf("a-%03d", i), "tp", time.Now())
		st.sessions[rec.ID] = rec
	}
	page, err := (&Service{store: st}).ListPage(context.Background(), ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != MaxListLimit+5 || page.NextCursor != "" {
		t.Fatalf("unpaged list = %d sessions, cursor %q", len(page.Sessions), page.NextCursor)
	}
}

// An active-only list reads only live rows plus the archive's counts. For every
// filter it must keep the same sessions and report the same archive as
// filtering every stored row would.
func TestActiveListFromLiveRowsMatchesFullScan(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	add := func(rec domain.SessionRecord) { st.sessions[rec.ID] = rec }
	add(domain.SessionRecord{ID: "tallpine-live", ProjectID: "tp", Kind: domain.KindWorker})
	add(domain.SessionRecord{ID: "tallpine-legacy", ProjectID: "tp"})
	add(domain.SessionRecord{ID: "tp-orch", ProjectID: "tp", Kind: domain.KindOrchestrator})
	add(domain.SessionRecord{ID: "web-live", ProjectID: "web", Kind: domain.KindWorker})
	add(domain.SessionRecord{ID: "solo-live", Kind: domain.KindWorker})
	add(domain.SessionRecord{ID: "prep-live", ProjectID: "tp", Kind: domain.KindWorker, IsTaskPreparation: true})
	add(archivedRecord("tp-1", "tp", now))
	add(archivedRecord("tp-2", "tp", now))
	add(archivedRecord("web-1", "web", now))
	add(archivedRecord("solo-1", "", now))
	legacy := archivedRecord("tallpine-legacy-1", "tp", now)
	legacy.Kind = ""
	add(legacy)
	orch := archivedRecord("tp-orch-1", "tp", now)
	orch.Kind = domain.KindOrchestrator
	add(orch)
	add(domain.SessionRecord{ID: "prep-1", ProjectID: "web", Kind: domain.KindWorker, IsTerminated: true, IsTaskPreparation: true})

	ctx := context.Background()
	all, _ := st.ListAllSessions(ctx)
	live, _ := st.ListLiveSessions(ctx)
	counts, _ := st.CountArchivedSessions(ctx)
	ids := func(recs []domain.SessionRecord) []string {
		out := make([]string, 0, len(recs))
		for _, rec := range recs {
			out = append(out, string(rec.ID))
		}
		sort.Strings(out)
		return out
	}
	for _, project := range []domain.ProjectID{"", "tp", "web"} {
		for _, kind := range []domain.SessionKind{"", domain.KindWorker, domain.KindOrchestrator} {
			for _, flags := range []ListFilter{{}, {OrchestratorOnly: true}, {Fresh: true}, {Standalone: true}} {
				filter := flags
				filter.ProjectID, filter.Kind, filter.Active = project, kind, boolPtr(true)
				scoped := all
				if project != "" {
					scoped = nil
					for _, rec := range all {
						if rec.ProjectID == project {
							scoped = append(scoped, rec)
						}
					}
				}
				wantRecs, wantArchive := selectSessionRecords(scoped, filter)
				gotRecs, gotArchive := selectLiveSessionRecords(live, counts, filter)
				if fmt.Sprint(ids(gotRecs)) != fmt.Sprint(ids(wantRecs)) {
					t.Errorf("filter %+v: sessions %v, want %v", filter, ids(gotRecs), ids(wantRecs))
				}
				if !reflect.DeepEqual(gotArchive, wantArchive) {
					t.Errorf("filter %+v: archive %+v, want %+v", filter, gotArchive, wantArchive)
				}
			}
		}
	}
}

// The list the app polls must not decode the archive to answer.
func TestActiveListDoesNotReadEverySession(t *testing.T) {
	st := newFakeStore()
	st.sessions["live"] = domain.SessionRecord{ID: "live", ProjectID: "tp", Kind: domain.KindWorker}
	st.sessions["old"] = archivedRecord("old", "tp", time.Now())
	svc := &Service{store: st}
	for _, filter := range []ListFilter{{Active: boolPtr(true)}, {ProjectID: "tp", Active: boolPtr(true), Limit: 10}} {
		if _, err := svc.ListPage(context.Background(), filter); err != nil {
			t.Fatal(err)
		}
	}
	if st.allListCalls != 0 || st.liveListCalls != 2 {
		t.Fatalf("active lists read all sessions %d times and live sessions %d times; want 0 and 2", st.allListCalls, st.liveListCalls)
	}
}
