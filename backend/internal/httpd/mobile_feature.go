package httpd

import (
	"net/http"
	"strings"

	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// mobileGatedPrefixes are the route families that only exist while the mobile
// feature flag is on: the Connect Mobile control surface, the push-device
// registration routes a phone calls, and the phone's endpoint refresh.
var mobileGatedPrefixes = []string{
	"/api/v1/mobile",
	"/api/v1/push",
	"/api/v1/endpoints",
	"/api/v1/tunnel",
}

// mobileFeatureOpen are the only mobile routes reachable while the flag is off:
// the status read (so Settings can render the toggle without a 404) and the
// flag's own setter. Neither starts a listener, tunnel or push relay, and
// status carries no secret while the bridge is not running.
func mobileFeatureOpen(method, path string) bool {
	return (method == http.MethodGet && path == "/api/v1/mobile/status") ||
		(method == http.MethodPost && path == "/api/v1/mobile/feature")
}

func isMobileGatedPath(path string) bool {
	for _, p := range mobileGatedPrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// mobileFeatureGate answers 404 for every mobile route while the flag is off,
// as if they were never mounted. A nil enabled func leaves routing unchanged
// (routers built directly in tests).
func mobileFeatureGate(enabled func() bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if enabled == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isMobileGatedPath(r.URL.Path) && !mobileFeatureOpen(r.Method, r.URL.Path) && !enabled() {
				envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "MOBILE_FEATURE_DISABLED",
					"Mobile access is turned off. Turn it on in Settings > Mobile.", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
