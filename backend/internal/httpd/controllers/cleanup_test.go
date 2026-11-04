package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	cleanupsvc "tallpine.eu/tallpine/backend/internal/service/cleanup"
)

type fakeCleanup struct {
	patch      string
	runReq     cleanupsvc.RunRequest
	runErr     error
	limit      int
	includeDry bool
}

func (f *fakeCleanup) Status(context.Context) (cleanupsvc.Status, error) {
	report := cleanupsvc.Report{ID: 7, Pass: cleanupsvc.PassFull, Trigger: cleanupsvc.TriggerSchedule, FreedBytes: 42,
		Categories: []cleanupsvc.CategoryReport{{Category: cleanupsvc.CategoryWorktrees, FreedBytes: 42, Freed: 1,
			Items: []cleanupsvc.Item{{SessionID: "s1", What: "worktree", Bytes: 42, Status: cleanupsvc.StatusFreed}}}}}
	return cleanupsvc.Status{Policy: cleanupsvc.DefaultPolicy(), LastRun: &report, NextLightAt: time.Unix(3600, 0)}, nil
}

func (f *fakeCleanup) UpdatePolicy(_ context.Context, patch []byte) (cleanupsvc.Policy, error) {
	f.patch = string(patch)
	return cleanupsvc.ApplyPatch(cleanupsvc.DefaultPolicy(), patch)
}

func (f *fakeCleanup) Run(_ context.Context, req cleanupsvc.RunRequest) (cleanupsvc.Report, error) {
	f.runReq = req
	if f.runErr != nil {
		return cleanupsvc.Report{}, f.runErr
	}
	return cleanupsvc.Report{ID: 9, Pass: req.Pass, DryRun: req.DryRun, Trigger: req.Trigger, Categories: []cleanupsvc.CategoryReport{}}, nil
}

func (f *fakeCleanup) History(_ context.Context, limit int, includeDry bool) ([]cleanupsvc.Report, error) {
	f.limit, f.includeDry = limit, includeDry
	return []cleanupsvc.Report{{ID: 1}}, nil
}

func cleanupRouter(svc CleanupService) http.Handler {
	r := chi.NewRouter()
	(&CleanupController{Svc: svc}).Register(r)
	return r
}

func serveCleanup(svc CleanupService, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	cleanupRouter(svc).ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestCleanup_Status(t *testing.T) {
	rec := serveCleanup(&fakeCleanup{}, http.MethodGet, "/cleanup", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d body %s", rec.Code, rec.Body)
	}
	var out CleanupStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Policy.Automatic || out.Policy.Artifacts.DeliverableAction != "archive" || out.LastRun == nil ||
		out.LastRun.FreedBytes != 42 || len(out.LastRun.Categories[0].Items) != 1 || out.LastDryRun != nil || out.NextLightRunAt == "" || out.NextFullRunAt != "" {
		t.Fatalf("status = %s", rec.Body)
	}
}

func TestCleanup_UpdatePolicy(t *testing.T) {
	svc := &fakeCleanup{}
	rec := serveCleanup(svc, http.MethodPatch, "/cleanup/policy", `{"worktrees":{"retentionDays":3}}`)
	if rec.Code != http.StatusOK || svc.patch != `{"worktrees":{"retentionDays":3}}` {
		t.Fatalf("code %d patch %q body %s", rec.Code, svc.patch, rec.Body)
	}
	var out CleanupPolicy
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Worktrees.RetentionDays != 3 {
		t.Fatalf("policy = %s (%v)", rec.Body, err)
	}
	for _, body := range []string{`{"worktrees":{"retentionDays":-1}}`, `{"unknown":true}`, `nope`} {
		rec := serveCleanup(svc, http.MethodPatch, "/cleanup/policy", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "CLEANUP_POLICY_INVALID") {
			t.Fatalf("%s: code %d body %s", body, rec.Code, rec.Body)
		}
	}
}

func TestCleanup_Run(t *testing.T) {
	svc := &fakeCleanup{}
	rec := serveCleanup(svc, http.MethodPost, "/cleanup/runs", `{"dryRun":true}`)
	if rec.Code != http.StatusOK || !svc.runReq.DryRun || svc.runReq.Pass != cleanupsvc.PassFull || svc.runReq.Trigger != cleanupsvc.TriggerManual || svc.runReq.ConfirmArtifacts {
		t.Fatalf("code %d req %+v body %s", rec.Code, svc.runReq, rec.Body)
	}
	rec = serveCleanup(svc, http.MethodPost, "/cleanup/runs", `{"pass":"light","confirmArtifacts":true}`)
	if rec.Code != http.StatusOK || svc.runReq.Pass != cleanupsvc.PassLight || !svc.runReq.ConfirmArtifacts || svc.runReq.DryRun {
		t.Fatalf("code %d req %+v", rec.Code, svc.runReq)
	}
	if rec := serveCleanup(svc, http.MethodPost, "/cleanup/runs", `{"pass":"deep"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid pass: code %d", rec.Code)
	}
	svc.runErr = fmt.Errorf("wrapped: %w", cleanupsvc.ErrBusy)
	rec = serveCleanup(svc, http.MethodPost, "/cleanup/runs", `{}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "CLEANUP_IN_PROGRESS") {
		t.Fatalf("busy: code %d body %s", rec.Code, rec.Body)
	}
}

func TestCleanup_History(t *testing.T) {
	svc := &fakeCleanup{}
	rec := serveCleanup(svc, http.MethodGet, "/cleanup/runs?limit=5&includeDryRuns=true", "")
	if rec.Code != http.StatusOK || svc.limit != 5 || !svc.includeDry || !strings.Contains(rec.Body.String(), `"runs"`) {
		t.Fatalf("code %d limit %d dry %t body %s", rec.Code, svc.limit, svc.includeDry, rec.Body)
	}
}

func TestCleanup_NotImplementedWithoutService(t *testing.T) {
	if rec := serveCleanup(nil, http.MethodGet, "/cleanup", ""); rec.Code != http.StatusNotImplemented {
		t.Fatalf("code %d", rec.Code)
	}
}
