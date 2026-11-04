package httpd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

func newAuthUnderTest(pw string, now func() time.Time) (http.Handler, *lockout) {
	st := &authState{}
	h := mobilebridge.HashPassword(pw)
	armTestBearer(st, h)
	lock := newLockout(now)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	return authMiddleware(st, lock)(ok), lock
}

func req(auth string) *http.Request {
	r := loopbackRequest(http.MethodGet, "/api/v1/sessions", nil)
	r.RemoteAddr = "192.168.1.50:5555"
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	return r
}

func reqFrom(remoteAddr, auth string) *http.Request {
	r := loopbackRequest(http.MethodGet, "/api/v1/sessions", nil)
	r.RemoteAddr = remoteAddr
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	return overTLS(r) // past the encrypted-transport gate; see lan_plain_refused_test.go
}

func TestAuthLockoutResetsAfterCooldown(t *testing.T) {
	nowP := time.Now()
	h, _ := newAuthUnderTest("secret12", func() time.Time { return nowP })
	// Lock the source with 5 failures.
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req("Bearer wrong"))
	}
	// Still within cooldown → 429 even with the right password.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer secret12"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("during cooldown: got %d want 429", w.Code)
	}
	// Advance past the 1-minute cooldown.
	nowP = nowP.Add(time.Minute + time.Second)
	// A single WRONG attempt must NOT immediately re-lock — it starts a fresh
	// window and returns 401, not 429.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer wrong"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("first attempt after cooldown: got %d want 401 (fresh window, not re-locked)", w.Code)
	}
	// And the correct password now succeeds.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer secret12"))
	if w.Code != http.StatusOK {
		t.Fatalf("correct password after cooldown: got %d want 200", w.Code)
	}
}

func TestAuthRejectsMissingAndWrong(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	for _, tc := range []struct {
		name, auth string
		want       int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong", "Bearer nope", http.StatusUnauthorized},
		{"right", "Bearer secret12", http.StatusOK},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req(tc.auth))
		if w.Code != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, w.Code, tc.want)
		}
	}
}

func TestAuthLockoutAfterFive(t *testing.T) {
	now := time.Now()
	h, _ := newAuthUnderTest("secret12", func() time.Time { return now })
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req("Bearer wrong"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d want 401", i, w.Code)
		}
	}
	// 6th attempt — even with the RIGHT password — is locked out.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer secret12"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked attempt: got %d want 429", w.Code)
	}
}

func TestValidTunnelClientSurvivesAnotherClientsLockout(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	for range 5 {
		w := httptest.NewRecorder()
		r := reqFrom("127.0.0.1:5555", "Bearer wrong")
		r.Header.Set("CF-Connecting-IP", "198.51.100.1")
		r = viaCloudflaredListener(r)
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("bad tunnel request: got %d want 401", w.Code)
		}
	}
	w := httptest.NewRecorder()
	r := reqFrom("127.0.0.1:6666", "Bearer secret12")
	r.Header.Set("CF-Connecting-IP", "203.0.113.2")
	r = viaCloudflaredListener(r)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("valid tunnel client sharing connector IP: got %d want 200", w.Code)
	}
	w = httptest.NewRecorder()
	r = reqFrom("127.0.0.1:5555", "Bearer wrong")
	r.Header.Set("CF-Connecting-IP", "198.51.100.1")
	r = viaCloudflaredListener(r)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("bad tunnel client after valid request: got %d want 429", w.Code)
	}
}

func TestLANClientCannotSpoofTunnelSource(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	for i := range 5 {
		r := reqFrom("192.168.1.50:5555", "Bearer wrong")
		r.Header.Set("CF-Connecting-IP", fmt.Sprintf("203.0.113.%d", i+1))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("bad LAN request %d: got %d want 401", i, w.Code)
		}
	}
	r := reqFrom("192.168.1.50:6666", "Bearer secret12")
	r.Header.Set("CF-Connecting-IP", "203.0.113.99")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed LAN source: got %d want 429", w.Code)
	}
}

func TestLockoutPrunesAbandonedSources(t *testing.T) {
	now := time.Now()
	lock := newLockout(func() time.Time { return now })
	lock.fail("abandoned")
	now = now.Add(time.Minute + time.Second)
	lock.fail("current")
	if _, ok := lock.attempts["abandoned"]; ok {
		t.Fatal("expired source remains in lockout map")
	}
}

