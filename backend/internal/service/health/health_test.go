package health

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeSource struct {
	chats     []domain.SessionID
	batches   []domain.StuckReportBatch
	fallbacks []domain.OverdueTurnFallback
	err       error
	cutoffs   []time.Time
}

func (f *fakeSource) ListChatSessionsExpectedLive(_ context.Context, at time.Time) ([]domain.SessionID, error) {
	f.cutoffs = append(f.cutoffs, at)
	return f.chats, f.err
}
func (f *fakeSource) ListStuckReportBatches(context.Context, time.Time) ([]domain.StuckReportBatch, error) {
	return f.batches, f.err
}
func (f *fakeSource) ListOverdueTurnFallbacks(context.Context, time.Time) ([]domain.OverdueTurnFallback, error) {
	return f.fallbacks, f.err
}

type fakeChat map[domain.SessionID]bool

func (f fakeChat) HasLiveChatController(id domain.SessionID) bool { return f[id] }

type fakeParking map[domain.ProjectID]time.Time

func (f fakeParking) UndeliverableProjects() map[domain.ProjectID]time.Time { return f }

type fakeSessions map[domain.SessionID]domain.ProjectID

func (f fakeSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	p, ok := f[id]
	return domain.SessionRecord{ID: id, ProjectID: p}, ok, nil
}

var now = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func TestStatusHealthy(t *testing.T) {
	svc := &Service{Source: &fakeSource{chats: []domain.SessionID{"a"}}, Chat: fakeChat{"a": true}, Reports: fakeParking{}, Now: func() time.Time { return now }}
	st, err := svc.Status(context.Background())
	if err != nil || !st.Healthy() {
		t.Fatalf("status = %+v err %v, want healthy", st, err)
	}
	if st.ChatNotAttached == nil || st.ReportBatches == nil || st.TurnFallbacks == nil {
		t.Fatalf("slices must be non-nil: %+v", st)
	}
}

func TestStatusChatNotAttached(t *testing.T) {
	svc := &Service{Source: &fakeSource{chats: []domain.SessionID{"live", "dead"}}, Chat: fakeChat{"live": true}, Now: func() time.Time { return now }}
	st, _ := svc.Status(context.Background())
	if len(st.ChatNotAttached) != 1 || st.ChatNotAttached[0].SessionID != "dead" {
		t.Fatalf("chat issues = %+v, want only the detached session", st.ChatNotAttached)
	}
}

func TestStatusReportBatches(t *testing.T) {
	src := &fakeSource{batches: []domain.StuckReportBatch{{ProjectID: "p1", Pending: 2, Since: now.Add(-time.Hour)}}}
	svc := &Service{Source: src, Reports: fakeParking{
		"p1": now.Add(-time.Hour),   // also parked: same row, flagged
		"p2": now.Add(-time.Hour),   // unconfirmed with no DB row past due
		"p3": now.Add(-time.Second), // parked moments ago: inside grace
	}, Now: func() time.Time { return now }}
	st, _ := svc.Status(context.Background())
	if len(st.ReportBatches) != 2 {
		t.Fatalf("report issues = %+v, want p1 and p2 only", st.ReportBatches)
	}
	if p1 := st.ReportBatches[0]; p1.ProjectID != "p1" || p1.Pending != 2 || !p1.Undeliverable {
		t.Fatalf("p1 = %+v", p1)
	}
	if p2 := st.ReportBatches[1]; p2.ProjectID != "p2" || !p2.Undeliverable {
		t.Fatalf("p2 = %+v", p2)
	}
}

