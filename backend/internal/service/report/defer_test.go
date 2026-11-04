package report

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type deferCall struct {
	project domain.ProjectID
	session domain.SessionID
	excerpt string
	lines   []string
}

func deferService(t *testing.T, filer func(context.Context, domain.ProjectID, domain.SessionID, string, []string) ([]domain.DeferOutcome, error)) (*Service, *fakeStore) {
	t.Helper()
	st := &fakeStore{ok: true, session: domain.SessionRecord{ID: "w1", ProjectID: "tp", Kind: domain.KindWorker}}
	return New(Deps{Store: st, FileDeferred: filer}), st
}

func TestCreateFilesDeferredLinesWithSessionAttribution(t *testing.T) {
	var got []deferCall
	svc, _ := deferService(t, func(_ context.Context, p domain.ProjectID, s domain.SessionID, ex string, lines []string) ([]domain.DeferOutcome, error) {
		got = append(got, deferCall{p, s, ex, lines})
		return []domain.DeferOutcome{{Line: lines[0], Status: domain.DeferFiled, Ref: "B1"}}, nil
	})
	rec, err := svc.Create(context.Background(), CreateInput{SessionID: "w1", State: domain.ReportDone, Note: "all done", Defer: []string{"  follow up  "}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].project != "tp" || got[0].session != "w1" || got[0].excerpt != "all done" || got[0].lines[0] != "follow up" {
		t.Fatalf("filer calls = %+v", got)
	}
	if len(rec.Deferred) != 1 || rec.Deferred[0].Ref != "B1" {
		t.Fatalf("deferred = %+v", rec.Deferred)
	}
}

func TestCreateRejectsInvalidDeferBeforeStoring(t *testing.T) {
	called := false
	svc, st := deferService(t, func(context.Context, domain.ProjectID, domain.SessionID, string, []string) ([]domain.DeferOutcome, error) {
		called = true
		return nil, nil
	})
	for name, lines := range map[string][]string{
		"four lines": {"a", "b", "c", "d"},
		"blank":      {" "},
		"too long":   {strings.Repeat("x", 201)},
	} {
		if _, err := svc.Create(context.Background(), CreateInput{SessionID: "w1", Message: "hi", Defer: lines}); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if called || st.created.ID != "" {
		t.Fatalf("filer called=%v stored=%+v; invalid defer must reject the whole report", called, st.created)
	}
	// The limits themselves are allowed.
	if _, err := svc.Create(context.Background(), CreateInput{SessionID: "w1", Message: "hi", Defer: []string{"a", "b", strings.Repeat("x", 200)}}); err != nil {
		t.Fatalf("at-limit defer rejected: %v", err)
	}
}

func TestCreateSurvivesFilingFailureAndReportsEveryLineUnfiled(t *testing.T) {
	svc, st := deferService(t, func(context.Context, domain.ProjectID, domain.SessionID, string, []string) ([]domain.DeferOutcome, error) {
		return nil, errors.New("db down")
	})
	rec, err := svc.Create(context.Background(), CreateInput{SessionID: "w1", Message: "hi", Defer: []string{"a", "b"}})
	if err != nil || st.created.ID == "" {
		t.Fatalf("report must still be stored: err=%v created=%+v", err, st.created)
	}
	if len(rec.Deferred) != 2 || rec.Deferred[0].Status != domain.DeferFailed || !strings.HasPrefix(rec.Deferred[0].Reason, "not filed") {
		t.Fatalf("deferred = %+v", rec.Deferred)
	}
	noFiler := New(Deps{Store: st})
	rec, err = noFiler.Create(context.Background(), CreateInput{SessionID: "w1", Message: "hi", Defer: []string{"a"}})
	if err != nil || len(rec.Deferred) != 1 || rec.Deferred[0].Status != domain.DeferFailed {
		t.Fatalf("no filer: %+v %v", rec.Deferred, err)
	}
}

func TestSynthesizedReportsNeverFileDeferred(t *testing.T) {
	called := false
	svc, _ := deferService(t, func(context.Context, domain.ProjectID, domain.SessionID, string, []string) ([]domain.DeferOutcome, error) {
		called = true
		return nil, nil
	})
	rec, err := svc.Create(context.Background(), CreateInput{SessionID: "w1", Message: "notice", Synthesized: true, Defer: []string{"x"}})
	if err != nil || called || len(rec.Deferred) != 0 {
		t.Fatalf("synthesized: called=%v deferred=%+v err=%v", called, rec.Deferred, err)
	}
}

func TestStandaloneSessionDeferIsReportedNotFiled(t *testing.T) {
	called := false
	st := &fakeStore{ok: true, session: domain.SessionRecord{ID: "solo", Kind: domain.KindWorker}}
	svc := New(Deps{Store: st, FileDeferred: func(context.Context, domain.ProjectID, domain.SessionID, string, []string) ([]domain.DeferOutcome, error) {
		called = true
		return nil, nil
	}})
	rec, err := svc.Create(context.Background(), CreateInput{SessionID: "solo", Message: "hi", Defer: []string{"later"}})
	if err != nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
	if len(rec.Deferred) != 1 || rec.Deferred[0].Reason != "not filed: no project" {
		t.Fatalf("deferred = %+v", rec.Deferred)
	}
}
