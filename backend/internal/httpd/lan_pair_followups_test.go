package httpd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// A phone retrying with a code the daemon already consumed is not guessing: it
// gets PAIRING_CODE_USED (not a "rotated password" 401) and no lockout strike.
func TestReusedPairingCodeIsDistinctAndFreeOfLockout(t *testing.T) {
	m, _ := newPairingManager(t, "Code1234")
	tokenFrom(t, exchange(m, "192.168.1.40:5000", "", `{"code":"Code1234","installId":"i-1"}`))
	for range sourceFailureLimit + 2 {
		w := exchange(m, "192.168.1.40:5001", "", `{"code":"Code1234","installId":"i-1"}`)
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "PAIRING_CODE_USED") {
			t.Fatalf("reused code = %d %s, want 401 PAIRING_CODE_USED", w.Code, w.Body)
		}
	}
	// Never locked out, so a fresh code from the same source still redeems.
	m.SetPasswordHash(mobilebridge.HashPassword("Fresh5678"))
	tokenFrom(t, exchange(m, "192.168.1.40:5002", "", `{"code":"Fresh5678","installId":"i-1"}`))
}

// Behind Tailscale Serve every peer arrives from loopback. They must not share
// one lockout bucket.
func TestServeLockoutIsKeyedPerTailnetPeer(t *testing.T) {
	m, _ := newPairingManager(t, "Code1234")
	from := func(peer, code string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, lanPairExchangePath, strings.NewReader(`{"code":"`+code+`","installId":"i-9"}`))
		r.RemoteAddr = "127.0.0.1:4000"
		r.Header.Set("X-Forwarded-For", peer)
		w := httptest.NewRecorder()
		m.dispatch().ServeHTTP(w, r)
		return w
	}
	for range sourceFailureLimit {
		from("100.64.0.9", "nope")
	}
	if w := from("100.64.0.9", "Code1234"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked peer = %d, want 429", w.Code)
	}
	if w := from("100.64.0.10", "Code1234"); w.Code != http.StatusOK {
		t.Fatalf("other peer behind the same Serve = %d, want 200", w.Code)
	}
}

func TestForgedForwardedForFromNonTailnetAddressIsIgnored(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:4000"
	r.Header.Set("X-Forwarded-For", "8.8.8.8")
	if got := clientSource(r); got != "127.0.0.1" {
		t.Fatalf("source = %q, want the loopback address", got)
	}
}

// A successful exchange is unauthenticated, so it must not trust its source
// past a spent global budget.
func TestExchangeDoesNotTrustItsSource(t *testing.T) {
	m, _ := newPairingManager(t, "Code1234")
	tokenFrom(t, exchange(m, "192.168.1.50:5000", "", `{"code":"Code1234","installId":"i-1"}`))
	if m.lock.trustedLocked(m.lock.now(), "192.168.1.50") {
		t.Fatal("pair/exchange marked its source trusted")
	}
}
