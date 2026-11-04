package httpd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// TestRequestLoggerRecords5xxCause: the wire envelope collapses unrecognized
// service errors into "Internal server error", so the access log line is the
// only place the cause can survive. A 500 must carry it; a typed 4xx (whose
// envelope already explains itself) must not.
func TestRequestLoggerRecords5xxCause(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantInLog string
		absent    bool
	}{
		{name: "raw error on 500 is logged", err: errors.New("gitworktree: worktree remove exploded"), wantInLog: "gitworktree: worktree remove exploded"},
		{name: "typed 404 carries no error attr", err: apierr.NotFound("SESSION_NOT_FOUND", "Unknown session"), wantInLog: "error=", absent: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, nil))
			handler := requestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				envelope.WriteError(w, r, tc.err)
			}))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, loopbackRequest(http.MethodPost, "/api/v1/sessions/x/kill", nil))

			got := buf.String()
			if tc.absent {
				if strings.Contains(got, tc.wantInLog) {
					t.Fatalf("log line unexpectedly contains %q:\n%s", tc.wantInLog, got)
				}
				return
			}
			if !strings.Contains(got, tc.wantInLog) {
				t.Fatalf("log line missing %q:\n%s", tc.wantInLog, got)
			}
		})
	}
}

// TestRequestLoggerDemotesFastSuccess: the renderer issues several requests a
// second, so successes log at Debug; failures stay visible at Info.
func TestRequestLoggerDemotesFastSuccess(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil)) // Info
	status := http.StatusOK
	handler := requestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), loopbackRequest(http.MethodGet, "/api/v1/sessions", nil))
	if buf.Len() != 0 {
		t.Fatalf("fast 200 logged at Info:\n%s", buf.String())
	}
	status = http.StatusNotFound
	handler.ServeHTTP(httptest.NewRecorder(), loopbackRequest(http.MethodGet, "/api/v1/nope", nil))
	if !strings.Contains(buf.String(), "status=404") {
		t.Fatalf("404 not logged at Info:\n%s", buf.String())
	}

	buf.Reset()
	debugLog := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	status = http.StatusOK
	requestLogger(debugLog)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})).
		ServeHTTP(httptest.NewRecorder(), loopbackRequest(http.MethodGet, "/api/v1/sessions", nil))
	if !strings.Contains(buf.String(), "http request") {
		t.Fatalf("Debug level should still include fast successes:\n%s", buf.String())
	}
}

// TestRequestLoggerMarksClientCancel: a phone that aborts a superseded poll
// left "status=500 error=...context canceled" in the daemon log, which read as
// a server fault. The line must say the client cancelled, with no 500.
func TestRequestLoggerMarksClientCancel(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	handler := requestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelope.WriteError(w, r, fmt.Errorf("list review runs: %w", r.Context().Err()))
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handler.ServeHTTP(httptest.NewRecorder(), loopbackRequest(http.MethodGet, "/api/v1/sessions", nil).WithContext(ctx))
	got := buf.String()
	if !strings.Contains(got, "status=499") || !strings.Contains(got, "outcome=client_cancelled") || strings.Contains(got, "status=500") {
		t.Fatalf("want a 499 client_cancelled line, got:\n%s", got)
	}
}
