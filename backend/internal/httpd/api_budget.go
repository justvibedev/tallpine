package httpd

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"tallpine.eu/tallpine/backend/internal/config"
)

// browserHandoffBudget outlasts the longest handoff (30m, plus the browser
// service's 15s runtime grace; backend/internal/service/browser/handoff.go).
const browserHandoffBudget = 32 * time.Minute

// requestTimeout is the ordinary /api/v1 request budget.
func requestTimeout(cfg config.Config) time.Duration {
	if cfg.RequestTimeout <= 0 {
		return config.DefaultRequestTimeout
	}
	return cfg.RequestTimeout
}

// requestBudget bounds each /api/v1 request by its route's budget: the
// configured timeout, or longer for the routes that do more inside one request.
//
// Large base64 bodies can spend longer than the ordinary REST budget
// uploading over a phone connection. Only attachment-bearing requests
// opt in; ordinary calls to the same routes keep the configured timeout.
func (a *API) requestBudget(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		ordinary := middleware.Timeout(timeout)(next)
		upload := middleware.Timeout(max(timeout, 10*time.Minute))(next)
		clone := middleware.Timeout(max(timeout, 30*time.Minute))(next)
		browserCommands := middleware.Timeout(max(timeout, browserHandoffBudget))(next)
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// A clone downloads the whole repository inside the request,
			// which takes minutes for a large repository.
			// A manual cleanup run measures and removes whole worktrees and
			// artifact trees inside the request.
			if pattern := chi.RouteContext(req.Context()).RoutePattern(); pattern == "/api/v1/projects/clone" ||
				pattern == "/api/v1/projects/clone/prepare" ||
				(pattern == "/api/v1/cleanup/runs" && req.Method == http.MethodPost) ||
				pattern == "/api/v1/storage/cleanup" || pattern == "/api/v1/storage/delete" {
				clone.ServeHTTP(w, req)
				return
			}
			// A spawn, an orchestrator start or a restore runs the project's
			// Setup inside the request, and a Setup step may take far longer
			// than one REST request: the operation extends this budget by its
			// Setup steps' limits once it knows its project.
			if setupRoute(req) {
				budget := timeout
				if a.cfg.SpawnTimeout > 0 {
					budget = a.cfg.SpawnTimeout
				}
				if attachmentUploadRoute(req) {
					budget = max(budget, 10*time.Minute)
				}
				serveWithSetupBudget(next, w, req, budget)
				return
			}
			if attachmentUploadRoute(req) {
				upload.ServeHTTP(w, req)
				return
			}
			// A browser handoff waits on the person for up to 30 minutes
			// (B858). The action is in the body, so the controller holds
			// every other browser command to the ordinary budget.
			if pattern := chi.RouteContext(req.Context()).RoutePattern(); pattern == "/api/v1/browser/commands" && req.Method == http.MethodPost {
				browserCommands.ServeHTTP(w, req)
				return
			}
			// A managed preview carries the dev server's HMR socket and event
			// streams, and the agent browser its stream socket: both stay open
			// as long as the page does.
			if pattern := chi.RouteContext(req.Context()).RoutePattern(); pattern == "/api/v1/sessions/{sessionId}/preview/app/*" ||
				pattern == "/api/v1/sessions/{sessionId}/agent-browser/*" {
				next.ServeHTTP(w, req)
				return
			}
			ordinary.ServeHTTP(w, req)
		})
	}
}
