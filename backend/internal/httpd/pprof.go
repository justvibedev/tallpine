package httpd

import (
	"net/http"
	netpprof "net/http/pprof"
	"runtime"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

const pprofPrefix = "/debug/pprof/"

// Contention sampling the runtime does only while the profiler is reachable.
// Both rates are deliberately low: one contended mutex unlock in 100 is
// recorded, and a blocking wait is recorded with probability duration/10ms,
// so every wait of 10ms or more is always there.
const (
	pprofMutexProfileFraction = 100
	pprofBlockProfileRate     = 10 * time.Millisecond
)

// mountPprof serves Go's pprof handlers on the authenticated loopback listener
// so a hung daemon can be profiled without a restart (the 2026-10-25 slowdown
// left no goroutine or heap evidence). It is deliberately narrower than the
// rest of the API:
//
//   - It is only mounted when a daemon token exists; TALLPINE_DAEMON_AUTH=off leaves
//     the profiler unreachable rather than open.
//   - It re-checks the token itself (defense in depth behind
//     daemonAuthMiddleware) and refuses any non-loopback Host or Origin-bearing
//     request, answering 404 so the route is not advertised.
//   - The LAN listener blocks /debug/ (lanControlBlockedPrefixes), so a phone on
//     the network never reaches it.
//
// Mounting it also turns on low-rate mutex and block sampling, so
// /debug/pprof/mutex and /debug/pprof/block show where goroutines contend or
// wait instead of an empty profile.
//
// Usage: curl -H "X-VO-Token: $(cat ~/.tallpine/daemon.token)" \
//
//	http://127.0.0.1:3101/debug/pprof/goroutine?debug=2
func mountPprof(r chi.Router, token string) {
	if token == "" {
		return
	}
	runtime.SetMutexProfileFraction(pprofMutexProfileFraction)
	runtime.SetBlockProfileRate(int(pprofBlockProfileRate.Nanoseconds()))
	h := pprofGate(token, http.HandlerFunc(servePprof))
	r.Get("/debug/pprof", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, pprofPrefix, http.StatusMovedPermanently)
	})
	r.Get(pprofPrefix+"*", h.ServeHTTP)
}

func pprofGate(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestscope.IsLAN(r.Context()) || !localControlRequest(r) || !presentedTokenValid(r, token) {
			notFoundJSON(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func servePprof(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimPrefix(r.URL.Path, pprofPrefix) {
	case "cmdline":
		netpprof.Cmdline(w, r)
	case "profile":
		netpprof.Profile(w, r)
	case "symbol":
		netpprof.Symbol(w, r)
	case "trace":
		netpprof.Trace(w, r)
	default:
		// Index also serves the named profiles (heap, goroutine, ...).
		netpprof.Index(w, r)
	}
}
