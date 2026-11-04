package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	updatesvc "tallpine.eu/tallpine/backend/internal/service/update"
)

type fakeUpdate struct {
	st    updatesvc.Status
	res   updatesvc.Result
	err   error
	got   string
	allow bool
}

func (f *fakeUpdate) Status(context.Context) (updatesvc.Status, error) { return f.st, nil }
func (f *fakeUpdate) Start(_ context.Context, c string, allow bool) (updatesvc.Result, error) {
	f.got, f.allow = c, allow
	return f.res, f.err
}

func serveUpdate(svc UpdateService, method, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	(&UpdateController{Svc: svc}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, "/update", strings.NewReader(body)))
	return rec
}

func TestUpdateStatusReportsCandidate(t *testing.T) {
	rec := serveUpdate(&fakeUpdate{st: updatesvc.Status{Candidate: "abc", UpdateAvailable: true, Last: &updatesvc.Result{State: "rolled_back"}}}, "GET", "")
	b := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(b, `"candidate":"abc"`) || !strings.Contains(b, `"updateAvailable":true`) || !strings.Contains(b, `"rolled_back"`) {
		t.Fatalf("%d %s", rec.Code, b)
	}
}

func TestUpdateStartMapsRefusals(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, http.StatusAccepted},
		{updatesvc.ErrNotCIPassed, http.StatusUnprocessableEntity},
		{updatesvc.ErrNotOnMain, http.StatusUnprocessableEntity},
		{updatesvc.ErrDowngrade, http.StatusUnprocessableEntity},
		{updatesvc.ErrRunning, http.StatusConflict},
		{updatesvc.ErrUnavailable, http.StatusServiceUnavailable},
	} {
		f := &fakeUpdate{err: tc.err, res: updatesvc.Result{State: "running"}}
		rec := serveUpdate(f, "POST", `{"commit":"abc1234"}`)
		if rec.Code != tc.want || f.got != "abc1234" {
			t.Errorf("err %v: %d %s (commit %q)", tc.err, rec.Code, rec.Body, f.got)
		}
	}
}

func TestUpdateStartRequiresCommit(t *testing.T) {
	f := &fakeUpdate{}
	if rec := serveUpdate(f, "POST", `{}`); rec.Code != http.StatusBadRequest || f.got != "" {
		t.Fatalf("%d", rec.Code)
	}
}

func TestUpdateStartPassesAllowDowngrade(t *testing.T) {
	f := &fakeUpdate{}
	serveUpdate(f, "POST", `{"commit":"abc1234","allowDowngrade":true}`)
	if !f.allow {
		t.Fatal("allowDowngrade not forwarded")
	}
}
