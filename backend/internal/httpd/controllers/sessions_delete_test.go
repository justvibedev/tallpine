package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

type deleteService struct {
	*fakeSessionService
	id      domain.SessionID
	cleanup bool
	err     error
}

func (s *deleteService) Delete(_ context.Context, id domain.SessionID, cleanup bool) error {
	s.id, s.cleanup = id, cleanup
	return s.err
}

func TestDeleteSessionRoute(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		err         error
		wantCode    int
		wantCleanup bool
		wantBody    string
	}{
		{name: "ok", wantCode: 200, wantBody: `"sessionId":"mer-1"`},
		{name: "cleanup", query: "?cleanup=true", wantCode: 200, wantCleanup: true},
		{name: "cleanup other value", query: "?cleanup=1", wantCode: 200},
		{name: "conflict", err: apierr.Conflict("WORKSPACE_NOT_RELEASED", "not released", nil), wantCode: 409, wantBody: "WORKSPACE_NOT_RELEASED"},
		{name: "not found", err: apierr.NotFound("SESSION_NOT_FOUND", "Unknown session"), wantCode: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &deleteService{fakeSessionService: newFakeSessionService(), err: tc.err}
			r := chi.NewRouter()
			r.Route("/api/v1", (&controllers.SessionsController{Svc: svc}).Register)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/mer-1"+tc.query, nil))
			if rec.Code != tc.wantCode || svc.id != "mer-1" || svc.cleanup != tc.wantCleanup {
				t.Fatalf("code=%d id=%q cleanup=%v body=%s", rec.Code, svc.id, svc.cleanup, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body = %s, want %s", rec.Body, tc.wantBody)
			}
			if tc.wantCode == 200 {
				var res controllers.DeleteSessionResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || !res.OK || res.SessionID != "mer-1" {
					t.Fatalf("response = %+v err=%v", res, err)
				}
			}
		})
	}
}
