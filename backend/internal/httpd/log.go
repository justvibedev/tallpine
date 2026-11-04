package httpd

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"tallpine.eu/tallpine/backend/internal/diag"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// slowRequestLogThreshold is the duration from which a successful request is
// logged at Info instead of Debug.
const slowRequestLogThreshold = time.Second

// isLongLivedRequest reports streams (SSE, WebSocket upgrades) whose duration is
// the connection lifetime and so must not feed request-latency percentiles.
func isLongLivedRequest(r *http.Request, responseContentType string) bool {
	return r.Header.Get("Upgrade") != "" || strings.HasPrefix(responseContentType, "text/event-stream")
}

// requestLogger emits one structured access-log line per request via the
// daemon's slog logger. Chi's built-in middleware.Logger writes to stdout
// using stdlib log; reusing the daemon's slog keeps every line on stderr in
// the same key=value shape as the rest of the daemon (one stream for the
// Electron supervisor to capture, one format to grep).
//
// Status, bytes, and duration come from a wrapped ResponseWriter so the log
// is accurate even when the handler returns without calling WriteHeader. The
// request id is read off the context populated by middleware.RequestID, so
// this middleware must be mounted after it.
//
// A 5xx line additionally carries the raw service error recorded by
// envelope.WriteError: the wire envelope hides internals ("Internal server
// error"), so without this the cause of a 500 was lost entirely.
//
// Successful, fast requests log at Debug: the renderer issues several per
// second, and at Info they dominated the daemon log. Failures (status >= 400)
// and slow requests (>= slowRequestLogThreshold, which includes every
// long-lived stream when it closes) stay at Info so a post-mortem still sees
// them. Bounded requests also feed the p95 latency in the metrics line.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			r, capturedErr := envelope.WithErrorCapture(r)
			start := time.Now()
			defer func() {
				attrs := []any{
					"id", middleware.GetReqID(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"status", ww.Status(),
					"bytes", ww.BytesWritten(),
					"duration", time.Since(start),
					"remote", r.RemoteAddr,
				}
				captured := capturedErr()
				if captured.Err != nil && ww.Status() >= http.StatusInternalServerError {
					attrs = append(attrs, "error", captured.Err)
				}
				// The client hung up mid-request (envelope maps that to 499). Say so
				// plainly instead of leaving a bare status to be read as a failure.
				if ww.Status() == envelope.StatusClientClosedRequest {
					attrs = append(attrs, "outcome", "client_cancelled")
				}
				elapsed := time.Since(start)
				if !isLongLivedRequest(r, ww.Header().Get("Content-Type")) {
					diag.ObserveRequest(elapsed)
				}
				level := slog.LevelInfo
				if ww.Status() < http.StatusBadRequest && elapsed < slowRequestLogThreshold {
					level = slog.LevelDebug
				}
				log.Log(context.Background(), level, "http request", attrs...)
			}()
			next.ServeHTTP(ww, r)
		})
	}
}
