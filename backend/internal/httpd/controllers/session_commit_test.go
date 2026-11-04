package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/sessioncommit"
)

type fakeCommit struct {
	err     error
	session domain.SessionID
	message string
}

func (f *fakeCommit) Commit(_ context.Context, s domain.SessionID, m string) (sessioncommit.Result, error) {
	f.session, f.message = s, m
	return sessioncommit.Result{SHA: "abc123"}, f.err
}

func TestSessionCommitStatuses(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 200},
		{sessioncommit.ErrSessionNotFound, 404},
		{sessioncommit.ErrBadMessage, 400},
		{sessioncommit.ErrNothingToCommit, 409},
		{sessioncommit.ErrNotOwnWorktree, 409},
		{sessioncommit.ErrNoWorkspace, 409},
		{errors.New("boom"), 500},
	}
	for _, c := range cases {
		f := &fakeCommit{err: c.err}
		r := chi.NewRouter()
		(&SessionCommitController{Svc: f}).Register(r)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/sessions/s9/commit", strings.NewReader(`{"message":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("err %v: status %d, want %d (%s)", c.err, rec.Code, c.want, rec.Body)
		}
		if f.session != "s9" || f.message != "hi" {
			t.Errorf("service got %q %q", f.session, f.message)
		}
	}
}

func TestSessionCommitNotImplementedWithoutService(t *testing.T) {
	r := chi.NewRouter()
	(&SessionCommitController{}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/s/commit", strings.NewReader(`{"message":"x"}`)))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status %d", rec.Code)
	}
}
