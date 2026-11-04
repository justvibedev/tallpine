package httpd

import (
	"net/http"
	"strings"
)

// noStoreAPIMiddleware sets `Cache-Control: no-store` as the default on every
// /api/v1/* response. Chromium otherwise heuristically caches the large JSON
// bodies (the session list is multi-MB) and rewrites its disk cache on every
// refetch. The header is set before the handler runs, so handlers that
// deliberately opt into revalidation (`private, no-cache` on content-addressed
// blobs) or streaming (`no-cache` on SSE) simply overwrite it.
func noStoreAPIMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/v1/") {
				w.Header().Set("Cache-Control", "no-store")
			}
			next.ServeHTTP(w, r)
		})
	}
}