func TestRotatedPasswordCanRecoverFromStaleClientLockout(t *testing.T) {
	state := &authState{}
	armTestBearer(state, mobilebridge.HashPassword("oldpass1"))
	lock := newLockout(time.Now)
	h := authMiddleware(state, lock)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	armTestBearer(state, mobilebridge.HashPassword("newpass1"))
	for range 5 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req("Bearer oldpass1"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("stale client: got %d want 401", w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer newpass1"))
	if w.Code != http.StatusOK {
		t.Fatalf("rotated password from same source: got %d want 200", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer oldpass1"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("old password after recovery: got %d want 401", w.Code)
	}
}

func TestRetiredPasswordDoesNotResetLockout(t *testing.T) {
	state := &authState{}
	armTestBearer(state, mobilebridge.HashPassword("oldpass1"))
	armTestBearer(state, mobilebridge.HashPassword("newpass1"))
	lock := newLockout(time.Now)
	h := authMiddleware(state, lock)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for range 5 {
		for _, password := range []string{"oldpass1", "wrong"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req("Bearer "+password))
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s attempt: got %d want 401", password, w.Code)
			}
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer newpass1"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("new password after five genuinely wrong guesses: got %d want 429", w.Code)
	}
}

func TestLANManagerPasswordRotationClearsPriorLockout(t *testing.T) {
	m := NewMobileLAN(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "", 0, nil)
	armTestBearerLAN(m, mobilebridge.HashPassword("oldpass1"))
	for range 5 {
		w := httptest.NewRecorder()
		m.handler.ServeHTTP(w, req("Bearer wrong"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("failed attempt: got %d want 401", w.Code)
		}
	}
	w := httptest.NewRecorder()
	m.handler.ServeHTTP(w, req("Bearer oldpass1"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("before rotation: got %d want 429", w.Code)
	}
	armTestBearerLAN(m, mobilebridge.HashPassword("newpass1"))
	w = httptest.NewRecorder()
	m.handler.ServeHTTP(w, req("Bearer newpass1"))
	if w.Code != http.StatusOK {
		t.Fatalf("new password after rotation: got %d want 200", w.Code)
	}
}

// reqPathCookie builds a request to an arbitrary path, optionally carrying the
// Bearer header and/or the preview auth cookie, for the preview-cookie tests.
func reqPathCookie(method, path, auth, cookie string) *http.Request {
	r := loopbackRequest(method, path, nil)
	r.RemoteAddr = "192.168.1.50:5555"
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: authCookieName, Value: cookie})
	}
	return r
}

// A preview subresource (image/CSS/JS) is fetched by the WebView WITHOUT our
// Authorization header, carrying only the cookie the top-level load set. It must
// authenticate on the preview-files path.
func TestPreviewCookieAuthenticatesSubresource(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqPathCookie(http.MethodGet, "/api/v1/sessions/abc/preview/files/logo.png", "", "secret12"))
	if w.Code != http.StatusOK {
		t.Fatalf("preview subresource with cookie: got %d want 200", w.Code)
	}
}

// The top-level preview file load (Bearer header) must set the auth cookie,
// scoped tightly to that session's preview-files directory and HttpOnly.
func TestPreviewFileSetsScopedCookie(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqPathCookie(http.MethodGet, "/api/v1/sessions/abc/preview/files/index.html", "Bearer secret12", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("preview index with bearer: got %d want 200", w.Code)
	}
	var c *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == authCookieName {
			c = ck
		}
	}
	if c == nil {
		t.Fatal("expected auth cookie on preview file response")
		return
	}
	if c.Path != "/api/v1/sessions/abc/preview/files/" { //nolint:staticcheck // SA5011 false positive: t.Fatal above halts the test
		t.Errorf("cookie Path = %q, want /api/v1/sessions/abc/preview/files/", c.Path)
	}
	if !c.HttpOnly {
		t.Error("cookie must be HttpOnly")
	}
}

// After a password regenerate the WebView still holds the cookie minted under the
// OLD password. The top-level load re-authenticates via the Bearer header (the
// mobile app has the new password), so the server must overwrite the stale cookie
// — otherwise the page's subresources keep sending the old token and 401.
func TestPreviewCookieRefreshedAfterPasswordChange(t *testing.T) {
	h, _ := newAuthUnderTest("newpass12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqPathCookie(http.MethodGet,
		"/api/v1/sessions/abc/preview/files/index.html", "Bearer newpass12", "oldpass12"))
	if w.Code != http.StatusOK {
		t.Fatalf("preview index with new bearer + stale cookie: got %d want 200", w.Code)
	}
	var c *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == authCookieName {
			c = ck
		}
	}
	if c == nil {
		t.Fatal("expected stale auth cookie to be refreshed")
		return
	}
	if c.Value != "newpass12" { //nolint:staticcheck // SA5011 false positive: t.Fatal above halts the test
		t.Errorf("cookie Value = %q, want the current token newpass12", c.Value)
	}
}

