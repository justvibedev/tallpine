package httpd

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

const pprofTestToken = "pprof-test-token"

func pprofGET(t *testing.T, h http.Handler, path string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := loopbackRequest(http.MethodGet, path, nil)
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPprofIsTokenGatedAndLoopbackOnly(t *testing.T) {
	router := NewRouterWithControl(config.Config{}, discardLogger(), nil, APIDeps{}, ControlDeps{DaemonToken: pprofTestToken})
	withToken := func(r *http.Request) { r.Header.Set(daemontoken.Header, pprofTestToken) }

	if rec := pprofGET(t, router, "/debug/pprof/", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rec.Code)
	}
	if rec := pprofGET(t, router, "/debug/pprof/", func(r *http.Request) { r.Header.Set(daemontoken.Header, "wrong") }); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", rec.Code)
	}
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/goroutine?debug=1", "/debug/pprof/cmdline", "/debug/pprof/heap?debug=1"} {
		if rec := pprofGET(t, router, path, withToken); rec.Code != http.StatusOK {
			t.Fatalf("%s with token: status = %d, want 200", path, rec.Code)
		}
	}
	// Token in the query string is never accepted.
	if rec := pprofGET(t, router, "/debug/pprof/?token="+pprofTestToken, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("query token: status = %d, want 401", rec.Code)
	}
	// A token-bearing request that arrived through the LAN listener must not reach it.
	lan := func(r *http.Request) {
		withToken(r)
		*r = *r.WithContext(requestscope.WithLAN(r.Context()))
	}
	if rec := pprofGET(t, pprofGate(pprofTestToken, http.HandlerFunc(servePprof)), "/debug/pprof/", lan); rec.Code != http.StatusNotFound {
		t.Fatalf("LAN request: status = %d, want 404", rec.Code)
	}
	if !isLANControlBlockedPath("/debug/pprof/heap") {
		t.Fatal("LAN listener must block /debug/")
	}
}

func TestPprofNotMountedWithoutDaemonToken(t *testing.T) {
	router := NewRouterWithControl(config.Config{}, discardLogger(), nil, APIDeps{}, ControlDeps{})
	if rec := pprofGET(t, router, "/debug/pprof/", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("auth off: status = %d, want 404 (profiler must not be open)", rec.Code)
	}
}

// TestPprofEnablesContentionProfiles: the mutex and block profiles are empty
// unless the runtime samples them, and a hang caused by lock contention or a
// blocked channel is exactly what the profiler was added to diagnose.
func TestPprofEnablesContentionProfiles(t *testing.T) {
	runtime.SetMutexProfileFraction(0)
	runtime.SetBlockProfileRate(0)
	t.Cleanup(func() {
		runtime.SetMutexProfileFraction(0)
		runtime.SetBlockProfileRate(0)
	})
	router := NewRouterWithControl(config.Config{}, discardLogger(), nil, APIDeps{}, ControlDeps{DaemonToken: pprofTestToken})
	withToken := func(r *http.Request) { r.Header.Set(daemontoken.Header, pprofTestToken) }

	if got := runtime.SetMutexProfileFraction(-1); got != pprofMutexProfileFraction {
		t.Fatalf("mutex profile fraction = %d, want %d", got, pprofMutexProfileFraction)
	}
	rec := pprofGET(t, router, "/debug/pprof/mutex?debug=1", withToken)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "sampling period=100") {
		t.Fatalf("mutex profile: status = %d body = %q, want sampling period=100", rec.Code, rec.Body.String())
	}

	// A wait at least as long as the block rate is always recorded.
	ch := make(chan struct{})
	go func() {
		time.Sleep(2 * pprofBlockProfileRate)
		close(ch)
	}()
	blockOnChannel(ch)
	rec = pprofGET(t, router, "/debug/pprof/block?debug=1", withToken)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "blockOnChannel") {
		t.Fatalf("block profile does not record a %s channel wait: status = %d body = %q", 2*pprofBlockProfileRate, rec.Code, rec.Body.String())
	}
}

//go:noinline
func blockOnChannel(ch <-chan struct{}) { <-ch }

func TestPprofLeavesContentionProfilesOffWithoutDaemonToken(t *testing.T) {
	runtime.SetMutexProfileFraction(0)
	runtime.SetBlockProfileRate(0)
	NewRouterWithControl(config.Config{}, discardLogger(), nil, APIDeps{}, ControlDeps{})
	if got := runtime.SetMutexProfileFraction(-1); got != 0 {
		t.Fatalf("mutex profile fraction = %d without a reachable profiler, want 0", got)
	}
}
