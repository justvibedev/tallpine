package controllers_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	prsvc "tallpine.eu/tallpine/backend/internal/service/pr"
)

type fakePRService struct {
	mergeResult   prsvc.MergeResult
	mergeErr      error
	mergeRequest  prsvc.MergeRequest
	resolveResult prsvc.ResolveResult
	resolveErr    error
}

func (f *fakePRService) Merge(_ context.Context, request prsvc.MergeRequest) (prsvc.MergeResult, error) {
	f.mergeRequest = request
	return f.mergeResult, f.mergeErr
}

func (f *fakePRService) ResolveComments(_ context.Context, _ string, _ []string) (prsvc.ResolveResult, error) {
	return f.resolveResult, f.resolveErr
}

func newPRTestServer(t *testing.T, svc prsvc.ActionManager) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{PRs: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

const validMergeBody = `{"prUrl":"https://github.com/acme/widgets/pull/42","expectedHeadSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`

// ---- Nil service → 503 SCM_NOT_CONFIGURED ----

func TestPRsRoutes_NilService_MergeReturns501(t *testing.T) {
	srv := newPRTestServer(t, nil)
	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestPRsRoutes_NilService_ResolveCommentsReturns501(t *testing.T) {
	srv := newPRTestServer(t, nil)
	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/resolve-comments", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

// ---- Merge: 200 ----

func TestPRsRoutes_Merge_200(t *testing.T) {
	stateChangedAt := time.Date(2026, 10, 19, 11, 6, 22, 0, time.UTC)
	svc := &fakePRService{mergeResult: prsvc.MergeResult{
		PRNumber: 42, Method: "squash", State: domain.PRStateMerged,
		MergeCommitSHA: "merge-sha", StateChangedAt: stateChangedAt,
	}}
	srv := newPRTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/merge", validMergeBody)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		OK             bool           `json:"ok"`
		PRNumber       int            `json:"prNumber"`
		Method         string         `json:"method"`
		State          domain.PRState `json:"state"`
		StateChangedAt time.Time      `json:"stateChangedAt"`
	}
	mustJSON(t, body, &resp)
	if !resp.OK || resp.PRNumber != 42 || resp.Method != "squash" || resp.State != domain.PRStateMerged || !resp.StateChangedAt.Equal(stateChangedAt) {
		t.Errorf("unexpected merge response: %+v", resp)
	}
	if svc.mergeRequest.PRID != "42" || svc.mergeRequest.PRURL != "https://github.com/acme/widgets/pull/42" || svc.mergeRequest.ExpectedHeadSHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("merge request = %#v", svc.mergeRequest)
	}
}

// ---- Merge: 404 ----

func TestPRsRoutes_Merge_404(t *testing.T) {
	svc := &fakePRService{mergeErr: prsvc.ErrPRNotFound}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/99/merge", validMergeBody)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotFound, "PR_NOT_FOUND")
}

// ---- Merge: 409 ----

func TestPRsRoutes_Merge_409(t *testing.T) {
	svc := &fakePRService{mergeErr: prsvc.ErrPRNotMergeable}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", validMergeBody)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusConflict, "PR_NOT_MERGEABLE")
}

// ---- Merge: 422 ----

func TestPRsRoutes_Merge_422(t *testing.T) {
	svc := &fakePRService{mergeErr: prsvc.ErrPRPreconditions}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", validMergeBody)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "PR_PRECONDITIONS_UNMET")
}

func TestPRsRoutes_Merge_422NamesFailedPrecondition(t *testing.T) {
	svc := &fakePRService{mergeErr: &prsvc.PreconditionError{Blocker: domain.MergeBlockerCIUnknown}}
	srv := newPRTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", validMergeBody)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "PR_PRECONDITIONS_UNMET")
	var resp struct {
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	mustJSON(t, body, &resp)
	if resp.Details["reason"] != "ci_unknown" || resp.Details["detail"] != domain.MergeBlockerCIUnknown.Describe() {
		t.Fatalf("details = %#v, want reason ci_unknown with its description", resp.Details)
	}
	if !strings.Contains(resp.Message, "CI status is unknown") {
		t.Fatalf("message = %q, want the failed precondition", resp.Message)
	}
}

func TestPRsRoutes_Merge_422KeepsWrappedPreconditionText(t *testing.T) {
	svc := &fakePRService{mergeErr: fmt.Errorf("%w: pull request repository is unknown", prsvc.ErrPRPreconditions)}
	srv := newPRTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", validMergeBody)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "PR_PRECONDITIONS_UNMET")
	var resp struct {
		Details map[string]any `json:"details"`
	}
	mustJSON(t, body, &resp)
	if resp.Details["detail"] != "pull request repository is unknown" {
		t.Fatalf("details = %#v", resp.Details)
	}
}

// ---- ResolveComments: 200 ----

func TestPRsRoutes_ResolveComments_200(t *testing.T) {
	svc := &fakePRService{resolveResult: prsvc.ResolveResult{Resolved: 3}}
	srv := newPRTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/resolve-comments", `{"commentIds":["T_1","T_2","T_3"]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		OK       bool `json:"ok"`
		Resolved int  `json:"resolved"`
	}
	mustJSON(t, body, &resp)
	if !resp.OK || resp.Resolved != 3 {
		t.Errorf("resp = %+v, want {ok:true resolved:3}", resp)
	}
}

func TestPRsRoutes_ResolveComments_200_NoBody(t *testing.T) {
	svc := &fakePRService{resolveResult: prsvc.ResolveResult{Resolved: 2}}
	srv := newPRTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/resolve-comments", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
}

// ---- ResolveComments: 404 ----

func TestPRsRoutes_ResolveComments_404(t *testing.T) {
	svc := &fakePRService{resolveErr: prsvc.ErrPRNotFound}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/99/resolve-comments", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotFound, "PR_NOT_FOUND")
}

// ---- ResolveComments: 422 ----

func TestPRsRoutes_ResolveComments_422(t *testing.T) {
	svc := &fakePRService{resolveErr: prsvc.ErrNothingToResolve}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/resolve-comments", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "NOTHING_TO_RESOLVE")
}

func TestPRsRoutes_ResolveComments_InvalidPRID(t *testing.T) {
	svc := &fakePRService{resolveResult: prsvc.ResolveResult{Resolved: 0}}
	srv := newPRTestServer(t, svc)

	for _, id := range []string{"0", "abc", "-1"} {
		body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/"+id+"/resolve-comments", "")
		assertJSON(t, headers)
		assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_PR")
	}
}

func TestPRsRoutes_Merge_500CarriesRedactedCause(t *testing.T) {
	cause := fmt.Errorf("merge pull request: POST https://x-access-token:ghs_abcdefghijklmnopqrstuvwxyz0123@api.github.com/graphql: Authorization: Bearer ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345 rejected; token=glpat-abcdefghijklmnopqrst; retry Authorization: token fedcba9876543210fedcba9876543210fedcba98 rejected\x1b[31m")
	srv := newPRTestServer(t, &fakePRService{mergeErr: cause})
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", validMergeBody)
	assertErrorCode(t, body, status, http.StatusInternalServerError, "PR_OPERATION_FAILED")
	var resp struct {
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	mustJSON(t, body, &resp)
	got, _ := resp.Details["cause"].(string)
	if !strings.Contains(got, "merge pull request: POST https://[redacted]@api.github.com/graphql") || !strings.Contains(resp.Message, got) {
		t.Fatalf("cause = %q, message = %q", got, resp.Message)
	}
	for _, secret := range []string{"ghs_", "ghp_", "glpat-", "x-access-token", "\x1b", "fedcba9876543210"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("response leaks %q: %s", secret, body)
		}
	}
}

func TestPRsRoutes_Merge_500CapsCauseLength(t *testing.T) {
	srv := newPRTestServer(t, &fakePRService{mergeErr: errors.New(strings.Repeat("x", 2000))})
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", validMergeBody)
	assertErrorCode(t, body, status, http.StatusInternalServerError, "PR_OPERATION_FAILED")
	var resp struct {
		Details map[string]any `json:"details"`
	}
	mustJSON(t, body, &resp)
	if got, _ := resp.Details["cause"].(string); len([]rune(got)) > 501 {
		t.Fatalf("cause length = %d, want capped", len([]rune(got)))
	}
}