// The cookie must NOT authenticate any non-preview endpoint: a preview page that
// tries POST /kill with only the cookie is rejected. This is the server-side
// half of the guarantee (the cookie's Path already stops the browser sending it
// here at all).
func TestPreviewCookieRejectedOnOtherEndpoints(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqPathCookie(http.MethodPost, "/api/v1/sessions/abc/kill", "", "secret12"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie on /kill: got %d want 401", w.Code)
	}
}

// A normal (non-preview) authenticated request must not get an auth cookie set,
// so the cookie only ever exists for the preview flow.
func TestNoCookieSetOnNonPreviewRoutes(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer secret12")) // path /api/v1/sessions
	if w.Code != http.StatusOK {
		t.Fatalf("got %d want 200", w.Code)
	}
	for _, ck := range w.Result().Cookies() {
		if ck.Name == authCookieName {
			t.Fatal("auth cookie must not be set on a non-preview route")
		}
	}
}

func TestAuthLockoutIsPerSource(t *testing.T) {
	now := time.Now()
	h, _ := newAuthUnderTest("secret12", func() time.Time { return now })

	// Source A: lock with 5 failed attempts from 192.168.1.50
	sourceA := "192.168.1.50:5555"
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, reqFrom(sourceA, "Bearer wrong"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("source A attempt %d: got %d want 401", i, w.Code)
		}
	}
	// Verify source A is now locked
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqFrom(sourceA, "Bearer secret12"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("source A locked check: got %d want 429", w.Code)
	}

	// Source B: should NOT be locked despite source A being locked
	sourceB := "192.168.1.99:6666"
	// B with correct password should be 200, not 429
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqFrom(sourceB, "Bearer secret12"))
	if w.Code != http.StatusOK {
		t.Fatalf("source B with correct password: got %d want 200", w.Code)
	}

	// B with wrong password should be 401, not 429
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqFrom(sourceB, "Bearer wrong"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("source B with wrong password: got %d want 401", w.Code)
	}
}

// reqTo builds a request to an arbitrary path so the identity exemption can be
// probed alongside the normal authenticated surface.
func reqTo(method, path, auth string) *http.Request {
	r := loopbackRequest(method, path, nil)
	r.RemoteAddr = "192.168.1.50:5555"
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	return r
}

// The phone must be able to ask "who are you?" before it presents a
// credential. Without this, verifying an endpoint's identity would require
// sending the bearer token to whatever device happens to hold that private IP
// on the current network — see docs/adr/0003.
func TestAuthExemptsIdentityProbe(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqTo(http.MethodGet, "/api/v1/identity", ""))

	if w.Code != http.StatusOK {
		t.Fatalf("unauthenticated identity probe got %d, want 200", w.Code)
	}
}

// The exemption is one method on one exact path. These are the guards that
// stop it widening into a general hole in the LAN listener.
func TestAuthExemptionIsNarrow(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)

	for _, tc := range []struct {
		name, method, path string
		want               int
	}{
		{"exact path, GET", http.MethodGet, "/api/v1/identity", http.StatusOK},
		{"write to the same path", http.MethodPost, "/api/v1/identity", http.StatusUnauthorized},
		{"nested below it", http.MethodGet, "/api/v1/identity/secrets", http.StatusUnauthorized},
		{"prefix collision", http.MethodGet, "/api/v1/identityzzz", http.StatusUnauthorized},
		{"trailing slash", http.MethodGet, "/api/v1/identity/", http.StatusUnauthorized},
		{"unrelated route", http.MethodGet, "/api/v1/sessions", http.StatusUnauthorized},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, reqTo(tc.method, tc.path, ""))
		if w.Code != tc.want {
			t.Errorf("%s (%s %s): got %d want %d", tc.name, tc.method, tc.path, w.Code, tc.want)
		}
	}
}

// A probe carrying no credential must not count toward the lockout, or an
// unauthenticated phone racing several endpoints would lock itself out.
func TestIdentityProbeDoesNotCountTowardLockout(t *testing.T) {
	h, lock := newAuthUnderTest("secret12", time.Now)

	for range 10 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, reqTo(http.MethodGet, "/api/v1/identity", ""))
	}

	if lock.blocked("192.168.1.50") {
		t.Fatal("identity probes triggered the lockout")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer secret12"))
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated request after probes got %d, want 200", w.Code)
	}
}

