package httpd

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// lanRequestLog records each request the LAN listener serves: method, path,
// status, source and duration. Without it a phone that reached the listener
// and was refused left no trace, so "did the phone get here?" had no answer.
//
// Only the path is logged, never the query or headers: WebSocket URLs and the
// Authorization header carry credentials. Refusals (4xx/5xx) and pairing are
// logged at Info so they show in the daemon log by default; the steady stream
// of successful polls goes to Debug.
func lanRequestLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status == 0 {
			// Hijacked for a WebSocket before any header was written.
			status = http.StatusSwitchingProtocols
		}
		level := slog.LevelDebug
		if status >= http.StatusBadRequest || r.URL.Path == lanPairExchangePath {
			level = slog.LevelInfo
		}
		log.Log(r.Context(), level, "lan request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"remote", clientSource(r),
			"ms", time.Since(start).Milliseconds(),
		)
	})
}