func TestStatusTurnFallbacksAndGrace(t *testing.T) {
	src := &fakeSource{fallbacks: []domain.OverdueTurnFallback{{ID: "f", SessionID: "s", DueAt: now.Add(-time.Hour)}}}
	svc := &Service{Source: src, Now: func() time.Time { return now }}
	st, _ := svc.Status(context.Background())
	if len(st.TurnFallbacks) != 1 || st.TurnFallbacks[0].ID != "f" {
		t.Fatalf("fallbacks = %+v", st.TurnFallbacks)
	}
	if len(src.cutoffs) != 1 || !src.cutoffs[0].Equal(now.Add(-Grace)) {
		t.Fatalf("grace cutoff = %v, want now-%v", src.cutoffs, Grace)
	}
}

func TestStatusPropagatesSourceError(t *testing.T) {
	svc := &Service{Source: &fakeSource{err: errors.New("db down")}}
	if _, err := svc.Status(context.Background()); err == nil {
		t.Fatal("want error")
	}
}

func TestStatusForProjectFiltersToOwnProject(t *testing.T) {
	src := &fakeSource{
		chats:     []domain.SessionID{"a-1", "b-1", "gone"},
		batches:   []domain.StuckReportBatch{{ProjectID: "a", Pending: 1, Since: now.Add(-time.Hour)}, {ProjectID: "b", Pending: 1, Since: now.Add(-time.Hour)}},
		fallbacks: []domain.OverdueTurnFallback{{ID: "fa", SessionID: "a-1"}, {ID: "fb", SessionID: "b-1"}},
	}
	svc := &Service{Source: src, Sessions: fakeSessions{"a-1": "a", "b-1": "b"}, Now: func() time.Time { return now }}
	st, err := svc.StatusForProject(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.ChatNotAttached) != 1 || st.ChatNotAttached[0].SessionID != "a-1" ||
		len(st.ReportBatches) != 1 || st.ReportBatches[0].ProjectID != "a" ||
		len(st.TurnFallbacks) != 1 || st.TurnFallbacks[0].ID != "fa" {
		t.Fatalf("status = %+v, want only project a", st)
	}
}

func TestStatusForProjectEmptyProjectFailsClosed(t *testing.T) {
	src := &fakeSource{
		chats:   []domain.SessionID{"x"},
		batches: []domain.StuckReportBatch{{ProjectID: "", Pending: 1, Since: now.Add(-time.Hour)}},
	}
	svc := &Service{Source: src, Sessions: fakeSessions{"x": ""}, Now: func() time.Time { return now }}
	st, err := svc.StatusForProject(context.Background(), "")
	if err != nil || !st.Healthy() {
		t.Fatalf("status = %+v err %v, want empty", st, err)
	}
}

func TestStatusForProjectWithoutLookupLeaksNothing(t *testing.T) {
	svc := &Service{Source: &fakeSource{chats: []domain.SessionID{"a-1"}}, Now: func() time.Time { return now }}
	st, _ := svc.StatusForProject(context.Background(), "a")
	if len(st.ChatNotAttached) != 0 {
		t.Fatalf("status = %+v", st)
	}
}

func TestUnattachedChatWarnsOncePerOccurrence(t *testing.T) {
	var buf bytes.Buffer
	chat := fakeChat{}
	svc := &Service{Source: &fakeSource{chats: []domain.SessionID{"dead"}}, Sessions: fakeSessions{"dead": "p"}, Chat: chat,
		Log: slog.New(slog.NewTextHandler(&buf, nil)), Now: func() time.Time { return now }}
	for i := 0; i < 2; i++ {
		if _, err := svc.Status(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(buf.String(), "no attached agent"); n != 1 {
		t.Fatalf("warn count = %d, want 1: %s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "sessionId=dead") || !strings.Contains(buf.String(), "sinceLastUpdate=") {
		t.Fatalf("missing fields: %s", buf.String())
	}
	chat["dead"] = true // recovers, then recurs
	_, _ = svc.Status(context.Background())
	chat["dead"] = false
	_, _ = svc.Status(context.Background())
	if n := strings.Count(buf.String(), "no attached agent"); n != 2 {
		t.Fatalf("warn count after recurrence = %d, want 2", n)
	}
}