func TestAuthLockoutGroupsIPv6ByPrefix(t *testing.T) {
	h, _ := newAuthUnderTest("goodpass", time.Now)
	// Five guesses from five addresses in one /64 lock the whole /64 out.
	for i := 1; i <= sourceFailureLimit; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, reqFrom(fmt.Sprintf("[2001:db8:1:2::%x]:5555", i), "Bearer wrong"))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, reqFrom("[2001:db8:1:2::ffff]:5555", "Bearer goodpass"))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("another address in the same /64 = %d, want 429", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, reqFrom("[2001:db8:1:3::1]:5555", "Bearer goodpass"))
	if rr.Code != http.StatusOK {
		t.Fatalf("a different /64 = %d, want 200", rr.Code)
	}
}

func TestAuthGlobalBudgetStopsRotatingSources(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	h, _ := newAuthUnderTest("goodpass", clock)

	// A phone that paired earlier.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, reqFrom("192.168.1.20:5555", "Bearer goodpass"))
	if rr.Code != http.StatusOK {
		t.Fatalf("pairing request = %d", rr.Code)
	}
	// One wrong guess per address never trips a source's own limit.
	for i := 0; i < globalFailureLimit; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, reqFrom(fmt.Sprintf("10.0.%d.%d:5555", i/250, i%250+1), "Bearer wrong"))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d = %d, want 401", i, rr.Code)
		}
	}
	// The budget is spent: a fresh source is refused even with the password,
	// which is what keeps the guesser from ever learning it worked.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, reqFrom("10.9.9.9:5555", "Bearer goodpass"))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("fresh source after the budget = %d, want 429", rr.Code)
	}
	// Spoofing a visitor address through the loopback bridge does not help.
	spoofed := reqFrom("127.0.0.1:5555", "Bearer goodpass")
	spoofed.Header.Set("CF-Connecting-IP", "203.0.113.77")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, spoofed)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed visitor after the budget = %d, want 429", rr.Code)
	}
	// The paired phone keeps working.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, reqFrom("192.168.1.20:6666", "Bearer goodpass"))
	if rr.Code != http.StatusOK {
		t.Fatalf("paired phone during the lockout = %d, want 200", rr.Code)
	}
	// A guesser naming an install id gains nothing.
	moved := reqFrom("100.70.1.2:5555", "Bearer goodpass")
	moved.Header.Set(InstallIDHeader, "phone-install-1")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, moved)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("unpaired install during the lockout = %d, want 429", rr.Code)
	}
	now = now.Add(lockoutWindow)
	paired := reqFrom("192.168.1.20:7777", "Bearer goodpass")
	paired.Header.Set(InstallIDHeader, "phone-install-1")
	h.ServeHTTP(httptest.NewRecorder(), paired)
	for i := 0; i < globalFailureLimit; i++ {
		h.ServeHTTP(httptest.NewRecorder(), reqFrom(fmt.Sprintf("10.1.%d.%d:5555", i/250, i%250+1), "Bearer wrong"))
	}
	// P2-L1: an install id is not secret and never lifts the lockout, even a
	// "paired" one that authenticated before from another address.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, moved)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("known install id from a new address during the lockout = %d, want 429", rr.Code)
	}
	// The window ends.
	now = now.Add(lockoutWindow)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, reqFrom("10.9.9.9:5555", "Bearer goodpass"))
	if rr.Code != http.StatusOK {
		t.Fatalf("fresh source after the window = %d, want 200", rr.Code)
	}
}

// viaCloudflaredListener marks r as accepted on the listener reserved for
// cloudflared, the only place CF-Connecting-IP is believed.
func viaCloudflaredListener(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), cloudflaredContextKey{}, true))
}

// Tailscale Serve (and any other local proxy) connects from loopback too, but
// not through the listener reserved for cloudflared: its CF-Connecting-IP is
// forgeable by a tailnet peer, so it must not choose the lockout source.
func TestForgedCFHeaderFromOtherLoopbackPeerIsIgnored(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	for i := range 5 {
		r := reqFrom("127.0.0.1:5555", "Bearer wrong")
		r.Header.Set("CF-Connecting-IP", fmt.Sprintf("203.0.113.%d", i+1))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("bad proxied request %d: got %d want 401", i, w.Code)
		}
	}
	r := reqFrom("127.0.0.1:6666", "Bearer secret12")
	r.Header.Set("CF-Connecting-IP", "203.0.113.99")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("forged header moved the lockout source: got %d want 429", w.Code)
	}
}
